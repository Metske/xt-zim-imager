package mathjaxflatten

import (
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/srwiley/rasterx"
)

// Options allows configuring the rasterization output.
type Options struct {
	// TargetWidth is the desired width in pixels.
	// The height will be calculated automatically to maintain the aspect ratio.
	// If 0, it defaults to 800px.
	TargetWidth int

	// Background is the color used to fill the image before drawing the SVG.
	// Defaults to white.
	Background color.Color
}

// DefaultOptions provides a standard configuration.
var DefaultOptions = Options{
	TargetWidth: 800,
	Background:  color.White,
}

// Render reads a MathJax SVG from the given io.Reader, parses it, and renders it
// into an image.Image using the specified options.
func Render(r io.Reader, opts Options) (image.Image, error) {
	if opts.TargetWidth <= 0 {
		opts.TargetWidth = DefaultOptions.TargetWidth
	}
	if opts.Background == nil {
		opts.Background = DefaultOptions.Background
	}

	svgRoot, defsMap, err := parseSVG(r)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SVG: %w", err)
	}

	vb, err := parseViewBox(svgRoot.ViewBox)
	if err != nil {
		return nil, err
	}

	if vb.W <= 0 || vb.H <= 0 {
		return nil, fmt.Errorf("invalid viewBox dimensions: w=%v, h=%v", vb.W, vb.H)
	}

	scale := float64(opts.TargetWidth) / vb.W
	targetHeight := int(vb.H * scale)

	// Base transform to map viewBox to target dimensions
	// viewBox usually has negative Y in MathJax, e.g. "0 -1006 13706 1439"
	baseMatrix := rasterx.Matrix2D{
		A: scale, B: 0, C: 0,
		D: scale, E: -vb.X * scale, F: -vb.Y * scale,
	}

	// Collect all draw ops
	var allOps []*rasterx.Path
	for _, g := range svgRoot.Groups {
		allOps = append(allOps, collectDrawOps(g, defsMap, baseMatrix)...)
	}

	// Prepare image
	img := image.NewRGBA(image.Rect(0, 0, opts.TargetWidth, targetHeight))
	// Fill background
	r16, g16, b16, a16 := opts.Background.RGBA()
	if r16 == 0xffff && g16 == 0xffff && b16 == 0xffff && a16 == 0xffff {
		for i := range img.Pix {
			img.Pix[i] = 0xff
		}
	} else {
		r8, g8, b8, a8 := uint8(r16>>8), uint8(g16>>8), uint8(b16>>8), uint8(a16>>8)
		pix := img.Pix
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i] = r8
			pix[i+1] = g8
			pix[i+2] = b8
			pix[i+3] = a8
		}
	}

	// Set up scanner
	scanner := rasterx.NewScannerGV(opts.TargetWidth, targetHeight, img, img.Bounds())
	filler := rasterx.NewFiller(opts.TargetWidth, targetHeight, scanner)
	
	// Draw each op
	for _, path := range allOps {
		filler.Clear()
		path.AddTo(filler)
		filler.SetColor(color.Black) // MathJax is usually black
		filler.Draw()
	}

	return img, nil
}
