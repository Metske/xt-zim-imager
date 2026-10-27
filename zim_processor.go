package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	goRuntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto/md5"
	"encoding/hex"
	zim "github.com/akhenakh/gozim"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
	stdhtml "html"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	"github.com/makeworld-the-better-one/dither/v2"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"mathjaxflatten"
)

func hashImageKey(name string) string {
	hash := md5.Sum([]byte(name))
	return hex.EncodeToString(hash[:])
}

const (
	NodeSize      = 512
	MaxKeys       = 6
	KeySize       = 64
	btreeChunkCap = 65536 // 65,536 nodes per chunk (~30 MB per chunk)
)

// FlatBTreeNode contains zero pointers so the Go GC marks its backing slice as noscan.
type FlatBTreeNode struct {
	Keys     [MaxKeys][KeySize]byte
	Offsets  [MaxKeys]uint64
	Children [MaxKeys + 1]uint32
	SectorID uint32
	NumKeys  uint8
	IsLeaf   bool
}

type BTree struct {
	chunks   [][]FlatBTreeNode
	numNodes uint32
	root     uint32
}

func NewBTree() *BTree {
	t := &BTree{
		chunks: make([][]FlatBTreeNode, 0, 128),
	}
	t.root = t.allocNode(true)
	return t
}

func (t *BTree) allocNode(isLeaf bool) uint32 {
	if len(t.chunks) == 0 || len(t.chunks[len(t.chunks)-1]) == btreeChunkCap {
		t.chunks = append(t.chunks, make([]FlatBTreeNode, 0, btreeChunkCap))
	}
	last := len(t.chunks) - 1
	t.chunks[last] = append(t.chunks[last], FlatBTreeNode{IsLeaf: isLeaf})
	id := t.numNodes
	t.numNodes++
	return id
}

func (t *BTree) node(id uint32) *FlatBTreeNode {
	return &t.chunks[id>>16][id&0xFFFF]
}

func (t *BTree) Insert(key []byte, offset uint64) {
	t.InsertIfAbsent(key, offset)
}

func (t *BTree) InsertIfAbsent(key []byte, offset uint64) bool {
	if len(key) == 0 {
		return false
	}
	var kPad [KeySize]byte
	copy(kPad[:], key)

	rootNode := t.node(t.root)
	for j := uint8(0); j < rootNode.NumKeys; j++ {
		if rootNode.Keys[j] == kPad {
			return false
		}
	}

	if rootNode.NumKeys == MaxKeys {
		oldRoot := t.root
		newRoot := t.allocNode(false)
		t.node(newRoot).Children[0] = oldRoot
		t.splitChild(newRoot, 0)
		t.root = newRoot
	}

	currID := t.root
	for {
		n := t.node(currID)
		i := 0
		numK := int(n.NumKeys)
		for i < numK {
			if n.Keys[i] == kPad {
				return false
			}
			if bytes.Compare(kPad[:], n.Keys[i][:]) < 0 {
				break
			}
			i++
		}

		if n.IsLeaf {
			for j := numK; j > i; j-- {
				n.Keys[j] = n.Keys[j-1]
				n.Offsets[j] = n.Offsets[j-1]
			}
			n.Keys[i] = kPad
			n.Offsets[i] = offset
			n.NumKeys++
			return true
		}

		childID := n.Children[i]
		child := t.node(childID)
		for j := uint8(0); j < child.NumKeys; j++ {
			if child.Keys[j] == kPad {
				return false
			}
		}

		if child.NumKeys == MaxKeys {
			t.splitChild(currID, i)
			n = t.node(currID)
			if n.Keys[i] == kPad {
				return false
			}
			if bytes.Compare(kPad[:], n.Keys[i][:]) > 0 {
				i++
			}
		}
		currID = t.node(currID).Children[i]
	}
}

func (t *BTree) splitChild(parentID uint32, i int) {
	yID := t.node(parentID).Children[i]
	yIsLeaf := t.node(yID).IsLeaf
	zID := t.allocNode(yIsLeaf)

	p := t.node(parentID)
	y := t.node(yID)
	z := t.node(zID)

	const mid = MaxKeys / 2 // 3
	rightCount := int(y.NumKeys) - (mid + 1)

	for j := 0; j < rightCount; j++ {
		z.Keys[j] = y.Keys[mid+1+j]
		z.Offsets[j] = y.Offsets[mid+1+j]
		y.Keys[mid+1+j] = [KeySize]byte{}
		y.Offsets[mid+1+j] = 0
	}
	if !y.IsLeaf {
		for j := 0; j <= rightCount; j++ {
			z.Children[j] = y.Children[mid+1+j]
			y.Children[mid+1+j] = 0
		}
	}
	z.NumKeys = uint8(rightCount)

	upKey := y.Keys[mid]
	upOffset := y.Offsets[mid]
	y.Keys[mid] = [KeySize]byte{}
	y.Offsets[mid] = 0
	y.NumKeys = uint8(mid)

	pNum := int(p.NumKeys)
	for j := pNum + 1; j > i+1; j-- {
		p.Children[j] = p.Children[j-1]
	}
	p.Children[i+1] = zID

	for j := pNum; j > i; j-- {
		p.Keys[j] = p.Keys[j-1]
		p.Offsets[j] = p.Offsets[j-1]
	}
	p.Keys[i] = upKey
	p.Offsets[i] = upOffset
	p.NumKeys++
}

func (t *BTree) SerializeTo(w io.Writer) (int, int64, error) {
	bfsOrder := make([]uint32, 0, t.numNodes)
	bfsOrder = append(bfsOrder, t.root)

	for idx := 0; idx < len(bfsOrder); idx++ {
		nodeID := bfsOrder[idx]
		n := t.node(nodeID)
		n.SectorID = uint32(idx)
		if !n.IsLeaf {
			for c := uint8(0); c <= n.NumKeys; c++ {
				bfsOrder = append(bfsOrder, n.Children[c])
			}
		}
	}

	bw := bufio.NewWriterSize(w, 4*1024*1024)
	var block [NodeSize]byte
	var totalWritten int64

	for _, nodeID := range bfsOrder {
		n := t.node(nodeID)
		block = [NodeSize]byte{}
		if n.IsLeaf {
			block[0] = 1
		}
		block[1] = n.NumKeys

		pos := 4
		for i := uint8(0); i < n.NumKeys; i++ {
			copy(block[pos:pos+KeySize], n.Keys[i][:])
			pos += KeySize
		}

		pos = 4 + KeySize*MaxKeys
		for i := uint8(0); i < n.NumKeys; i++ {
			binary.LittleEndian.PutUint64(block[pos:], n.Offsets[i])
			pos += 8
		}

		if !n.IsLeaf {
			pos = 4 + KeySize*MaxKeys + 8*MaxKeys
			for i := uint8(0); i <= n.NumKeys; i++ {
				childSec := t.node(n.Children[i]).SectorID
				binary.LittleEndian.PutUint32(block[pos:], childSec)
				pos += 4
			}
		}

		nw, err := bw.Write(block[:])
		totalWritten += int64(nw)
		if err != nil {
			return 0, totalWritten, err
		}
	}

	if err := bw.Flush(); err != nil {
		return 0, totalWritten, err
	}
	return len(bfsOrder), totalWritten, nil
}

func (t *BTree) Serialize(filename string) (int, error) {
	f, err := os.Create(filename)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	numNodes, _, err := t.SerializeTo(f)
	return numNodes, err
}

func hasInvisibleUTF8(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		c := s[i]
		if c == 0xE2 {
			b1, b2 := s[i+1], s[i+2]
			if (b1 == 0x80 && ((b2 >= 0x8B && b2 <= 0x8F) || (b2 >= 0xAA && b2 <= 0xAE))) ||
				(b1 == 0x81 && b2 >= 0xA0 && b2 <= 0xAF) {
				return true
			}
		} else if c == 0xEF && s[i+1] == 0xBB && s[i+2] == 0xBF {
			return true
		}
	}
	return false
}

func stripInvisibleAndDecode(text string) string {
	if strings.IndexByte(text, '&') != -1 {
		text = stdhtml.UnescapeString(text)
	}
	if !hasInvisibleUTF8(text) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if (r >= 0x200B && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) ||
			(r >= 0x2060 && r <= 0x206F) || r == 0xFEFF {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var (
	reSvgEx    = regexp.MustCompile(`width=["']([0-9\.]+)ex["']`)
	reSvgVB    = regexp.MustCompile(`viewBox=["'][^"']*?\s[^"']*?\s([0-9\.]+)\s`)
	reSvgPath1 = regexp.MustCompile(`([0-9\.])([a-zA-Z])`)
	reSvgPath2 = regexp.MustCompile(`([a-zA-Z])([0-9\.\-])`)
	reSvgVB4   = regexp.MustCompile(`viewBox=["']([0-9\.\-]+)\s+([0-9\.\-]+)\s+([0-9\.\-]+)\s+([0-9\.\-]+)["']`)
	svgTagBytes = []byte("<svg")

	lutJPEG [4081]uint8
	lutPNG  [4081]uint8
)

func init() {
	buildToneLUT(&lutJPEG, 25.0, 13.0, 240.0)
	buildToneLUT(&lutPNG, 13.0, 15.0, 240.0)
}

func buildToneLUT(lut *[4081]uint8, contrastPct, blackClip, whiteClip float32) {
	c := 1.0 + contrastPct/100.0
	bc := blackClip / 255.0
	wc := whiteClip / 255.0
	for i := 0; i <= 4080; i++ {
		v := float32(i) / 4080.0
		// Contrast
		v = (v-0.5)*c + 0.5
		if v < 0 {
			v = 0
		} else if v > 1 {
			v = 1
		}
		// Black/White clip
		if wc > bc {
			if v >= wc {
				v = 1.0
			} else if v <= bc {
				v = 0.0
			} else {
				v = (v - bc) / (wc - bc)
			}
		}
		out := int(v*255.0 + 0.5)
		if out < 0 {
			out = 0
		} else if out > 255 {
			out = 255
		}
		lut[i] = uint8(out)
	}
}

type imgScratch struct {
	grayA   []uint8
	grayB   []uint8
	blurTmp []uint16
}

var imgScratchPool = sync.Pool{
	New: func() interface{} {
		return &imgScratch{
			grayA:   make([]uint8, 520*650),
			grayB:   make([]uint8, 520*650),
			blurTmp: make([]uint16, 520*650),
		}
	},
}

type pngEncPool struct {
	pool sync.Pool
}

func (p *pngEncPool) Get() *png.EncoderBuffer {
	if v := p.pool.Get(); v != nil {
		return v.(*png.EncoderBuffer)
	}
	return nil
}

func (p *pngEncPool) Put(b *png.EncoderBuffer) {
	p.pool.Put(b)
}

var sharedPNGEncoder = png.Encoder{
	BufferPool: &pngEncPool{},
}

type flushBuffer struct {
	*bytes.Buffer
}

func (flushBuffer) Flush() error { return nil }

func toGrayImage(img image.Image, scratch *imgScratch) *image.Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewGray(image.Rect(0, 0, 1, 1))
	}

	switch src := img.(type) {
	case *image.YCbCr:
		off := src.YOffset(b.Min.X, b.Min.Y)
		return &image.Gray{
			Pix:    src.Y[off:],
			Stride: src.YStride,
			Rect:   image.Rect(0, 0, w, h),
		}
	case *image.Gray:
		off := src.PixOffset(b.Min.X, b.Min.Y)
		return &image.Gray{
			Pix:    src.Pix[off:],
			Stride: src.Stride,
			Rect:   image.Rect(0, 0, w, h),
		}
	}

	nPix := w * h
	if cap(scratch.grayA) < nPix {
		scratch.grayA = make([]uint8, nPix)
	}
	dstPix := scratch.grayA[:nPix]

	switch src := img.(type) {
	case *image.NYCbCrA:
		idx := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			yOff := (y - src.Rect.Min.Y) * src.YStride
			aOff := (y - src.Rect.Min.Y) * src.AStride
			for x := b.Min.X; x < b.Max.X; x++ {
				dx := x - src.Rect.Min.X
				luma := uint32(src.Y[yOff+dx])
				alpha := uint32(src.A[aOff+dx])
				dstPix[idx] = uint8((luma*alpha + 255*(255-alpha) + 127) / 255)
				idx++
			}
		}
	case *image.RGBA:
		idx := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			row := src.Pix[(y-src.Rect.Min.Y)*src.Stride+(b.Min.X-src.Rect.Min.X)*4:]
			for x := 0; x < w; x++ {
				p := row[x*4 : x*4+4 : x*4+4]
				invA := uint32(255 - p[3])
				r := uint32(p[0]) + invA
				g := uint32(p[1]) + invA
				bl := uint32(p[2]) + invA
				dstPix[idx] = uint8((19595*r + 38470*g + 7471*bl + 32768) >> 16)
				idx++
			}
		}
	case *image.NRGBA:
		idx := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			row := src.Pix[(y-src.Rect.Min.Y)*src.Stride+(b.Min.X-src.Rect.Min.X)*4:]
			for x := 0; x < w; x++ {
				p := row[x*4 : x*4+4 : x*4+4]
				r, g, bl, a := uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
				lum := (19595*r + 38470*g + 7471*bl + 32768) >> 16
				dstPix[idx] = uint8((lum*a + 255*(255-a) + 127) / 255)
				idx++
			}
		}
	case *image.Paletted:
		var palLUT [256]uint8
		for i, c := range src.Palette {
			if i >= 256 {
				break
			}
			r16, g16, b16, a16 := c.RGBA()
			invA := uint32(255 - (a16 >> 8))
			r := uint32(r16>>8) + invA
			g := uint32(g16>>8) + invA
			bl := uint32(b16>>8) + invA
			palLUT[i] = uint8((19595*r + 38470*g + 7471*bl + 32768) >> 16)
		}
		idx := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			row := src.Pix[(y-src.Rect.Min.Y)*src.Stride+(b.Min.X-src.Rect.Min.X):]
			for x := 0; x < w; x++ {
				dstPix[idx] = palLUT[row[x]]
				idx++
			}
		}
	default:
		idx := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r16, g16, b16, a16 := img.At(x, y).RGBA()
				invA := uint32(255 - (a16 >> 8))
				r := uint32(r16>>8) + invA
				g := uint32(g16>>8) + invA
				bl := uint32(b16>>8) + invA
				dstPix[idx] = uint8((19595*r + 38470*g + 7471*bl + 32768) >> 16)
				idx++
			}
		}
	}

	return &image.Gray{
		Pix:    dstPix,
		Stride: w,
		Rect:   image.Rect(0, 0, w, h),
	}
}

