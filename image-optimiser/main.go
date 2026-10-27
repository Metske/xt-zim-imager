package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"image/jpeg"
	_ "image/gif"
	_ "golang.org/x/image/webp"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	zim "github.com/akhenakh/gozim"
	"github.com/disintegration/gift"
	"github.com/makeworld-the-better-one/dither/v2"
	"golang.org/x/image/draw"
	"regexp"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"mathjaxflatten"
)

var zReader *zim.ZimReader

func main() {
	rand.Seed(time.Now().UnixNano())
	http.HandleFunc("/", serveIndex)
	http.HandleFunc("/api/load", apiLoad)
	http.HandleFunc("/api/random", apiRandom)
	http.HandleFunc("/api/process", apiProcess)
	http.HandleFunc("/api/original", apiOriginal)

	port := "8085"
	fmt.Println("Image Optimiser running on http://localhost:" + port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}

func apiLoad(w http.ResponseWriter, r *http.Request) {
	path := r.FormValue("path")
	if path == "" {
		http.Error(w, "missing path", 400)
		return
	}
	if zReader != nil {
		zReader.Close()
	}
	reader, err := zim.NewReader(path, false)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	zReader = reader
	fmt.Fprintf(w, `{"status":"ok", "articles": %d}`, reader.ArticleCount)
}

func apiRandom(w http.ResponseWriter, r *http.Request) {
	if zReader == nil {
		http.Error(w, "zim not loaded", 400)
		return
	}
	// find random image
	for {
		idx := uint32(rand.Intn(int(zReader.ArticleCount)))
		art, err := zReader.ArticleAtURLIdx(idx)
		if err == nil && strings.HasPrefix(art.MimeType(), "image/") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"index": idx,
				"url":   art.FullURL(),
				"mime":  art.MimeType(),
			})
			return
		}
	}
}

func apiOriginal(w http.ResponseWriter, r *http.Request) {
	idxStr := r.URL.Query().Get("idx")
	idx, _ := strconv.Atoi(idxStr)
	art, err := zReader.ArticleAtURLIdx(uint32(idx))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	b, _ := art.Data()
	w.Header().Set("Content-Type", art.MimeType())
	w.Write(b)
}
func simulateFreeInkToneMapper(src image.Image, numLevels int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	
	dst := image.NewRGBA(bounds)
	
	step := 255 / (numLevels - 1)
	
	errCur := make([]int32, width+2)
	errNext := make([]int32, width+2)
	
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := color.GrayModel.Convert(src.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.Gray)
			v := int32(c.Y) + errCur[x+1]
			
			if v < 0 { v = 0 }
			if v > 255 { v = 255 }
			
			q := (v + int32(step)/2) / int32(step)
			if q < 0 { q = 0 }
			maxLevel := int32(numLevels - 1)
			if q > maxLevel { q = maxLevel }
			
			outVal := uint8(q * int32(step))
			dst.SetRGBA(bounds.Min.X+x, bounds.Min.Y+y, color.RGBA{outVal, outVal, outVal, 255})
			
			err := v - q*int32(step)
			
			errCur[x+2] += err * 7 / 16
			errNext[x] += err * 3 / 16
			errNext[x+1] += err * 5 / 16
			errNext[x+2] += err * 1 / 16
		}
		
		errCur, errNext = errNext, errCur
		for i := range errNext {
			errNext[i] = 0
		}
	}
	
	return dst
}

