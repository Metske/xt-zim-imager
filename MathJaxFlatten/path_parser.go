package mathjaxflatten

import (
	"fmt"
	"strconv"
	"unicode"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

type pathParser struct {
	s   string
	pos int
}

func (p *pathParser) skipWhitespace() {
	for p.pos < len(p.s) && (unicode.IsSpace(rune(p.s[p.pos])) || p.s[p.pos] == ',') {
		p.pos++
	}
}

func (p *pathParser) nextFloat() (float64, error) {
	p.skipWhitespace()
	if p.pos >= len(p.s) {
		return 0, fmt.Errorf("unexpected end of path data")
	}

	start := p.pos
	hasDot := false
	hasDigit := false
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == '-' || c == '+' {
			if p.pos > start {
				// Sign indicates start of next number, so we stop here
				// UNLESS it's the exponent sign, but MathJax usually doesn't use scientific notation in paths.
				// For safety, let's just break if we're past the start.
				break
			}
		} else if c == '.' {
			if hasDot {
				// Second dot means start of next number
				break
			}
			hasDot = true
		} else if c >= '0' && c <= '9' {
			hasDigit = true
		} else {
			break
		}
		p.pos++
	}

	if !hasDigit {
		return 0, fmt.Errorf("invalid number format at position %d: %s", start, p.s[start:p.pos])
	}

	valStr := p.s[start:p.pos]
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, err
	}
	return val, nil
}

func (p *pathParser) nextCommand() (byte, bool) {
	p.skipWhitespace()
	if p.pos >= len(p.s) {
		return 0, false
	}
	c := p.s[p.pos]
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		p.pos++
		return c, true
	}
	return 0, false
}

func toFixed(x, y float64, m rasterx.Matrix2D) (fixed.Int26_6, fixed.Int26_6) {
	nx := m.A*x + m.C*y + m.E
	ny := m.B*x + m.D*y + m.F
	return fixed.Int26_6(nx * 64), fixed.Int26_6(ny * 64)
}

func parsePathData(d string, m rasterx.Matrix2D) (*rasterx.Path, error) {
	parser := &pathParser{s: d, pos: 0}
	path := &rasterx.Path{}

	var cmd byte
	var lastCmd byte
	var err error

	var x, y float64
	var ctrlX, ctrlY float64

	for parser.pos < len(parser.s) {
		newCmd, ok := parser.nextCommand()
		if ok {
			cmd = newCmd
		} else if lastCmd != 0 {
			if lastCmd == 'M' {
				cmd = 'L'
			} else if lastCmd == 'm' {
				cmd = 'l'
			} else {
				cmd = lastCmd
			}
		} else {
			return nil, fmt.Errorf("expected command at %d", parser.pos)
		}

		lastCmd = cmd

		switch cmd {
		case 'M', 'm':
			var nx, ny float64
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 'm' {
				nx += x
				ny += y
			}
			x, y = nx, ny
			ctrlX, ctrlY = x, y
			
			fx, fy := toFixed(x, y, m)
			path.Start(fixed.Point26_6{X: fx, Y: fy})

		case 'L', 'l':
			var nx, ny float64
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 'l' {
				nx += x
				ny += y
			}
			x, y = nx, ny
			ctrlX, ctrlY = x, y
			
			fx, fy := toFixed(x, y, m)
			path.Line(fixed.Point26_6{X: fx, Y: fy})

		case 'H', 'h':
			var nx float64
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 'h' {
				nx += x
			}
			x = nx
			ctrlX, ctrlY = x, y
			
			fx, fy := toFixed(x, y, m)
			path.Line(fixed.Point26_6{X: fx, Y: fy})

		case 'V', 'v':
			var ny float64
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 'v' {
				ny += y
			}
			y = ny
			ctrlX, ctrlY = x, y
			
			fx, fy := toFixed(x, y, m)
			path.Line(fixed.Point26_6{X: fx, Y: fy})

		case 'Q', 'q':
			var cx, cy, nx, ny float64
			cx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			cy, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 'q' {
				cx += x
				cy += y
				nx += x
				ny += y
			}
			
			fcx, fcy := toFixed(cx, cy, m)
			
			x, y = nx, ny
			ctrlX, ctrlY = cx, cy
			
			fx, fy := toFixed(x, y, m)
			path.QuadBezier(fixed.Point26_6{X: fcx, Y: fcy}, fixed.Point26_6{X: fx, Y: fy})

		case 'T', 't':
			var nx, ny float64
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 't' {
				nx += x
				ny += y
			}
			cx := x + (x - ctrlX)
			cy := y + (y - ctrlY)
			
			fcx, fcy := toFixed(cx, cy, m)
			
			x, y = nx, ny
			ctrlX, ctrlY = cx, cy
			
			fx, fy := toFixed(x, y, m)
			path.QuadBezier(fixed.Point26_6{X: fcx, Y: fcy}, fixed.Point26_6{X: fx, Y: fy})

		case 'C', 'c':
			var cx1, cy1, cx2, cy2, nx, ny float64
			cx1, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			cy1, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			cx2, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			cy2, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 'c' {
				cx1 += x
				cy1 += y
				cx2 += x
				cy2 += y
				nx += x
				ny += y
			}
			
			fcx1, fcy1 := toFixed(cx1, cy1, m)
			fcx2, fcy2 := toFixed(cx2, cy2, m)
			
			x, y = nx, ny
			ctrlX, ctrlY = cx2, cy2
			
			fx, fy := toFixed(x, y, m)
			path.CubeBezier(fixed.Point26_6{X: fcx1, Y: fcy1}, fixed.Point26_6{X: fcx2, Y: fcy2}, fixed.Point26_6{X: fx, Y: fy})

		case 'S', 's':
			var cx2, cy2, nx, ny float64
			cx2, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			cy2, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			nx, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			ny, err = parser.nextFloat()
			if err != nil {
				return nil, err
			}
			if cmd == 's' {
				cx2 += x
				cy2 += y
				nx += x
				ny += y
			}
			cx1 := x + (x - ctrlX)
			cy1 := y + (y - ctrlY)
			
			fcx1, fcy1 := toFixed(cx1, cy1, m)
			fcx2, fcy2 := toFixed(cx2, cy2, m)
			
			x, y = nx, ny
			ctrlX, ctrlY = cx2, cy2
			
			fx, fy := toFixed(x, y, m)
			path.CubeBezier(fixed.Point26_6{X: fcx1, Y: fcy1}, fixed.Point26_6{X: fcx2, Y: fcy2}, fixed.Point26_6{X: fx, Y: fy})

		case 'Z', 'z':
			path.Stop(true)
			ctrlX, ctrlY = x, y

		default:
			return nil, fmt.Errorf("unsupported path command: %c", cmd)
		}
	}

	return path, nil
}