func scaleGrayBiLinear(src *image.Gray, newW, newH int, scratch *imgScratch) *image.Gray {
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()
	nPix := newW * newH
	if cap(scratch.grayB) < nPix {
		scratch.grayB = make([]uint8, nPix)
	}
	dstPix := scratch.grayB[:nPix]

	// 16.16 fixed-point coordinates
	xStep := (uint64(srcW) << 16) / uint64(newW)
	yStep := (uint64(srcH) << 16) / uint64(newH)

	for dy := 0; dy < newH; dy++ {
		syFixed := (uint64(dy)*yStep + (yStep >> 1))
		var y0 int
		var yf uint32
		if syFixed < 32768 {
			y0 = 0
			yf = 0
		} else {
			syFixed -= 32768
			y0 = int(syFixed >> 16)
			yf = uint32((syFixed >> 8) & 0xFF)
		}
		y1 := y0 + 1
		if y0 >= srcH {
			y0 = srcH - 1
		}
		if y1 >= srcH {
			y1 = srcH - 1
		}

		row0 := src.Pix[y0*src.Stride:]
		row1 := src.Pix[y1*src.Stride:]
		dstRow := dstPix[dy*newW : (dy+1)*newW]
		wy0 := 256 - yf
		wy1 := yf

		for dx := 0; dx < newW; dx++ {
			sxFixed := (uint64(dx)*xStep + (xStep >> 1))
			var x0 int
			var xf uint32
			if sxFixed < 32768 {
				x0 = 0
				xf = 0
			} else {
				sxFixed -= 32768
				x0 = int(sxFixed >> 16)
				xf = uint32((sxFixed >> 8) & 0xFF)
			}
			x1 := x0 + 1
			if x0 >= srcW {
				x0 = srcW - 1
			}
			if x1 >= srcW {
				x1 = srcW - 1
			}

			wx0 := 256 - xf
			wx1 := xf

			top := uint32(row0[x0])*wx0 + uint32(row0[x1])*wx1
			bot := uint32(row1[x0])*wx0 + uint32(row1[x1])*wx1
			dstRow[dx] = uint8((top*wy0 + bot*wy1 + 32768) >> 16)
		}
	}

	return &image.Gray{
		Pix:    dstPix,
		Stride: newW,
		Rect:   image.Rect(0, 0, newW, newH),
	}
}

func filterGrayUnsharpAndLUT(src *image.Gray, lut *[4081]uint8, scratch *imgScratch) *image.Gray {
	w := src.Rect.Dx()
	h := src.Rect.Dy()
	nPix := w * h
	if cap(scratch.blurTmp) < nPix {
		scratch.blurTmp = make([]uint16, nPix)
	}
	tmp := scratch.blurTmp[:nPix]

	// Output buffer: use grayA if src is not already backed by grayA, otherwise grayB
	var outPix []uint8
	if len(src.Pix) > 0 && len(scratch.grayA) > 0 && &src.Pix[0] == &scratch.grayA[0] {
		if cap(scratch.grayB) < nPix {
			scratch.grayB = make([]uint8, nPix)
		}
		outPix = scratch.grayB[:nPix]
	} else {
		if cap(scratch.grayA) < nPix {
			scratch.grayA = make([]uint8, nPix)
		}
		outPix = scratch.grayA[:nPix]
	}

	// 7-tap Gaussian weights (sigma=1.0, radius=3), sum = 65536
	const (
		w0 = 26158
		w1 = 15860
		w2 = 3538
		w3 = 291
	)

	// Pass 1: Horizontal 7-tap Gaussian blur (stores 8-bit fractional fixed-point in tmp: 0..65280)
	for y := 0; y < h; y++ {
		sRow := src.Pix[y*src.Stride : y*src.Stride+w]
		tRow := tmp[y*w : (y+1)*w]
		if w > 6 {
			// Left border (x = 0, 1, 2)
			for x := 0; x < 3; x++ {
				xm3 := x - 3
				if xm3 < 0 {
					xm3 = 0
				}
				xm2 := x - 2
				if xm2 < 0 {
					xm2 = 0
				}
				xm1 := x - 1
				if xm1 < 0 {
					xm1 = 0
				}
				sum := uint32(sRow[x])*w0 +
					(uint32(sRow[xm1])+uint32(sRow[x+1]))*w1 +
					(uint32(sRow[xm2])+uint32(sRow[x+2]))*w2 +
					(uint32(sRow[xm3])+uint32(sRow[x+3]))*w3
				tRow[x] = uint16((sum + 128) >> 8)
			}
			// Interior (branchless, single bounds check per pixel)
			for x := 3; x < w-3; x++ {
				win := sRow[x-3 : x+4]
				sum := uint32(win[3])*w0 +
					(uint32(win[2])+uint32(win[4]))*w1 +
					(uint32(win[1])+uint32(win[5]))*w2 +
					(uint32(win[0])+uint32(win[6]))*w3
				tRow[x] = uint16((sum + 128) >> 8)
			}
			// Right border (x = w-3, w-2, w-1)
			for x := w - 3; x < w; x++ {
				xp1 := x + 1
				if xp1 >= w {
					xp1 = w - 1
				}
				xp2 := x + 2
				if xp2 >= w {
					xp2 = w - 1
				}
				xp3 := x + 3
				if xp3 >= w {
					xp3 = w - 1
				}
				sum := uint32(sRow[x])*w0 +
					(uint32(sRow[x-1])+uint32(sRow[xp1]))*w1 +
					(uint32(sRow[x-2])+uint32(sRow[xp2]))*w2 +
					(uint32(sRow[x-3])+uint32(sRow[xp3]))*w3
				tRow[x] = uint16((sum + 128) >> 8)
			}
		} else {
			for x := 0; x < w; x++ {
				xm3, xm2, xm1 := x-3, x-2, x-1
				if xm3 < 0 {
					xm3 = 0
				}
				if xm2 < 0 {
					xm2 = 0
				}
				if xm1 < 0 {
					xm1 = 0
				}
				xp1, xp2, xp3 := x+1, x+2, x+3
				if xp1 >= w {
					xp1 = w - 1
				}
				if xp2 >= w {
					xp2 = w - 1
				}
				if xp3 >= w {
					xp3 = w - 1
				}
				sum := uint32(sRow[x])*w0 +
					(uint32(sRow[xm1])+uint32(sRow[xp1]))*w1 +
					(uint32(sRow[xm2])+uint32(sRow[xp2]))*w2 +
					(uint32(sRow[xm3])+uint32(sRow[xp3]))*w3
				tRow[x] = uint16((sum + 128) >> 8)
			}
		}
	}

	// Pass 2: Vertical 7-tap Gaussian blur + UnsharpMask(amount=1.0) + Contrast/Clip LUT
	for y := 0; y < h; y++ {
		ym3, ym2, ym1 := y-3, y-2, y-1
		if ym3 < 0 {
			ym3 = 0
		}
		if ym2 < 0 {
			ym2 = 0
		}
		if ym1 < 0 {
			ym1 = 0
		}
		yp1, yp2, yp3 := y+1, y+2, y+3
		if yp1 >= h {
			yp1 = h - 1
		}
		if yp2 >= h {
			yp2 = h - 1
		}
		if yp3 >= h {
			yp3 = h - 1
		}

		r0 := tmp[y*w : (y+1)*w]
		rm1 := tmp[ym1*w : (ym1+1)*w]
		rp1 := tmp[yp1*w : (yp1+1)*w]
		rm2 := tmp[ym2*w : (ym2+1)*w]
		rp2 := tmp[yp2*w : (yp2+1)*w]
		rm3 := tmp[ym3*w : (ym3+1)*w]
		rp3 := tmp[yp3*w : (yp3+1)*w]

		sRow := src.Pix[y*src.Stride : y*src.Stride+w]
		dRow := outPix[y*w : (y+1)*w]

		for x := 0; x < w; x++ {
			vSum := uint32(r0[x])*w0 +
				(uint32(rm1[x])+uint32(rp1[x]))*w1 +
				(uint32(rm2[x])+uint32(rp2[x]))*w2 +
				(uint32(rm3[x])+uint32(rp3[x]))*w3
			// vSum has 24 fractional bits (8 from Pass 1, 16 from Pass 2 -> shift right 20 to leave 4 fractional bits -> 0..4080)
			blur12 := int((vSum + (1 << 19)) >> 20)
			orig12 := int(sRow[x]) << 4
			sharp12 := 2*orig12 - blur12
			if uint(sharp12) > 4080 {
				if sharp12 < 0 {
					sharp12 = 0
				} else {
					sharp12 = 4080
				}
			}
			dRow[x] = lut[sharp12]
		}
	}

	return &image.Gray{
		Pix:    outPix,
		Stride: w,
		Rect:   image.Rect(0, 0, w, h),
	}
}