func apiProcess(w http.ResponseWriter, r *http.Request) {
	idxStr := r.URL.Query().Get("idx")
	idx, _ := strconv.Atoi(idxStr)
	
	brightness, _ := strconv.ParseFloat(r.URL.Query().Get("brightness"), 64)
	contrast, _ := strconv.ParseFloat(r.URL.Query().Get("contrast"), 64)
	gamma, _ := strconv.ParseFloat(r.URL.Query().Get("gamma"), 64)
	bitDepth, _ := strconv.Atoi(r.URL.Query().Get("bits"))
	if bitDepth == 0 {
		bitDepth = 1
	}
	algo := r.URL.Query().Get("algo")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "png"
	}
	
	sharpen, _ := strconv.ParseFloat(r.URL.Query().Get("sharpen"), 64)
	whiteClip, _ := strconv.ParseFloat(r.URL.Query().Get("whiteclip"), 64)
	if r.URL.Query().Get("whiteclip") == "" { whiteClip = 240 }
	blackClip, _ := strconv.ParseFloat(r.URL.Query().Get("blackclip"), 64)
	if r.URL.Query().Get("blackclip") == "" { blackClip = 13 }

	art, err := zReader.ArticleAtURLIdx(uint32(idx))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	b, _ := art.Data()
	
	var img image.Image
	mimeType := art.MimeType()
	
	limit := 200
	if len(b) < limit {
		limit = len(b)
	}
	if strings.Contains(mimeType, "svg") || strings.Contains(string(b[:limit]), "<svg") {
		log.Printf("Detected SVG. Length: %d, Mime: %s\n", len(b), mimeType)
		
		sanitizedStr := strings.ReplaceAll(string(b), "currentColor", "#000000")

		// Try MathJaxFlatten first
		opts := mathjaxflatten.DefaultOptions
		reEx := regexp.MustCompile(`width=["']([0-9\.]+)ex["']`)
		if matches := reEx.FindStringSubmatch(sanitizedStr); len(matches) > 1 {
			if exW, errFloat := strconv.ParseFloat(matches[1], 64); errFloat == nil {
				computedW := int(exW * 10)
				if computedW > 0 {
					opts.TargetWidth = computedW
				}
			}
		} else if strings.Contains(sanitizedStr, "viewBox") {
			reVB := regexp.MustCompile(`viewBox=["'][^"']*?\s[^"']*?\s([0-9\.]+)\s`)
			if matches := reVB.FindStringSubmatch(sanitizedStr); len(matches) > 1 {
				if vbW, errFloat := strconv.ParseFloat(matches[1], 64); errFloat == nil {
					computedW := int(vbW * 0.02)
					if computedW > 0 {
						opts.TargetWidth = computedW
					}
				}
			}
		}
		
		mjImg, mjErr := mathjaxflatten.Render(bytes.NewReader([]byte(sanitizedStr)), opts)
		if mjErr == nil && mjImg != nil {
			img = mjImg
		} else {
			// Fallback to oksvg
			re1 := regexp.MustCompile(`([0-9\.])([a-zA-Z])`)
			sanitized := re1.ReplaceAll([]byte(sanitizedStr), []byte("${1} ${2}"))
			re2 := regexp.MustCompile(`([a-zA-Z])([0-9\.\-])`)
			sanitized = re2.ReplaceAll(sanitized, []byte("${1} ${2}"))
			
			icon, errSvg := oksvg.ReadIconStream(bytes.NewReader(sanitized))
			if errSvg == nil {
				if icon.ViewBox.W == 0 || icon.ViewBox.H == 0 {
					re := regexp.MustCompile(`viewBox=["']([0-9\.\-]+)\s+([0-9\.\-]+)\s+([0-9\.\-]+)\s+([0-9\.\-]+)["']`)
					matches := re.FindStringSubmatch(sanitizedStr)
					if len(matches) == 5 {
						x, _ := strconv.ParseFloat(matches[1], 64)
						y, _ := strconv.ParseFloat(matches[2], 64)
						w, _ := strconv.ParseFloat(matches[3], 64)
						h, _ := strconv.ParseFloat(matches[4], 64)
						icon.ViewBox.X = x
						icon.ViewBox.Y = y
						icon.ViewBox.W = w
						icon.ViewBox.H = h
					}
				}
				iconW, iconH := int(icon.ViewBox.W), int(icon.ViewBox.H)
				log.Printf("Parsed SVG viewbox: %d x %d\n", iconW, iconH)
				if iconW == 0 || iconH == 0 {
					iconW, iconH = 520, 520
				}
				if iconW > 1024 {
					scale := 1024.0 / float64(iconW)
					iconW = 1024
					iconH = int(float64(iconH) * scale)
				}
				if iconH > 1024 {
					scale := 1024.0 / float64(iconH)
					iconH = 1024
					iconW = int(float64(iconW) * scale)
				}
				icon.SetTarget(0, 0, float64(iconW), float64(iconH))
				rgbaSvg := image.NewRGBA(image.Rect(0, 0, iconW, iconH))
				scanner := rasterx.NewScannerGV(iconW, iconH, rgbaSvg, rgbaSvg.Bounds())
				raster := rasterx.NewDasher(iconW, iconH, scanner)
				icon.Draw(raster, 1.0)
				img = rgbaSvg
			} else {
				log.Printf("SVG parse error: %v\n", errSvg)
			}
		}
	}
	
	if img == nil {
		var err error
		img, _, err = image.Decode(bytes.NewReader(b))
		if err != nil {
			// Fallback: create a blank placeholder with an X for unparseable complex SVGs
			placeholder := image.NewRGBA(image.Rect(0, 0, 10, 10))
			draw.Draw(placeholder, placeholder.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
			// Draw a simple X
			for i := 0; i < 10; i++ {
				placeholder.Set(i, i, color.Black)
				placeholder.Set(9-i, i, color.Black)
			}
			img = placeholder
		}
	}

	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Over)
	
	const (
		MaxWidth  = 520
		MaxHeight = 650
	)
	var finalImg image.Image = rgba
	
	originalWidth := bounds.Dx()
	originalHeight := bounds.Dy()

	if originalWidth > MaxWidth || originalHeight > MaxHeight {
		widthRatio := float64(MaxWidth) / float64(originalWidth)
		heightRatio := float64(MaxHeight) / float64(originalHeight)

		var scale float64
		if widthRatio < heightRatio {
			scale = widthRatio
		} else {
			scale = heightRatio
		}

		newWidth := int(float64(originalWidth) * scale)
		newHeight := int(float64(originalHeight) * scale)

		dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		draw.BiLinear.Scale(dst, dst.Bounds(), rgba, bounds, draw.Over, nil)
		finalImg = dst
	}

	if gamma == 0 { gamma = 1.0 }
	
	g := gift.New(gift.Grayscale())
	if sharpen > 0 {
		g.Add(gift.UnsharpMask(1.0, float32(sharpen), 0))
	}
	g.Add(
		gift.Brightness(float32(brightness)),
		gift.Contrast(float32(contrast)),
		gift.Gamma(float32(gamma)),
		gift.ColorFunc(func(r0, g0, b0, a0 float32) (float32, float32, float32, float32) {
			wc := float32(whiteClip) / 255.0
			bc := float32(blackClip) / 255.0
			clip := func(v float32) float32 {
				if wc <= bc { return v }
				if v >= wc { return 1.0 }
				if v <= bc { return 0.0 }
				return (v - bc) / (wc - bc)
			}
			return clip(r0), clip(g0), clip(b0), a0
		}),
	)
	
	filtered := image.NewGray(g.Bounds(finalImg.Bounds()))
	g.Draw(filtered, finalImg)
	
	simulate := r.URL.Query().Get("simulate") == "true"
	
	if format == "jpeg" {
		if simulate {
			var buf bytes.Buffer
			jpeg.Encode(&buf, filtered, &jpeg.Options{Quality: 30})
			decodedJpeg, _, _ := image.Decode(&buf)
			numLevels := 2
			if bitDepth == 2 { numLevels = 4 }
			simulated := simulateFreeInkToneMapper(decodedJpeg, numLevels)
			w.Header().Set("Content-Type", "image/png")
			png.Encode(w, simulated)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		jpeg.Encode(w, filtered, &jpeg.Options{Quality: 30})
		return
	}

	var pal []color.Color
	if bitDepth == 2 {
		pal = []color.Color{color.Black, color.Gray{Y: 85}, color.Gray{Y: 170}, color.White}
	} else {
		pal = []color.Color{color.Black, color.White}
	}

	ditherer := dither.NewDitherer(pal)
	switch algo {
	case "atkinson":
		ditherer.Matrix = dither.Atkinson
	case "sierra":
		ditherer.Matrix = dither.Sierra
	case "bayer":
		ditherer.Mapper = dither.Bayer(8, 8, 1.0)
	default:
		ditherer.Matrix = dither.FloydSteinberg
	}

	dithered := ditherer.Dither(filtered)

	w.Header().Set("Content-Type", "image/png")
	png.Encode(w, dithered)
}
