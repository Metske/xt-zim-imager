package mathjaxflatten

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/srwiley/rasterx"
)

type SVG struct {
	ViewBox string  `xml:"viewBox,attr"`
	Width   string  `xml:"width,attr"`
	Height  string  `xml:"height,attr"`
	Defs    Defs    `xml:"defs"`
	Groups  []Group `xml:"g"`
}

type Defs struct {
	Paths []Path `xml:"path"`
}

type Path struct {
	ID          string `xml:"id,attr"`
	D           string `xml:"d,attr"`
	StrokeWidth string `xml:"stroke-width,attr"`
}

type Group struct {
	Transform   string  `xml:"transform,attr"`
	Paths       []Path  `xml:"path"`
	Uses        []Use   `xml:"use"`
	Groups      []Group `xml:"g"`
	StrokeWidth string  `xml:"stroke-width,attr"`
	Fill        string  `xml:"fill,attr"`
	Stroke      string  `xml:"stroke,attr"`
}

type Use struct {
	XMLName   xml.Name
	Attrs     []xml.Attr `xml:",any,attr"`
}

type viewBox struct {
	X, Y, W, H float64
}

func parseViewBox(s string) (viewBox, error) {
	parts := strings.Fields(s)
	if len(parts) != 4 {
		return viewBox{}, fmt.Errorf("invalid viewBox: %s", s)
	}
	x, _ := strconv.ParseFloat(parts[0], 64)
	y, _ := strconv.ParseFloat(parts[1], 64)
	w, _ := strconv.ParseFloat(parts[2], 64)
	h, _ := strconv.ParseFloat(parts[3], 64)
	return viewBox{x, y, w, h}, nil
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseTransform(s string) rasterx.Matrix2D {
	m := rasterx.Identity
	s = strings.TrimSpace(s)
	if s == "" {
		return m
	}

	// Simple parser for standard MathJax transforms: matrix, translate, scale
	for len(s) > 0 {
		idx := strings.Index(s, "(")
		if idx == -1 {
			break
		}
		cmd := strings.TrimSpace(s[:idx])
		s = s[idx+1:]
		idx = strings.Index(s, ")")
		if idx == -1 {
			break
		}
		argsStr := s[:idx]
		s = s[idx+1:]

		// replace commas with spaces
		argsStr = strings.ReplaceAll(argsStr, ",", " ")
		fields := strings.Fields(argsStr)
		var args []float64
		for _, f := range fields {
			args = append(args, parseFloat(f))
		}

		switch cmd {
		case "matrix":
			if len(args) == 6 {
				tm := rasterx.Matrix2D{
					A: args[0], B: args[1], C: args[2],
					D: args[3], E: args[4], F: args[5],
				}
				m = m.Mult(tm)
			}
		case "translate":
			if len(args) >= 1 {
				tx := args[0]
				ty := 0.0
				if len(args) >= 2 {
					ty = args[1]
				}
				tm := rasterx.Matrix2D{
					A: 1, B: 0, C: 0,
					D: 1, E: tx, F: ty,
				}
				m = m.Mult(tm)
			}
		case "scale":
			if len(args) >= 1 {
				sx := args[0]
				sy := sx
				if len(args) >= 2 {
					sy = args[1]
				}
				tm := rasterx.Matrix2D{
					A: sx, B: 0, C: 0,
					D: sy, E: 0, F: 0,
				}
				m = m.Mult(tm)
			}
		}
	}
	return m
}

func parseSVG(r io.Reader) (*SVG, map[string]string, error) {
	decoder := xml.NewDecoder(r)
	var svgRoot SVG
	if err := decoder.Decode(&svgRoot); err != nil {
		return nil, nil, err
	}

	defsMap := make(map[string]string)
	for _, p := range svgRoot.Defs.Paths {
		if p.ID == "" {
			continue
		}
		defsMap[p.ID] = p.D
	}

	return &svgRoot, defsMap, nil
}

func collectDrawOps(g Group, defs map[string]string, parentMatrix rasterx.Matrix2D) []*rasterx.Path {
	var ops []*rasterx.Path
	
	m := parentMatrix.Mult(parseTransform(g.Transform))

	for _, p := range g.Paths {
		if p.D != "" {
			parsedPath, err := parsePathData(p.D, m)
			if err == nil && parsedPath != nil {
				ops = append(ops, parsedPath)
			}
		}
	}

	for _, u := range g.Uses {
		var href, x, y, transform string
		for _, attr := range u.Attrs {
			switch attr.Name.Local {
			case "href":
				href = attr.Value
			case "x":
				x = attr.Value
			case "y":
				y = attr.Value
			case "transform":
				transform = attr.Value
			}
		}
		href = strings.TrimPrefix(href, "#")
		if d, ok := defs[href]; ok {
			useMatrix := m
			
			if transform != "" {
				useMatrix = useMatrix.Mult(parseTransform(transform))
			}

			tx := parseFloat(x)
			ty := parseFloat(y)
			if tx != 0 || ty != 0 {
				tm := rasterx.Matrix2D{
					A: 1, B: 0, C: 0,
					D: 1, E: tx, F: ty,
				}
				useMatrix = useMatrix.Mult(tm)
			}


			parsedPath, err := parsePathData(d, useMatrix)
			if err == nil && parsedPath != nil {
				ops = append(ops, parsedPath)
			}
		}
	}

	for _, child := range g.Groups {
		ops = append(ops, collectDrawOps(child, defs, m)...)
	}

	return ops
}