func processImageBytes(imgData []byte, bitDepth int, mimeType string, ditherAlgo string, imageFormat string) []byte {
	var img image.Image
	var err error

	limit := 200
	if len(imgData) < limit {
		limit = len(imgData)
	}
	if strings.Contains(mimeType, "svg") || bytes.Contains(imgData[:limit], svgTagBytes) {
		sanitizedStr := strings.ReplaceAll(string(imgData), "currentColor", "#000000")

		opts := mathjaxflatten.DefaultOptions

		if matches := reSvgEx.FindStringSubmatch(sanitizedStr); len(matches) > 1 {
			if exW, errFloat := strconv.ParseFloat(matches[1], 64); errFloat == nil {
				computedW := int(exW * 10)
				if computedW > 0 {
					opts.TargetWidth = computedW
				}
			}
		} else if strings.Contains(sanitizedStr, "viewBox") {
			if matches := reSvgVB.FindStringSubmatch(sanitizedStr); len(matches) > 1 {
				if vbW, errFloat := strconv.ParseFloat(matches[1], 64); errFloat == nil {
					computedW := int(vbW * 0.02)
					if computedW > 0 {
						opts.TargetWidth = computedW
					}
				}
			}
		}

		mjImg, mjErr := mathjaxflatten.Render(strings.NewReader(sanitizedStr), opts)
		if mjErr == nil && mjImg != nil {
			img = mjImg
		} else {
			sanitized := reSvgPath1.ReplaceAll([]byte(sanitizedStr), []byte("${1} ${2}"))
			sanitized = reSvgPath2.ReplaceAll(sanitized, []byte("${1} ${2}"))

			icon, errSvg := oksvg.ReadIconStream(bytes.NewReader(sanitized))
			if errSvg == nil {
				if icon.ViewBox.W == 0 || icon.ViewBox.H == 0 {
					matches := reSvgVB4.FindStringSubmatch(sanitizedStr)
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
				if iconW <= 0 || iconH <= 0 {
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
				if iconW < 1 {
					iconW = 1
				}
				if iconH < 1 {
					iconH = 1
				}
				icon.SetTarget(0, 0, float64(iconW), float64(iconH))
				rgbaSvg := image.NewRGBA(image.Rect(0, 0, iconW, iconH))
				scanner := rasterx.NewScannerGV(iconW, iconH, rgbaSvg, rgbaSvg.Bounds())
				raster := rasterx.NewDasher(iconW, iconH, scanner)
				icon.Draw(raster, 1.0)
				img = rgbaSvg
			}
		}
	}

	if img == nil {
		img, _, err = image.Decode(bytes.NewReader(imgData))
		if err != nil {
			placeholder := image.NewGray(image.Rect(0, 0, 10, 10))
			for i := range placeholder.Pix {
				placeholder.Pix[i] = 255
			}
			for i := 0; i < 10; i++ {
				placeholder.Pix[i*10+i] = 0
				placeholder.Pix[i*10+(9-i)] = 0
			}
			img = placeholder
		}
	}

	scratch := imgScratchPool.Get().(*imgScratch)
	defer imgScratchPool.Put(scratch)

	grayImg := toGrayImage(img, scratch)

	const (
		MaxWidth  = 520
		MaxHeight = 650
	)
	originalWidth := grayImg.Rect.Dx()
	originalHeight := grayImg.Rect.Dy()

	if originalWidth > MaxWidth || originalHeight > MaxHeight {
		widthRatio := float64(MaxWidth) / float64(originalWidth)
		heightRatio := float64(MaxHeight) / float64(originalHeight)

		scale := widthRatio
		if heightRatio < widthRatio {
			scale = heightRatio
		}

		newWidth := int(float64(originalWidth) * scale)
		newHeight := int(float64(originalHeight) * scale)
		if newWidth < 1 {
			newWidth = 1
		}
		if newHeight < 1 {
			newHeight = 1
		}

		grayImg = scaleGrayBiLinear(grayImg, newWidth, newHeight, scratch)
	}

	lut := &lutPNG
	if imageFormat == "jpeg" {
		lut = &lutJPEG
	}
	filtered := filterGrayUnsharpAndLUT(grayImg, lut, scratch)

	if imageFormat == "jpeg" {
		var out bytes.Buffer
		out.Grow(8192)
		err = jpeg.Encode(flushBuffer{&out}, filtered, &jpeg.Options{Quality: 30})
		if err != nil {
			return nil
		}
		return out.Bytes()
	}

	var pal []color.Color
	if bitDepth == 2 {
		pal = []color.Color{color.Black, color.Gray{Y: 85}, color.Gray{Y: 170}, color.White}
	} else {
		pal = []color.Color{color.Black, color.White}
	}

	ditherer := dither.NewDitherer(pal)
	ditherer.SingleThreaded = true
	switch ditherAlgo {
	case "atkinson":
		ditherer.Matrix = dither.Atkinson
	case "sierra":
		ditherer.Matrix = dither.Sierra
	case "bayer":
		ditherer.Mapper = dither.Bayer(8, 8, 1.0)
	case "none":
		ditherer.Matrix = nil
		ditherer.Mapper = nil
	default:
		ditherer.Matrix = dither.FloydSteinberg
	}

	dithered := ditherer.Dither(filtered)

	var out bytes.Buffer
	out.Grow(8192)
	err = sharedPNGEncoder.Encode(&out, dithered)
	if err != nil {
		return nil
	}
	return out.Bytes()
}

type CladeNode struct {
	Label       string
	SubLabel    string
	Description string
	ImageHash   string
	Children    []*CladeNode
}

func getAttrNode(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func hasClassNode(n *html.Node, class string) bool {
	return strings.Contains(getAttrNode(n, "class"), class)
}

func extractNodeText(n *html.Node) string {
	var buf bytes.Buffer
	var walk func(*html.Node)
	walk = func(curr *html.Node) {
		if curr.Type == html.ElementNode {
			if curr.Data == "style" || curr.Data == "script" {
				return
			}
			if curr.Data == "a" {
				href := getAttrNode(curr, "href")
				var linkText bytes.Buffer
				for c := curr.FirstChild; c != nil; c = c.NextSibling {
					linkText.WriteString(extractNodeText(c))
				}
				cleanHref := filepath.Base(href)
				if idx := strings.IndexAny(cleanHref, "?#"); idx != -1 {
					cleanHref = cleanHref[:idx]
				}
				if decoded, err := url.PathUnescape(cleanHref); err == nil && decoded != "" {
					cleanHref = decoded
				}
				txt := strings.TrimSpace(linkText.String())
				if cleanHref != "" && txt != "" {
					buf.WriteString(fmt.Sprintf(`<a href="%s">%s</a>`, cleanHref, txt))
					return
				}
			}
		} else if curr.Type == html.TextNode {
			txt := curr.Data
			txt = strings.ReplaceAll(txt, "\u00a0", " ")
			txt = strings.ReplaceAll(txt, "\u200b", "")
			txt = strings.ReplaceAll(txt, "\u2060", "")
			buf.WriteString(txt)
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(buf.String()), " ")
}

func extractImages(n *html.Node) []string {
	var imgs []string
	var walk func(*html.Node)
	walk = func(curr *html.Node) {
		if curr.Type == html.ElementNode && curr.Data == "img" {
			src := getAttrNode(curr, "src")
			if src != "" {
				filename := filepath.Base(src)
				if idx := strings.IndexAny(filename, "?#"); idx != -1 {
					filename = filename[:idx]
				}
				if decoded, err := url.PathUnescape(filename); err == nil && decoded != "" {
					filename = decoded
				}
				filename = strings.ReplaceAll(filename, "_", " ")
				imgs = append(imgs, hashImageKey(filename))
			}
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return imgs
}

func findFirstCladeTable(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.Data == "table" && hasClassNode(n, "clade") {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirstCladeTable(c); found != nil {
			return found
		}
	}
	return nil
}

func parseCladeTable(table *html.Node) []*CladeNode {
	if table == nil {
		return nil
	}

	var rows []*html.Node
	var findRows func(*html.Node)
	findRows = func(curr *html.Node) {
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "tr" {
				rows = append(rows, c)
			} else if c.Type == html.ElementNode && c.Data == "tbody" {
				findRows(c)
			}
		}
	}
	findRows(table)

	var nodes []*CladeNode

	for i := 0; i < len(rows); {
		tr := rows[i]
		i++

		var labelCell, leafCell, slabelCell *html.Node
		for c := tr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data != "td" {
				continue
			}
			if hasClassNode(c, "clade-label") {
				labelCell = c
			} else if hasClassNode(c, "clade-leaf") {
				leafCell = c
			} else if hasClassNode(c, "clade-slabel") {
				slabelCell = c
			}
		}

		if labelCell == nil && leafCell == nil {
			if slabelCell != nil && len(nodes) > 0 {
				txt := extractNodeText(slabelCell)
				if txt != "" && nodes[len(nodes)-1].SubLabel == "" {
					nodes[len(nodes)-1].SubLabel = txt
				}
			}
			continue
		}

		node := &CladeNode{}
		if labelCell != nil {
			node.Label = extractNodeText(labelCell)
		}

		if i < len(rows) {
			nextTr := rows[i]
			var nextSlabel *html.Node
			for c := nextTr.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "td" && hasClassNode(c, "clade-slabel") {
					nextSlabel = c
					break
				}
			}
			if nextSlabel != nil {
				txt := extractNodeText(nextSlabel)
				if txt != "" {
					node.SubLabel = txt
				}
				i++
			}
		}

		if leafCell != nil {
			childTable := findFirstCladeTable(leafCell)
			if childTable != nil {
				node.Children = parseCladeTable(childTable)
			} else {
				node.Description = extractNodeText(leafCell)
				imgs := extractImages(leafCell)
				if len(imgs) > 0 {
					node.ImageHash = imgs[0]
				}
			}
		}

		if node.Label == "" && node.SubLabel != "" {
			node.Label = node.SubLabel
			node.SubLabel = ""
		}

		if node.Label == "" && node.SubLabel == "" && node.Description == "" && node.ImageHash == "" && len(node.Children) > 0 {
			nodes = append(nodes, node.Children...)
		} else {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

func formatDendrogram(nodes []*CladeNode, prefix string, isTail bool, out *bytes.Buffer) {
	for i, node := range nodes {
		tail := i == len(nodes)-1

		branchConnector := "├── "
		if tail {
			branchConnector = "└── "
		}
		if prefix == "" && len(nodes) == 1 {
			branchConnector = ""
		}

		var lineTitle string
		if node.Label != "" {
			lineTitle = node.Label
		}
		if node.SubLabel != "" {
			if lineTitle != "" {
				lineTitle += fmt.Sprintf(" [%s]", node.SubLabel)
			} else {
				lineTitle = node.SubLabel
			}
		}
		if node.Description != "" {
			if lineTitle != "" {
				lineTitle += " " + node.Description
			} else {
				lineTitle = node.Description
			}
		}

		lineTitle = strings.ReplaceAll(lineTitle, "|", "\\|")
		lineTitle = strings.TrimSpace(lineTitle)

		col1 := prefix + branchConnector + lineTitle
		col2 := ""
		if node.ImageHash != "" {
			col2 = fmt.Sprintf("[IMG:%s]", node.ImageHash)
		}

		out.WriteString(fmt.Sprintf("%s|%s\n", col1, col2))

		childPrefix := prefix
		if branchConnector != "" {
			if tail {
				childPrefix += "    "
			} else {
				childPrefix += "│   "
			}
		}

		if len(node.Children) > 0 {
			formatDendrogram(node.Children, childPrefix, true, out)
		}
	}
}

func processClade(cladeBytes []byte, cladeCount int) string {
	doc, err := html.Parse(bytes.NewReader(cladeBytes))
	if err != nil {
		return ""
	}
	rootTable := findFirstCladeTable(doc)
	if rootTable == nil {
		return ""
	}
	tree := parseCladeTable(rootTable)
	if len(tree) == 0 {
		return ""
	}
	var out bytes.Buffer
	out.WriteString(fmt.Sprintf("===CLADE:%d===\n", cladeCount))
	out.WriteString("[H]Taxonomy / Cladogram|[H]Photo\n")
	formatDendrogram(tree, "", true, &out)
	return out.String()
}

func normalizeTablePayload(rawTable string) string {
	if !strings.HasPrefix(rawTable, "===TABLE:") {
		return rawTable
	}

	lines := strings.Split(rawTable, "\n")
	if len(lines) <= 1 {
		return rawTable
	}

	headerMarker := lines[0]
	bodyLines := lines[1:]

	type CellInfo struct {
		isHeader bool
		colSpan  int
		rowSpan  int
		text     string
		raw      string
	}

	type RowInfo struct {
		cells []CellInfo
	}

	var rows []RowInfo
	maxLogicalCols := 0

	for _, line := range bodyLines {
		line = strings.TrimSpace(line)
		if line == "" || line == "===END===" {
			continue
		}

		var cellStrs []string
		var curCell strings.Builder
		escaped := false
		for i := 0; i < len(line); i++ {
			c := line[i]
			if escaped {
				curCell.WriteByte(c)
				escaped = false
			} else if c == '\\' {
				curCell.WriteByte(c)
				escaped = true
			} else if c == '|' {
				cellStrs = append(cellStrs, curCell.String())
				curCell.Reset()
			} else {
				curCell.WriteByte(c)
			}
		}
		cellStrs = append(cellStrs, curCell.String())

		var row RowInfo
		logicalCols := 0
		hasNonEmptyCell := false
		for _, rawCell := range cellStrs {
			ci := CellInfo{colSpan: 1, rowSpan: 1, raw: rawCell}
			t := rawCell
			for strings.HasPrefix(t, "[H]") || (strings.HasPrefix(t, "[C") && strings.Contains(t, "]")) || (strings.HasPrefix(t, "[R") && strings.Contains(t, "]")) {
				if strings.HasPrefix(t, "[H]") {
					ci.isHeader = true
					t = t[3:]
				} else if strings.HasPrefix(t, "[C") {
					endIdx := strings.Index(t, "]")
					if val, err := strconv.Atoi(t[2:endIdx]); err == nil && val > 1 {
						ci.colSpan = val
					}
					t = t[endIdx+1:]
				} else if strings.HasPrefix(t, "[R") {
					endIdx := strings.Index(t, "]")
					if val, err := strconv.Atoi(t[2:endIdx]); err == nil && val > 1 {
						ci.rowSpan = val
					}
					t = t[endIdx+1:]
				}
			}
			parts := strings.Split(t, `\n`)
			var cleanParts []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					cleanParts = append(cleanParts, p)
				}
			}
			t = strings.Join(cleanParts, `\n`)
			if t != "" {
				hasNonEmptyCell = true
			}
			ci.text = t
			logicalCols += ci.colSpan
			row.cells = append(row.cells, ci)
		}
		if !hasNonEmptyCell {
			continue
		}
		if logicalCols > maxLogicalCols {
			maxLogicalCols = logicalCols
		}
		rows = append(rows, row)
	}

	if maxLogicalCols == 0 || len(rows) == 0 {
		return rawTable
	}

	// Track active rowSpans across rows so we only promote 1-cell rows to full-width banners
	// when no columns on that row are occupied by a rowSpan from an earlier row.
	activeSpanRem := make([]int, maxLogicalCols+32)
	for rIdx := range rows {
		spannedCols := 0
		for c := 0; c < len(activeSpanRem); c++ {
			if activeSpanRem[c] > 0 {
				spannedCols++
			}
		}
		if len(rows[rIdx].cells) == 1 && spannedCols == 0 && rows[rIdx].cells[0].colSpan < maxLogicalCols {
			rows[rIdx].cells[0].colSpan = maxLogicalCols
		}
		colCursor := 0
		for _, cell := range rows[rIdx].cells {
			for colCursor < len(activeSpanRem) && activeSpanRem[colCursor] > 0 {
				activeSpanRem[colCursor]--
				colCursor++
			}
			for s := 0; s < cell.colSpan && (colCursor+s) < len(activeSpanRem); s++ {
				if cell.rowSpan > 1 {
					activeSpanRem[colCursor+s] = cell.rowSpan - 1
				}
			}
			colCursor += cell.colSpan
		}
		for colCursor < len(activeSpanRem) {
			if activeSpanRem[colCursor] > 0 {
				activeSpanRem[colCursor]--
			}
			colCursor++
		}
	}

	colDesiredWidth := make([]int, maxLogicalCols)
	for c := 0; c < maxLogicalCols; c++ {
		colDesiredWidth[c] = 80
	}

	for _, row := range rows {
		colIdx := 0
		for _, cell := range row.cells {
			if cell.colSpan == 1 && colIdx < maxLogicalCols {
				words := strings.Fields(cell.text)
				maxWordPx := 0
				for _, w := range words {
					wLen := len(w) * 9
					if wLen > maxWordPx {
						maxWordPx = wLen
					}
				}
				totalPx := len(cell.text) * 8
				if totalPx > 220 {
					totalPx = 220
				}
				needed := maxWordPx + 16
				if needed < totalPx {
					needed = totalPx
				}
				if needed > colDesiredWidth[colIdx] {
					colDesiredWidth[colIdx] = needed
				}
			}
			colIdx += cell.colSpan
		}
	}

	for c := 0; c < maxLogicalCols; c++ {
		if c == 0 && maxLogicalCols > 1 {
			if colDesiredWidth[c] > 175 {
				colDesiredWidth[c] = 175
			}
			if colDesiredWidth[c] < 80 {
				colDesiredWidth[c] = 80
			}
		} else {
			if colDesiredWidth[c] > 240 {
				colDesiredWidth[c] = 240
			}
			if colDesiredWidth[c] < 80 {
				colDesiredWidth[c] = 80
			}
		}
	}

	totalW := 0
	for _, w := range colDesiredWidth {
		totalW += w
	}
	const UsableWidth = 496
	if totalW < UsableWidth && maxLogicalCols > 0 {
		extra := (UsableWidth - totalW) / maxLogicalCols
		for c := 0; c < maxLogicalCols; c++ {
			colDesiredWidth[c] += extra
		}
	}

	var colWidthStrs []string
	for _, w := range colDesiredWidth {
		colWidthStrs = append(colWidthStrs, strconv.Itoa(w))
	}
	colsTag := fmt.Sprintf("[COLS:%s]", strings.Join(colWidthStrs, ","))

	var out strings.Builder
	out.WriteString(headerMarker)
	out.WriteString("\n")
	out.WriteString(colsTag)
	out.WriteString("\n")

	for _, row := range rows {
		for cIdx, cell := range row.cells {
			if cIdx > 0 {
				out.WriteString("|")
			}
			if cell.isHeader {
				out.WriteString("[H]")
			}
			if cell.colSpan > 1 {
				out.WriteString(fmt.Sprintf("[C%d]", cell.colSpan))
			}
			if cell.rowSpan > 1 {
				out.WriteString(fmt.Sprintf("[R%d]", cell.rowSpan))
			}
			out.WriteString(cell.text)
		}
		out.WriteString("\n")
	}

	return out.String()
}

// ---------------------------------------------------------------------------
// Parsoid HTML 2.8.0 / mwparserfromhtml-style element classification helpers
// ---------------------------------------------------------------------------

// hasClassToken checks if a whitespace-separated class attribute contains any of the target tokens.
func hasClassToken(classAttr string, targets ...string) bool {
	for _, field := range strings.Fields(classAttr) {
		field = strings.TrimRight(field, ";")
		for _, target := range targets {
			if field == target {
				return true
			}
		}
	}
	return false
}

// attrVal returns the value of the named attribute, or "" if absent.
func attrVal(attrs []html.Attribute, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// isVoidElement reports HTML elements that never have a closing tag, so they must
// never be pushed onto open/close tracking stacks.
func isVoidElement(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// isContentWrapper detects the article body wrapper for both legacy (id="mw-content-text")
// and Parsoid 2.8.0 (class="... mw-parser-output ...") ZIM HTML.
func isContentWrapper(attrs []html.Attribute) bool {
	for _, a := range attrs {
		if a.Key == "id" && a.Val == "mw-content-text" {
			return true
		}
		if a.Key == "class" && strings.Contains(a.Val, "mw-parser-output") {
			return true
		}
	}
	return false
}

// isIgnoredBoxOrNav checks if a tag is a footer navbox, sister-project box, portalbox, or maintenance/stub box.
func isIgnoredBoxOrNav(tagName string, attrs []html.Attribute) bool {
	classVal := attrVal(attrs, "class")
	roleVal := attrVal(attrs, "role")

	// Do NOT ignore sidebars/infoboxes here; they are routed to the Infobox viewer.
	if tagName == "table" && (hasClassToken(classVal, "sidebar", "sidebar-collapse", "vertical-navbox") || strings.Contains(classVal, "infobox")) {
		return false
	}

	// 1. Footer navboxes & navigation containers
	if roleVal == "navigation" {
		return true
	}
	if hasClassToken(classVal, "navbox", "navbox-inner", "navbox-subgroup", "navbox-vertical", "navbox-columns-table", "authority-control", "catlinks", "zim-footer") {
		return true
	}

	// 2. Sister project & portal boxes
	if hasClassToken(classVal, "side-box", "sistersitebox", "sister-box", "portalbox", "portal-bar") {
		return true
	}

	// 3. Maintenance message boxes (*mbox) & stub notices (asbox)
	for _, field := range strings.Fields(classVal) {
		if strings.HasSuffix(field, "mbox") || field == "mbox-small" || field == "asbox" {
			return true
		}
	}

	return false
}

// classifyTable returns:
//
//	"ignore"  - for navboxes / maintenance tables that should be completely skipped
//	"unwrap"  - for layout/presentation tables (col-begin, multicol, role="presentation"/"none")
//	"infobox" - for infoboxes, taxoboxes, chemboxes, and sidebars
//	"table"   - for standard data tables
func classifyTable(attrs []html.Attribute) (kind string, isSidebar bool) {
	classVal := attrVal(attrs, "class")
	roleVal := attrVal(attrs, "role")

	// Check sidebar / infobox first (since sidebars carry role="navigation")
	if hasClassToken(classVal, "sidebar", "sidebar-collapse", "sidebar-person", "sidebar-games-events", "vertical-navbox") {
		return "infobox", true
	}
	for _, field := range strings.Fields(classVal) {
		if strings.Contains(field, "infobox") || field == "biota" || field == "taxobox" || strings.HasPrefix(field, "ib-") {
			return "infobox", false
		}
	}

	// Check ignored navboxes / messageboxes / TOC
	if isIgnoredBoxOrNav("table", attrs) || hasClassToken(classVal, "toc") {
		return "ignore", false
	}

	// Check layout / presentation wrapper tables
	if roleVal == "presentation" || roleVal == "none" || hasClassToken(classVal, "col-begin", "multicol") {
		return "unwrap", false
	}

	// Secondary microformat classes when NOT on a data table
	if hasClassToken(classVal, "vcard", "vevent", "haudio", "hproduct", "hrecipe") &&
		!hasClassToken(classVal, "wikitable", "tracklist", "wikiepisodetable") {
		return "infobox", false
	}

	return "table", false
}

// isInternalWikiLink checks whether an <a> tag is a valid internal Wikipedia article link,
// returning the cleaned (unescaped, fragment-stripped) article href.
func isInternalWikiLink(attrs []html.Attribute) (string, bool) {
	href := attrVal(attrs, "href")
	relVal := attrVal(attrs, "rel")
	classVal := attrVal(attrs, "class")
	if href == "" {
		return "", false
	}
	// Exclude external links, interwiki links, and redlinks
	if strings.Contains(relVal, "mw:ExtLink") || strings.Contains(relVal, "mw:WikiLink/Interwiki") {
		return "", false
	}
	if hasClassToken(classVal, "new", "extiw", "external") {
		return "", false
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") ||
		strings.HasPrefix(href, "//") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
		return "", false
	}

	cleanHref := filepath.Base(href)
	if idx := strings.IndexAny(cleanHref, "?#"); idx != -1 {
		cleanHref = cleanHref[:idx]
	}
	for pass := 0; pass < 2 && strings.IndexByte(cleanHref, '%') != -1; pass++ {
		if decoded, err := url.PathUnescape(cleanHref); err == nil && decoded != "" && decoded != cleanHref {
			cleanHref = decoded
		} else {
			break
		}
	}
	if cleanHref == "" || cleanHref == "." || cleanHref == "/" {
		return "", false
	}
	// Exclude non-article namespaces
	if colonIdx := strings.IndexByte(cleanHref, ':'); colonIdx != -1 {
		ns := strings.ToLower(cleanHref[:colonIdx])
		switch ns {
		case "category", "file", "image", "wikipedia", "project", "special", "help", "template", "talk", "portal", "user", "module", "mediawiki", "timedtext", "draft", "mos":
			return "", false
		}
	}
	return cleanHref, true
}

// shouldSkipImage returns true if an <img> is a tiny decorative/UI icon rather than article/math content.
func shouldSkipImage(attrs []html.Attribute, inNoViewer bool) bool {
	var classVal string
	width, height := -1, -1
	for _, a := range attrs {
		switch a.Key {
		case "class":
			classVal = a.Val
		case "width":
			if v, err := strconv.Atoi(strings.TrimSuffix(a.Val, "px")); err == nil {
				width = v
			}
		case "height":
			if v, err := strconv.Atoi(strings.TrimSuffix(a.Val, "px")); err == nil {
				height = v
			}
		}
	}
	// Never skip math formula images
	if strings.Contains(classVal, "mwe-math") {
		return false
	}
	if inNoViewer || hasClassToken(classVal, "noviewer") {
		return true
	}
	if width > 0 && height > 0 && width <= 24 && height <= 24 {
		return true
	}
	return false
}

// detectTableCellSymbol inspects a <td> or <th> element's class and style attributes
// to detect Wikipedia color-coded boolean cells (green/yes -> ✓, red/no -> ✗).
func detectTableCellSymbol(attrs []html.Attribute) string {
	classVal := strings.ToLower(attrVal(attrs, "class"))
	styleVal := strings.ToLower(attrVal(attrs, "style"))

	if hasClassToken(classVal, "table-yes", "table-yes2", "yes", "table-included", "included", "table-success") {
		return "✓"
	}
	if hasClassToken(classVal, "table-no", "table-no2", "no", "table-excluded", "excluded", "table-failure") {
		return "✗"
	}

	if strings.Contains(styleVal, "background") {
		greenPatterns := []string{
			"#9eff9e", "#90ff90", "#9ff", "#8f8", "#dfd", "#bfb", "#cfc",
			"#d4edda", "#c5f7c5", "#b2ffb2", "#aaffaa", "#88ff88", "#99ff99",
			"#ccffcc", "#d1f2eb", "lightgreen", "palegreen",
		}
		for _, gp := range greenPatterns {
			if strings.Contains(styleVal, gp) {
				return "✓"
			}
		}
		redPatterns := []string{
			"#ffc7c7", "#ff9090", "#fdd", "#fbb", "#fcc", "#faa",
			"#f8d7da", "#ffe3e3", "#ffbbbb", "#ffcccc", "#ffaaaa",
			"#ff8888", "#ff9999", "#f2d7d5", "lightpink", "mistyrose",
		}
		for _, rp := range redPatterns {
			if strings.Contains(styleVal, rp) {
				return "✗"
			}
		}
	}
	return ""
}

// detectIconImageSymbol checks if a small skipped icon <img> inside a table cell
// represents a tick/check (✓) or cross/X (✗).
func detectIconImageSymbol(attrs []html.Attribute) string {
	src := strings.ToLower(attrVal(attrs, "src"))
	alt := strings.ToLower(attrVal(attrs, "alt"))
	combined := src + " " + alt
	for _, kw := range []string{"green_check", "yes_check", "check_mark", "checkmark", "tick", "symbol_support", "symbol_confirmed", "voting_keep"} {
		if strings.Contains(combined, kw) {
			return "✓"
		}
	}
	for _, kw := range []string{"red_x", "x_mark", "cross", "no_check", "symbol_oppose", "voting_delete"} {
		if strings.Contains(combined, kw) {
			return "✗"
		}
	}
	return ""
}

// unescapeImageFilename extracts and double-unescapes the base image filename from an <img src="..."> attribute.
func unescapeImageFilename(src string) string {
	filename := filepath.Base(src)
	if idx := strings.IndexAny(filename, "?#"); idx != -1 {
		filename = filename[:idx]
	}
	for pass := 0; pass < 2 && strings.IndexByte(filename, '%') != -1; pass++ {
		if decoded, err := url.PathUnescape(filename); err == nil && decoded != "" && decoded != filename {
			filename = decoded
		} else {
			break
		}
	}
	return strings.ReplaceAll(filename, "_", " ")
}

var tableCellMarkerRe = regexp.MustCompile(`\[(H|C\d+|R\d+)\]`)

// flattenTableRow converts a single-row raw table payload body into inline article text.
func flattenTableRow(body string) string {
	body = tableCellMarkerRe.ReplaceAllString(body, "")
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			switch body[i+1] {
			case '|':
				out.WriteByte('|')
				i++
				continue
			case 'n':
				out.WriteByte(' ')
				i++
				continue
			}
		}
		if c == '|' || c == '\n' {
			out.WriteByte(' ')
			continue
		}
		out.WriteByte(c)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// extractHTML extracts article text plus appended table/infobox/clade payloads.
// It first looks for the article body wrapper (legacy mw-content-text or Parsoid mw-parser-output)
// and only re-tokenizes without a wrapper if none was found.
func extractHTML(htmlContent []byte, processImages ...bool) string {
	hasImages := true
	if len(processImages) > 0 {
		hasImages = processImages[0]
	}
	if res := extractHTMLCore(htmlContent, hasImages, true); res != "" {
		return res
	}
	return extractHTMLCore(htmlContent, hasImages, false)
}

// extractHTMLFallback extracts from HTML snippets that have no article body wrapper.
func extractHTMLFallback(htmlContent []byte, processImages ...bool) string {
	hasImages := true
	if len(processImages) > 0 {
		hasImages = processImages[0]
	}
	return extractHTMLCore(htmlContent, hasImages, false)
}

func extractHTMLCore(htmlContent []byte, hasImages bool, requireWrapper bool) string {
	z := html.NewTokenizer(bytes.NewReader(htmlContent))
	var buf bytes.Buffer

	insideContent := !requireWrapper
	contentDivDepth := 0

	// Ignore frame: the outermost ignored container is tracked by tag name + same-name nesting,
	// so unclosed/void tags elsewhere can never leak the ignore state past its closing tag.
	ignoreTag := ""
	ignoreNest := 0
	// noviewer frame (decorative icon containers)
	noViewerTag := ""
	noViewerNest := 0

	type rtlDirFrame struct {
		tag  string
		mode byte // 'r' = RTL, 'l' = LTR, 'a' = auto/FSI, 0 = neutral same-tag nesting
	}
	var dirStack []rtlDirFrame
	activeDirMode := func() byte {
		for i := len(dirStack) - 1; i >= 0; i-- {
			if dirStack[i].mode != 0 {
				return dirStack[i].mode
			}
		}
		return 0
	}
	hasTagOnDirStack := func(tag string) bool {
		for i := len(dirStack) - 1; i >= 0; i-- {
			if dirStack[i].tag == tag {
				return true
			}
		}
		return false
	}
	pushDirTag := func(tag string, attrs []html.Attribute) {
		m := classifyParsoidTagDir(tag, attrs)
		if m != 0 || hasTagOnDirStack(tag) {
			dirStack = append(dirStack, rtlDirFrame{tag: tag, mode: m})
		}
	}
	popDirTag := func(tag string) {
		switch tag {
		case "td", "th", "tr", "caption", "table":
			for i := len(dirStack) - 1; i >= 0; i-- {
				if dirStack[i].tag == tag {
					dirStack = dirStack[:i]
					return
				}
				if tag != "table" && (dirStack[i].tag == "table" || dirStack[i].tag == "tr" || dirStack[i].tag == "td" || dirStack[i].tag == "th") {
					break
				}
			}
		default:
			if n := len(dirStack); n > 0 && dirStack[n-1].tag == tag {
				dirStack = dirStack[:n-1]
			}
		}
	}
	wrapDirIsolate := func(s string) string {
		if s == "" {
			return s
		}
		switch activeDirMode() {
		case 'r':
			return "\u2067" + s + "\u2069"
		case 'a':
			return "\u2068" + s + "\u2069"
		}
		return s
	}

	var ulIsGallery []bool
	var linkStack []bool

	inTable := false
	tableDepth := 0
	tableIgnoreDepth := 0
	isInfobox := false
	isSidebar := false
	tableUnclassed := false
	tableRowCount := 0
	tableMarkerPos := 0
	var tableDivStack []byte // 's' sidebar-structural div, 'n' normal div
	var tablesData []string
	var infoboxData bytes.Buffer
	hasInfobox := false
	var currentTable bytes.Buffer
	inCell := false
	firstCol := true
	cellPendingSpace := false
	cellContentStartPos := 0
	cellFallbackSymbol := ""
	var tableSpanStack []bool // true if <span class="sr-only"> was ignored
	tableCount := 0
	unwrapDepth := 0

	cladeCount := 0
	inClade := false
	cladeDepth := 0
	cladeTag := ""
	var cladeBuf bytes.Buffer

	ensureRowBreak := func() {
		b := currentTable.Bytes()
		if len(b) > 0 && b[len(b)-1] != '\n' {
			currentTable.WriteByte('\n')
		}
		cellPendingSpace = false
	}

	canEmitCellSpace := func() bool {
		b := currentTable.Bytes()
		if len(b) == 0 {
			return false
		}
		last := b[len(b)-1]
		if last == '\n' || last == '|' || last == ']' || last == ' ' || last == '(' {
			return false
		}
		if len(b) >= 2 && b[len(b)-2] == '\\' && b[len(b)-1] == 'n' {
			return false
		}
		return true
	}

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.CommentToken || tt == html.DoctypeToken {
			continue
		}

		if inClade {
			cladeBuf.Write(z.Raw())
			if tt == html.StartTagToken || tt == html.EndTagToken {
				tn, _ := z.TagName()
				name := string(tn)
				if name == "div" {
					if tt == html.StartTagToken {
						contentDivDepth++
					} else {
						contentDivDepth--
					}
				}
				if name == cladeTag {
					if tt == html.StartTagToken {
						cladeDepth++
					} else {
						cladeDepth--
						if cladeDepth == 0 {
							inClade = false
							cladeCount++
							buf.WriteString(fmt.Sprintf("\n[CLADE:%d]\n", cladeCount))
							if cladePayload := processClade(cladeBuf.Bytes(), cladeCount); cladePayload != "" {
								tablesData = append(tablesData, processRTLInTablePayload(cladePayload))
							}
						}
					}
				}
			}
			continue
		}

		if tt == html.TextToken {
			if !insideContent || ignoreTag != "" {
				continue
			}
			if inTable {
				if inCell && tableIgnoreDepth == 0 {
					raw := decodeTextPreserveBidi(string(z.Text()))
					hasLeadingSpace := len(raw) > 0 && len(strings.TrimLeft(raw, " \t\r\n\u00a0")) < len(raw)
					hasTrailingSpace := len(raw) > 0 && len(strings.TrimRight(raw, " \t\r\n\u00a0")) < len(raw)
					text := strings.TrimSpace(raw)
					if text != "" {
						if (cellPendingSpace || hasLeadingSpace) && canEmitCellSpace() {
							currentTable.WriteByte(' ')
						}
						text = strings.ReplaceAll(text, "|", "\\|")
						text = strings.ReplaceAll(text, "\n", " ")
						text = wrapDirIsolate(text)
						currentTable.WriteString(stdhtml.EscapeString(text))
						cellPendingSpace = hasTrailingSpace
					} else if hasLeadingSpace && canEmitCellSpace() {
						cellPendingSpace = true
					}
				}
			} else {
				text := decodeTextPreserveBidi(string(z.Text()))
				if strings.TrimSpace(text) != "" {
					text = wrapDirIsolate(text)
				}
				buf.WriteString(stdhtml.EscapeString(text))
			}
			continue
		}

		t := z.Token()
		isStart := tt == html.StartTagToken
		isEnd := tt == html.EndTagToken
		isOpen := isStart && !isVoidElement(t.Data) // start tag that will have a matching end tag

		// --- Article body wrapper tracking ---
		if !insideContent {
			if isStart && t.Data == "div" && isContentWrapper(t.Attr) {
				insideContent = true
				contentDivDepth = 1
			}
			continue
		}
		if requireWrapper && t.Data == "div" {
			if isStart {
				contentDivDepth++
			} else if isEnd {
				contentDivDepth--
				if contentDivDepth == 0 {
					insideContent = false
					continue
				}
			}
		}

		// --- Active ignore frame: skip everything until the ignored container closes ---
		if ignoreTag != "" {
			if t.Data == ignoreTag {
				if isOpen {
					ignoreNest++
				} else if isEnd {
					ignoreNest--
					if ignoreNest == 0 {
						ignoreTag = ""
					}
				}
			}
			continue
		}

		// --- noviewer frame tracking (decorative icon containers) ---
		if noViewerTag != "" {
			if t.Data == noViewerTag {
				if isOpen {
					noViewerNest++
				} else if isEnd {
					noViewerNest--
					if noViewerNest == 0 {
						noViewerTag = ""
					}
				}
			}
		} else if isOpen && hasClassToken(attrVal(t.Attr, "class"), "noviewer") {
			noViewerTag = t.Data
			noViewerNest = 1
		}

		// --- Start a new ignore frame (body only; tables manage their own ignores) ---
		if !inTable && isOpen && t.Data != "table" {
			isIgnoredContainer := false
			switch t.Data {
			case "script", "style", "nav", "footer", "sup", "annotation", "head", "title", "noscript":
				isIgnoredContainer = true
			case "math":
				isIgnoredContainer = hasImages
			}
			if !isIgnoredContainer && hasImages && t.Data == "span" && strings.Contains(attrVal(t.Attr, "class"), "mwe-math-mathml") {
				isIgnoredContainer = true
			}
			if !isIgnoredContainer && isIgnoredBoxOrNav(t.Data, t.Attr) {
				isIgnoredContainer = true
			}
			if isIgnoredContainer {
				ignoreTag = t.Data
				ignoreNest = 1
				continue
			}
		}

		// --- Clade detection ---
		if !inTable && isStart && (t.Data == "div" || t.Data == "table") && strings.Contains(attrVal(t.Attr, "class"), "clade") {
			inClade = true
			cladeTag = t.Data
			cladeDepth = 1
			cladeBuf.Reset()
			cladeBuf.Write(z.Raw())
			continue
		}

		// --- Tables ---
		if t.Data == "table" {
			if inTable {
				if isOpen {
					pushDirTag(t.Data, t.Attr)
				} else if isEnd {
					popDirTag(t.Data)
				}
				if isStart {
					tableDepth++
				} else if isEnd {
					tableDepth--
					if tableDepth == 0 {
						inTable = false
						inCell = false
						tableIgnoreDepth = 0
						tableDivStack = tableDivStack[:0]
						if isInfobox {
							infoboxData.Write(currentTable.Bytes())
							if b := infoboxData.Bytes(); len(b) > 0 && b[len(b)-1] != '\n' {
								infoboxData.WriteByte('\n')
							}
						} else if tableUnclassed && tableRowCount <= 1 {
							// Single-row unclassed tables (colour legends etc.) flow inline instead of [TABLE:n]
							body := currentTable.String()
							if nl := strings.IndexByte(body, '\n'); nl != -1 {
								body = body[nl+1:]
							}
							buf.Truncate(tableMarkerPos)
							tableCount--
							if flat := flattenTableRow(body); flat != "" {
								buf.WriteString("<p>" + flat + "</p>")
							}
						} else {
							tablesData = append(tablesData, normalizeTablePayload(processRTLInTablePayload(currentTable.String())))
						}
					}
				}
				continue
			}
			if isStart {
				kind, sidebar := classifyTable(t.Attr)
				switch kind {
				case "ignore":
					ignoreTag = "table"
					ignoreNest = 1
				case "unwrap":
					pushDirTag(t.Data, t.Attr)
					unwrapDepth++
				case "infobox":
					pushDirTag(t.Data, t.Attr)
					isInfobox = true
					isSidebar = sidebar
					inTable = true
					tableDepth = 1
					inCell = false
					firstCol = true
					currentTable.Reset()
					if !hasInfobox {
						hasInfobox = true
						buf.WriteString("\n[INFOBOX]\n")
						currentTable.WriteString("===INFOBOX===\n")
					}
				default:
					pushDirTag(t.Data, t.Attr)
					isInfobox = false
					isSidebar = false
					inTable = true
					tableDepth = 1
					inCell = false
					firstCol = true
					tableUnclassed = attrVal(t.Attr, "class") == ""
					tableRowCount = 0
					tableCount++
					tableMarkerPos = buf.Len()
					buf.WriteString(fmt.Sprintf("\n[TABLE:%d]\n", tableCount))
					currentTable.Reset()
					currentTable.WriteString(fmt.Sprintf("===TABLE:%d===\n", tableCount))
				}
			} else if isEnd {
				popDirTag(t.Data)
				if unwrapDepth > 0 {
					unwrapDepth--
					buf.WriteString("<br>")
				}
			}
			continue
		}

		// --- Unified Parsoid RTL/Bidi directional context tracking ---
		if isOpen {
			pushDirTag(t.Data, t.Attr)
		} else if isEnd {
			popDirTag(t.Data)
		}

		if inTable {
			if isStart || tt == html.SelfClosingTagToken {
				if t.Data == "style" || t.Data == "script" {
					if isStart {
						tableIgnoreDepth++
					}
					continue
				}
				if t.Data == "span" && isStart {
					ignoredSr := false
					if tableIgnoreDepth == 0 && inCell && hasClassToken(attrVal(t.Attr, "class"), "sr-only") &&
						cellContentStartPos <= currentTable.Len() &&
						strings.TrimSpace(currentTable.String()[cellContentStartPos:]) != "" {
						ignoredSr = true
						tableIgnoreDepth++
					}
					tableSpanStack = append(tableSpanStack, ignoredSr)
				}
				if tableIgnoreDepth > 0 {
					continue
				}
				switch t.Data {
				case "tr":
					firstCol = true
					cellPendingSpace = false
				case "th", "td":
					if !firstCol {
						currentTable.WriteString("|")
					}
					firstCol = false
					inCell = true
					cellPendingSpace = false
					if t.Data == "th" {
						currentTable.WriteString("[H]")
					}
					for _, attr := range t.Attr {
						if attr.Key == "colspan" {
							if val, err := strconv.Atoi(attr.Val); err == nil && val > 1 {
								currentTable.WriteString(fmt.Sprintf("[C%d]", val))
							}
						}
						if attr.Key == "rowspan" {
							if val, err := strconv.Atoi(attr.Val); err == nil && val > 1 {
								currentTable.WriteString(fmt.Sprintf("[R%d]", val))
							}
						}
					}
					cellContentStartPos = currentTable.Len()
					cellFallbackSymbol = detectTableCellSymbol(t.Attr)
				case "caption":
					if isInfobox {
						ensureRowBreak()
						currentTable.WriteString("[H]")
						inCell = true
						firstCol = false
						cellPendingSpace = false
					}
				case "div":
					kind := byte('n')
					if isSidebar {
						classVal := attrVal(t.Attr, "class")
						switch {
						case hasClassToken(classVal, "sidebar-list"):
							kind = 's'
							ensureRowBreak()
							firstCol = true
							inCell = false
							cellPendingSpace = false
						case hasClassToken(classVal, "sidebar-list-title"):
							kind = 's'
							ensureRowBreak()
							currentTable.WriteString("[H]")
							inCell = true
							firstCol = false
							cellPendingSpace = false
						case hasClassToken(classVal, "sidebar-list-content"):
							kind = 's'
							currentTable.WriteString("|")
							inCell = true
							firstCol = false
							cellPendingSpace = false
						}
					}
					if isStart {
						tableDivStack = append(tableDivStack, kind)
					}
					if kind == 'n' && inCell {
						currentTable.WriteString("\\n")
						cellPendingSpace = false
					}
				case "br", "p", "li":
					if inCell {
						currentTable.WriteString("\\n")
						cellPendingSpace = false
					}
				case "img":
					if inCell {
						if !shouldSkipImage(t.Attr, noViewerTag != "") {
							if src := attrVal(t.Attr, "src"); src != "" {
								currentTable.WriteString("[IMG:" + hashImageKey(unescapeImageFilename(src)) + "]")
								cellPendingSpace = false
							}
						} else if sym := detectIconImageSymbol(t.Attr); sym != "" {
							cellFallbackSymbol = sym
						}
					}
				case "a":
					if isStart {
						cleanHref, ok := isInternalWikiLink(t.Attr)
						linkStack = append(linkStack, ok)
						if ok && inCell {
							if cellPendingSpace && canEmitCellSpace() {
								currentTable.WriteByte(' ')
								cellPendingSpace = false
							}
							currentTable.WriteString(`<a href="` + cleanHref + `">`)
						}
					}
				}
			} else if isEnd {
				if t.Data == "style" || t.Data == "script" {
					if tableIgnoreDepth > 0 {
						tableIgnoreDepth--
					}
					continue
				}
				if t.Data == "span" {
					if n := len(tableSpanStack); n > 0 {
						wasIgnored := tableSpanStack[n-1]
						tableSpanStack = tableSpanStack[:n-1]
						if wasIgnored && tableIgnoreDepth > 0 {
							tableIgnoreDepth--
							continue
						}
					}
				}
				if tableIgnoreDepth > 0 {
					continue
				}
				switch t.Data {
				case "tr":
					currentTable.WriteString("\n")
					cellPendingSpace = false
					if tableDepth == 1 {
						tableRowCount++
					}
				case "th", "td":
					if inCell && cellFallbackSymbol != "" && cellContentStartPos <= currentTable.Len() {
						if strings.TrimSpace(currentTable.String()[cellContentStartPos:]) == "" {
							currentTable.WriteString(cellFallbackSymbol)
						}
					}
					inCell = false
					cellPendingSpace = false
					cellFallbackSymbol = ""
				case "caption":
					if isInfobox {
						currentTable.WriteString("\n")
						inCell = false
						firstCol = true
						cellPendingSpace = false
					}
				case "div":
					kind := byte('n')
					if n := len(tableDivStack); n > 0 {
						kind = tableDivStack[n-1]
						tableDivStack = tableDivStack[:n-1]
					}
					if kind == 'n' && inCell {
						currentTable.WriteString("\\n")
						cellPendingSpace = false
					}
				case "p", "li":
					if inCell {
						currentTable.WriteString("\\n")
						cellPendingSpace = false
					}
				case "a":
					wasValid := false
					if n := len(linkStack); n > 0 {
						wasValid = linkStack[n-1]
						linkStack = linkStack[:n-1]
					}
					if wasValid && inCell {
						currentTable.WriteString("</a>")
					}
				}
			}
			continue
		}

		// --- Body images ---
		if t.Data == "img" {
			if isStart || tt == html.SelfClosingTagToken {
				if !shouldSkipImage(t.Attr, noViewerTag != "") {
					if src := attrVal(t.Attr, "src"); src != "" {
						buf.WriteString("\n[IMG:" + hashImageKey(unescapeImageFilename(src)) + "]\n")
					}
				}
			}
			continue
		}

		// --- Unwrapped layout table cells flow as line breaks ---
		if unwrapDepth > 0 && isEnd && (t.Data == "td" || t.Data == "th" || t.Data == "tr") {
			buf.WriteString("<br>")
		}

		// --- Emit simplified body markup ---
		if !isStart && !isEnd && tt != html.SelfClosingTagToken {
			continue
		}
		switch t.Data {
		case "p", "b", "i", "u", "h1", "h2", "h3", "h4", "h5", "h6", "br", "ol":
			if isEnd {
				buf.WriteString("</" + t.Data + ">")
			} else {
				buf.WriteString("<" + t.Data + ">")
			}
		case "ul":
			if !isEnd {
				isGal := strings.Contains(attrVal(t.Attr, "class"), "gallery")
				if isStart {
					ulIsGallery = append(ulIsGallery, isGal)
				}
				if isGal {
					buf.WriteString("<p>")
				} else {
					buf.WriteString("<ul>")
				}
			} else {
				isGal := false
				if n := len(ulIsGallery); n > 0 {
					isGal = ulIsGallery[n-1]
					ulIsGallery = ulIsGallery[:n-1]
				}
				if isGal {
					buf.WriteString("</p>")
				} else {
					buf.WriteString("</ul>")
				}
			}
		case "li":
			inGal := len(ulIsGallery) > 0 && ulIsGallery[len(ulIsGallery)-1]
			if !inGal && !isEnd && strings.Contains(attrVal(t.Attr, "class"), "gallerybox") {
				inGal = true
			}
			if inGal {
				if isEnd {
					buf.WriteString("</p>")
				} else {
					buf.WriteString("<p>")
				}
			} else {
				if isEnd {
					buf.WriteString("</li>")
				} else {
					buf.WriteString("<li>")
				}
			}
		case "a":
			if isStart {
				cleanHref, ok := isInternalWikiLink(t.Attr)
				linkStack = append(linkStack, ok)
				if ok {
					buf.WriteString(`<a href="` + cleanHref + `">`)
				}
			} else if isEnd {
				wasValid := false
				if n := len(linkStack); n > 0 {
					wasValid = linkStack[n-1]
					linkStack = linkStack[:n-1]
				}
				if wasValid {
					buf.WriteString("</a>")
				}
			}
		}
	}

	if buf.Len() == 0 {
		return ""
	}

	bodyProcessed := processRTLInBodyMarkup(buf.String())
	buf.Reset()
	buf.WriteString(bodyProcessed)

	// Append the merged infobox/sidebar payload, then table/clade payloads
	if infoboxData.Len() > 0 {
		buf.WriteString("\n" + processRTLInTablePayload(infoboxData.String()) + "===END===\n")
	}
	for _, td := range tablesData {
		buf.WriteString("\n" + td + "===END===\n")
	}

	resStr := strings.TrimSpace(buf.String())
	for strings.HasPrefix(resStr, "<p>") {
		rest := strings.TrimSpace(resStr[3:])
		if strings.HasPrefix(rest, "</p>") {
			resStr = strings.TrimSpace(rest[4:])
		} else {
			break
		}
	}
	return resStr
}

func formatArticleURL(u string) string {
	if strings.IndexByte(u, '%') != -1 || strings.IndexByte(u, '+') != -1 {
		if decoded, err := url.PathUnescape(u); err == nil {
			u = decoded
		}
	}
	if strings.IndexByte(u, '_') != -1 {
		u = strings.ReplaceAll(u, "_", " ")
	}
	return u
}

func ProcessZIM(ctx context.Context, zimPath string, rawDrive io.WriteSeeker, unpartitionedOffset int64, processImages bool, imageBitDepth int, ditherAlgo string, imageFormat string) (indexSector uint32, chunkSector uint32, fontSector uint32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in ProcessZIM: %v", r)
			os.WriteFile("crash.log", []byte(err.Error()), 0666)
			emitEvent(ctx, "error", err.Error())
		}
	}()
	emitEvent(ctx, "log", fmt.Sprintf("Opening ZIM file: %s", zimPath))
	reader, err := zim.NewReader(zimPath, false)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to open zim: %v", err)
	}
	defer reader.Close()

	tree := NewBTree()

	totalEntries := reader.ArticleCount
	count := 0

	// 1. Start writing chunks directly at the unpartitioned offset (no reserved index space).
	chunksStartOffset := unpartitionedOffset

	emitEvent(ctx, "log", fmt.Sprintf("Streaming chunks directly to absolute physical offset %d...", chunksStartOffset))

	// Seek physical drive to where chunks should begin
	_, err = rawDrive.Seek(chunksStartOffset, io.SeekStart)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to seek physical drive: %v", err)
	}

	emitEvent(ctx, "log", fmt.Sprintf("Total entries in ZIM: %d", totalEntries))

	// --- PHASE 1: Directory Scan ---
	emitEvent(ctx, "log", "Phase 1: Scanning directory to map clusters...")
	type Redirect struct {
		Title       string
		URL         string
		TargetIndex uint32
	}

	numWorkers := goRuntime.NumCPU()
	if numWorkers < 4 {
		numWorkers = 4
	}

	type phase1Part struct {
		clusters  map[uint32][]uint32
		redirects []Redirect
		scanned   int
	}
	parts := make([]phase1Part, numWorkers)

	var metaWg sync.WaitGroup
	chunkSize := (totalEntries + uint32(numWorkers) - 1) / uint32(numWorkers)

	for w := 0; w < numWorkers; w++ {
		wIdx := w
		startIdx := uint32(w) * chunkSize
		endIdx := startIdx + chunkSize
		if endIdx > totalEntries {
			endIdx = totalEntries
		}
		if startIdx >= endIdx {
			continue
		}

		metaWg.Add(1)
		go func(w int, start, end uint32) {
			defer metaWg.Done()
			dirReader := reader.NewDirReader()
			localClusters := make(map[uint32][]uint32, 1024)
			var localRedirects []Redirect
			var art zim.Article
			localScanned := 0

			for idx := start; idx < end; idx++ {
				hasTitleOrURL, err := dirReader.ReadPhase1Entry(idx, &art)
				if err != nil {
					continue
				}

				if art.EntryType == zim.RedirectEntry || art.EntryType == zim.LinkTargetEntry {
					target, err := art.RedirectIndex()
					if err == nil && hasTitleOrURL {
						u := formatArticleURL(art.URL())
						title := art.Title
						if title == "" {
							title = u
						}
						if title != "" {
							localRedirects = append(localRedirects, Redirect{
								Title:       title,
								URL:         u,
								TargetIndex: target,
							})
						}
					}
					localScanned++
					continue
				}
				if art.EntryType == zim.DeletedEntry || !hasTitleOrURL {
					continue
				}
				mime := art.MimeType()
				if mime != "text/html" {
					if !processImages || !strings.HasPrefix(mime, "image/") {
						continue
					}
				}
				localClusters[art.Cluster()] = append(localClusters[art.Cluster()], idx)
				localScanned++
			}
			parts[w] = phase1Part{
				clusters:  localClusters,
				redirects: localRedirects,
				scanned:   localScanned,
			}
		}(wIdx, startIdx, endIdx)
	}
	metaWg.Wait()

	clusterMap := make(map[uint32][]uint32, 4096)
	totalRedirects := 0
	countMeta := 0
	for w := 0; w < numWorkers; w++ {
		totalRedirects += len(parts[w].redirects)
		countMeta += parts[w].scanned
	}
	redirects := make([]Redirect, 0, totalRedirects)
	for w := 0; w < numWorkers; w++ {
		redirects = append(redirects, parts[w].redirects...)
		parts[w].redirects = nil
		for c, indices := range parts[w].clusters {
			clusterMap[c] = append(clusterMap[c], indices...)
		}
		parts[w].clusters = nil
	}
	emitEvent(ctx, "log", fmt.Sprintf("Found %d data clusters and %d redirects.", len(clusterMap), len(redirects)))

	// --- PHASE 2: Data Extraction by Cluster ---
	emitEvent(ctx, "log", "Phase 2: Extracting data cluster by cluster...")

	type ClusterJob struct {
		Cluster uint32
		Indices []uint32
	}
	type Result struct {
		Index   uint32
		Title   string
		URL     string
		Data    []byte
		IsImage bool
	}

	jobChan := make(chan ClusterJob, numWorkers*4)
	resultChan := make(chan Result, 1024)

	workerStats := make([]string, numWorkers)
	var statsMutex sync.Mutex
	for i := 0; i < numWorkers; i++ {
		workerStats[i] = "Idle"
	}

	doneChan := make(chan bool)
	defer close(doneChan)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-doneChan:
				return
			case <-ticker.C:
				statsMutex.Lock()
				statsCopy := make([]string, numWorkers)
				copy(statsCopy, workerStats)
				statsMutex.Unlock()
				emitEvent(ctx, "worker_stats", statsCopy)
			}
		}
	}()

	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		workerID := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			dirReader := reader.NewDirReader()
			clusterReader, err := reader.NewClusterReader()
			if err != nil {
				return
			}
			defer clusterReader.Close()
			var art zim.Article

			for job := range jobChan {
				statsMutex.Lock()
				workerStats[workerID] = fmt.Sprintf("Cluster %d (%d arts)", job.Cluster, len(job.Indices))
				statsMutex.Unlock()

				if err := clusterReader.LoadCluster(job.Cluster); err != nil {
					statsMutex.Lock()
					workerStats[workerID] = "Idle"
					statsMutex.Unlock()
					continue
				}

				for _, idx := range job.Indices {
					if err := dirReader.FillArticleAtURLIdx(idx, &art); err != nil {
						continue
					}
					b, err := clusterReader.BlobData(art.Blob())
					if err != nil {
						continue
					}
					mime := art.MimeType()
					isImage := strings.HasPrefix(mime, "image/")

					var dataBytes []byte
					if isImage {
						dataBytes = processImageBytes(b, imageBitDepth, mime, ditherAlgo, imageFormat)
						if len(dataBytes) == 0 {
							continue
						}
					} else {
						plainText := extractHTML(b, processImages)
						if len(plainText) < 50 {
							continue
						}
						dataBytes = []byte(plainText)
					}

					formattedURL := formatArticleURL(art.URL())
					title := art.Title
					if title == "" {
						title = formattedURL
					}

					resultChan <- Result{
						Index:   idx,
						Title:   title,
						URL:     formattedURL,
						Data:    dataBytes,
						IsImage: isImage,
					}
				}
				statsMutex.Lock()
				workerStats[workerID] = "Idle"
				statsMutex.Unlock()
			}
		}()
	}

	go func() {
		// Sort clusters to ensure sequential disk reading (massive speedup on HDDs and SSDs)
		clusters := make([]uint32, 0, len(clusterMap))
		for cluster := range clusterMap {
			clusters = append(clusters, cluster)
		}
		sort.Slice(clusters, func(i, j int) bool {
			return clusters[i] < clusters[j]
		})

		for _, cluster := range clusters {
			indices := clusterMap[cluster]
			delete(clusterMap, cluster)
			jobChan <- ClusterJob{
				Cluster: cluster,
				Indices: indices,
			}
		}
		close(jobChan)
		wg.Wait()
		close(resultChan)
	}()

	writeBuffer := make([]byte, 0, 16*1024*1024)
	currentAbsoluteByteOffset := uint64(chunksStartOffset)
	startTime := time.Now()

	const unsetSector = ^uint64(0)
	sectorSlice := make([]uint64, totalEntries)
	for i := range sectorSlice {
		sectorSlice[i] = unsetSector
	}

	clusterBuffer := make([]byte, 0, 64*1024)
	type PendingKey struct {
		Index              uint32
		Title              string
		URL                string
		UncompressedOffset uint32
	}
	var pendingKeys []PendingKey

	var compBuf bytes.Buffer
	gw := gzip.NewWriter(&compBuf)

	flushCluster := func() error {
		if len(pendingKeys) == 0 {
			return nil
		}

		compBuf.Reset()
		gw.Reset(&compBuf)
		if _, err := gw.Write(clusterBuffer); err != nil {
			return err
		}
		if err := gw.Close(); err != nil {
			return err
		}
		compressed := compBuf.Bytes()

		padding := 32 - (len(compressed) % 32)
		if padding == 32 {
			padding = 0
		}

		offset32 := currentAbsoluteByteOffset / 32

		for _, pk := range pendingKeys {
			val64 := (uint64(offset32) << 32) | uint64(pk.UncompressedOffset)

			tree.InsertIfAbsent([]byte(pk.Title), val64)
			kLowerOrig := strings.ToLower(pk.Title)
			if kLowerOrig != pk.Title {
				tree.InsertIfAbsent([]byte(kLowerOrig), val64)
			}
			if pk.URL != pk.Title {
				tree.InsertIfAbsent([]byte(pk.URL), val64)
			}
			kLowerURL := strings.ToLower(pk.URL)
			if kLowerURL != pk.URL && kLowerURL != kLowerOrig {
				tree.InsertIfAbsent([]byte(kLowerURL), val64)
			}

			if pk.Index < uint32(len(sectorSlice)) {
				sectorSlice[pk.Index] = val64
			}
		}

		writeBuffer = append(writeBuffer, compressed...)
		for i := 0; i < padding; i++ {
			writeBuffer = append(writeBuffer, 0)
		}

		currentAbsoluteByteOffset += uint64(len(compressed) + padding)

		if len(writeBuffer) >= 16*1024*1024 {
			_, err := rawDrive.Write(writeBuffer)
			if err != nil {
				return err
			}
			writeBuffer = writeBuffer[:0]
		}

		clusterBuffer = clusterBuffer[:0]
		pendingKeys = pendingKeys[:0]
		return nil
	}

	for res := range resultChan {
		kOriginal := res.Title
		kURL := res.URL

		if res.IsImage {
			// Write image directly to disk, aligned to 32 bytes
			padding := 32 - (len(res.Data) % 32)
			if padding == 32 {
				padding = 0
			}

			offset32 := currentAbsoluteByteOffset / 32
			val64 := (uint64(offset32) << 32) | 0xFFFFFFFF // 0xFFFFFFFF means unclustered image

			hashedTitle := hashImageKey(filepath.Base(kOriginal))
			tree.InsertIfAbsent([]byte(hashedTitle), val64)
			hashedURL := hashImageKey(filepath.Base(kURL))
			if hashedURL != hashedTitle {
				tree.InsertIfAbsent([]byte(hashedURL), val64)
			}

			if res.Index < uint32(len(sectorSlice)) {
				sectorSlice[res.Index] = val64
			}

			writeBuffer = append(writeBuffer, res.Data...)
			for i := 0; i < padding; i++ {
				writeBuffer = append(writeBuffer, 0)
			}
			currentAbsoluteByteOffset += uint64(len(res.Data) + padding)

			if len(writeBuffer) >= 16*1024*1024 {
				_, err = rawDrive.Write(writeBuffer)
				if err != nil {
					emitEvent(ctx, "error", fmt.Sprintf("CRITICAL DRIVE WRITE ERROR: %v", err))
					return 0, 0, 0, err
				}
				writeBuffer = writeBuffer[:0]
			}
		} else {
			// Cluster text articles
			uncompressedOffset := len(clusterBuffer)

			var lenHeader [4]byte
			binary.LittleEndian.PutUint32(lenHeader[:], uint32(len(res.Data)))
			clusterBuffer = append(clusterBuffer, lenHeader[:]...)
			clusterBuffer = append(clusterBuffer, res.Data...)

			pendingKeys = append(pendingKeys, PendingKey{
				Index:              res.Index,
				Title:              kOriginal,
				URL:                kURL,
				UncompressedOffset: uint32(uncompressedOffset),
			})

			if len(clusterBuffer) >= 32*1024 {
				if err := flushCluster(); err != nil {
					emitEvent(ctx, "error", fmt.Sprintf("CRITICAL DRIVE WRITE ERROR: %v", err))
					return 0, 0, 0, err
				}
			}
		}
		count++

		if count%5000 == 0 {
			emitEvent(ctx, "log", fmt.Sprintf("Processed %d articles...", count))

			elapsed := time.Since(startTime).Seconds()
			if elapsed > 0 {
				articlesPerSec := float64(count) / elapsed
				totalDataEst := float64(countMeta - len(redirects))
				remaining := totalDataEst - float64(count)
				if remaining < 0 {
					remaining = 0
				}
				etaSec := remaining / articlesPerSec
				etaDur := time.Duration(etaSec) * time.Second

				etaStr := fmt.Sprintf("ETA: %s (%.0f arts/sec)", etaDur.Round(time.Second).String(), articlesPerSec)
				emitEvent(ctx, "eta", etaStr)
				emitEvent(ctx, "progress", float64(count)/totalDataEst)
			}
		}

		if len(writeBuffer) >= 16*1024*1024 {
			_, err = rawDrive.Write(writeBuffer)
			if err != nil {
				emitEvent(ctx, "error", fmt.Sprintf("CRITICAL DRIVE WRITE ERROR: %v", err))
				return 0, 0, 0, err
			}
			writeBuffer = writeBuffer[:0]
		}
	}

	if err := flushCluster(); err != nil {
		emitEvent(ctx, "error", fmt.Sprintf("CRITICAL DRIVE WRITE ERROR: %v", err))
		return 0, 0, 0, err
	}

	if len(writeBuffer) > 0 {
		_, err = rawDrive.Write(writeBuffer)
		if err != nil {
			emitEvent(ctx, "error", fmt.Sprintf("CRITICAL DRIVE WRITE ERROR (Flush): %v", err))
			return 0, 0, 0, err
		}
		writeBuffer = nil
	}

	// --- PHASE 3: Insert Redirects ---
	emitEvent(ctx, "progress", 1.0)
	emitEvent(ctx, "eta", "Finalizing B-Tree index & metadata...")
	emitEvent(ctx, "log", "Phase 3: Processing redirects...")
	redirectsAdded := 0
	for _, red := range redirects {
		if red.TargetIndex >= uint32(len(sectorSlice)) {
			continue
		}
		targetVal64 := sectorSlice[red.TargetIndex]
		if targetVal64 == unsetSector {
			continue
		}

		kOriginal := red.Title
		kURL := red.URL

		if (targetVal64 & 0xFFFFFFFF) == 0xFFFFFFFF {
			hashedTitle := hashImageKey(filepath.Base(kOriginal))
			if tree.InsertIfAbsent([]byte(hashedTitle), targetVal64) {
				redirectsAdded++
			}
			hashedURL := hashImageKey(filepath.Base(kURL))
			if hashedURL != hashedTitle && tree.InsertIfAbsent([]byte(hashedURL), targetVal64) {
				redirectsAdded++
			}
			continue
		}

		if tree.InsertIfAbsent([]byte(kOriginal), targetVal64) {
			redirectsAdded++
		}
		kLowerOrig := strings.ToLower(kOriginal)
		if kLowerOrig != kOriginal && tree.InsertIfAbsent([]byte(kLowerOrig), targetVal64) {
			redirectsAdded++
		}
		if kURL != kOriginal && tree.InsertIfAbsent([]byte(kURL), targetVal64) {
			redirectsAdded++
		}
		kLowerURL := strings.ToLower(kURL)
		if kLowerURL != kURL && kLowerURL != kLowerOrig && tree.InsertIfAbsent([]byte(kLowerURL), targetVal64) {
			redirectsAdded++
		}
	}
	redirects = nil
	sectorSlice = nil

	emitEvent(ctx, "log", fmt.Sprintf("Added %d redirects to index.", redirectsAdded))
	emitEvent(ctx, "log", fmt.Sprintf("Serializing B-Tree for %d articles + %d redirects...", count, redirectsAdded))

	// 512-byte align before writing index
	pad512 := 512 - (currentAbsoluteByteOffset % 512)
	if pad512 == 512 {
		pad512 = 0
	}
	if pad512 > 0 {
		_, err = rawDrive.Write(make([]byte, pad512))
		if err != nil {
			return 0, 0, 0, err
		}
		currentAbsoluteByteOffset += pad512
	}

	indexStartSector := uint32(currentAbsoluteByteOffset / 512)
	numNodes, indexWritten, err := tree.SerializeTo(rawDrive)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to write index to physical drive: %v", err)
	}
	tree = nil
	emitEvent(ctx, "log", fmt.Sprintf("Appended %d B-Tree nodes to the end of chunks at sector %d...", numNodes, indexStartSector))
	currentAbsoluteByteOffset += uint64(indexWritten)

	emitEvent(ctx, "log", fmt.Sprintf("Successfully flashed %d bytes of index.", indexWritten))

	// 512-byte align before writing font
	pad512 = 512 - (currentAbsoluteByteOffset % 512)
	if pad512 == 512 {
		pad512 = 0
	}
	if pad512 > 0 {
		if _, err := rawDrive.Write(make([]byte, pad512)); err != nil {
			return 0, 0, 0, err
		}
		currentAbsoluteByteOffset += pad512
	}

	fontSectorStart := uint32(currentAbsoluteByteOffset / 512)
	fontSector = uint32(0)

	// Look for fallback_noto.bin in current dir or font-production
	if fontData, err := loadFallbackNoto(); err == nil {
		emitEvent(ctx, "log", fmt.Sprintf("Found fallback_noto.bin (%d bytes). Appending to image...", len(fontData)))
		padding := 512 - (len(fontData) % 512)
		if padding == 512 {
			padding = 0
		}

		if _, err := rawDrive.Write(fontData); err != nil {
			return 0, 0, 0, fmt.Errorf("failed to write font: %v", err)
		}
		if padding > 0 {
			if _, err := rawDrive.Write(make([]byte, padding)); err != nil {
				return 0, 0, 0, fmt.Errorf("failed to write font padding: %v", err)
			}
		}
		emitEvent(ctx, "log", fmt.Sprintf("Successfully flashed fallback_noto.bin at sector %d", fontSectorStart))
		fontSector = fontSectorStart
	} else {
		emitEvent(ctx, "log", "fallback_noto.bin not found, skipping font embedding.")
	}

	// Append Metadata Footer (CPR1)
	footer := make([]byte, 512)
	copy(footer[0:4], "CPR1")
	binary.LittleEndian.PutUint32(footer[4:8], indexStartSector)
	binary.LittleEndian.PutUint32(footer[8:12], uint32(chunksStartOffset/512))
	binary.LittleEndian.PutUint32(footer[12:16], fontSector)
	flags := uint32(0)
	if processImages {
		flags |= 1 // Bit 0: 1 = Image-enabled, 0 = No-pic
	}
	binary.LittleEndian.PutUint32(footer[16:20], flags)

	if _, err := rawDrive.Write(footer); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to write metadata footer: %v", err)
	}

	emitEvent(ctx, "log", "Successfully wrote CPR1 metadata footer to the final sector. The .txt config file is no longer required.")
	emitEvent(ctx, "eta", "")
	emitEvent(ctx, "done", true)

	return indexStartSector, uint32(chunksStartOffset / 512), fontSector, nil
}

func loadFallbackNoto() ([]byte, error) {
	candidates := []string{
		"fallback_noto.bin",
		filepath.Join("font-production", "fallback_noto.bin"),
		filepath.Join("..", "font-production", "fallback_noto.bin"),
		filepath.Join("..", "..", "font-production", "fallback_noto.bin"),
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "fallback_noto.bin"),
			filepath.Join(exeDir, "font-production", "fallback_noto.bin"),
			filepath.Join(exeDir, "..", "Resources", "fallback_noto.bin"),
			filepath.Join(exeDir, "..", "font-production", "fallback_noto.bin"),
			filepath.Join(exeDir, "..", "..", "font-production", "fallback_noto.bin"),
			filepath.Join(exeDir, "..", "..", "..", "font-production", "fallback_noto.bin"),
		)
	}

	for _, c := range candidates {
		if data, err := os.ReadFile(c); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	if len(embeddedFallbackNoto) > 0 {
		return embeddedFallbackNoto, nil
	}
	return nil, os.ErrNotExist
}

func AppendFontToImage(ctx context.Context, imgPath string) error {
	fontData, err := loadFallbackNoto()
	if err != nil {
		return fmt.Errorf("failed to read fallback_noto.bin: %v", err)
	}

	imgFile, err := os.OpenFile(imgPath, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open image file: %v", err)
	}
	defer imgFile.Close()

	info, err := imgFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat image file: %v", err)
	}
	size := info.Size()

	// Read the last 512 bytes to see if it's already a CPR1 footer
	footer := make([]byte, 512)
	imgFile.Seek(-512, io.SeekEnd)
	io.ReadFull(imgFile, footer)

	if string(footer[0:4]) != "CPR1" {
		return fmt.Errorf("image does not have a valid CPR1 footer, cannot append font")
	}

	indexSec := binary.LittleEndian.Uint32(footer[4:8])
	chunkSec := binary.LittleEndian.Uint32(footer[8:12])
	existingFontSec := binary.LittleEndian.Uint32(footer[12:16])
	flags := binary.LittleEndian.Uint32(footer[16:20])

	paddingEnd := 512 - (len(fontData) % 512)
	if paddingEnd == 512 {
		paddingEnd = 0
	}
	fontSectors := uint32((len(fontData) + paddingEnd) / 512)
	totalSectors := uint32(size / 512)

	var fontSector uint32
	if existingFontSec > 0 && existingFontSec+fontSectors+1 == totalSectors {
		// Overwrite existing fallback_noto.bin in-place without growing the image
		fontSector = existingFontSec
		if _, err := imgFile.Seek(int64(fontSector)*512, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek to existing font sector: %v", err)
		}
	} else {
		// Overwrite the footer with the font data (effectively deleting the old footer)
		if _, err := imgFile.Seek(-512, io.SeekEnd); err != nil {
			return fmt.Errorf("failed to seek to footer: %v", err)
		}
		fontSector = uint32((size - 512) / 512)
	}

	if _, err := imgFile.Write(fontData); err != nil {
		return fmt.Errorf("failed to write font: %v", err)
	}
	if paddingEnd > 0 {
		if _, err := imgFile.Write(make([]byte, paddingEnd)); err != nil {
			return fmt.Errorf("failed to write font padding: %v", err)
		}
	}

	// Write the new footer preserving flags
	newFooter := make([]byte, 512)
	copy(newFooter[0:4], "CPR1")
	binary.LittleEndian.PutUint32(newFooter[4:8], indexSec)
	binary.LittleEndian.PutUint32(newFooter[8:12], chunkSec)
	binary.LittleEndian.PutUint32(newFooter[12:16], fontSector)
	binary.LittleEndian.PutUint32(newFooter[16:20], flags)

	if _, err := imgFile.Write(newFooter); err != nil {
		return fmt.Errorf("failed to write new metadata footer: %v", err)
	}

	imgFile.Sync()
	emitEvent(ctx, "log", fmt.Sprintf("Successfully appended fallback_noto.bin to %s at sector %d", imgPath, fontSector))
	emitEvent(ctx, "done", true)

	return nil
}

func emitEvent(ctx context.Context, name string, data ...interface{}) {
	defer func() { recover() }() // ignore panics if ctx is invalid
	if ctx != context.Background() {
		runtime.EventsEmit(ctx, name, data...)
	}
	if name == "log" || name == "error" {
		fmt.Println(data...)
	}
}
