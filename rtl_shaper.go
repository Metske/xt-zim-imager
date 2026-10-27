package main

import (
	stdhtml "html"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/unicode/bidi"
)

// Arabic joining types (Unicode ArabicShaping.txt)
const (
	joinU = iota // Non-Joining
	joinL        // Left-Joining
	joinR        // Right-Joining (Isolated, Final)
	joinD        // Dual-Joining (Isolated, Final, Initial, Medial)
	joinC        // Join-Causing (Tatweel U+0640, ZWJ U+200D)
)

// Contextual form offsets in Presentation Forms-A and Forms-B
const (
	shapeIsolated = 0
	shapeFinal    = 1
	shapeInitial  = 2
	shapeMedial   = 3
)

const (
	zwjFlag             = 1
	zwnjFlag            = 2
	ligaturePlaceholder = rune(0xFFFF)
)

// Base Arabic block U+0621..U+064A -> Presentation Forms-B (0xFE00 + formB + offset)
var baseArabicShapes = [0x064A - 0x0621 + 1]struct {
	joinType uint8
	formB    uint8
}{
	/* 0621 ء */ {joinU, 0x80},
	/* 0622 آ */ {joinR, 0x81},
	/* 0623 أ */ {joinR, 0x83},
	/* 0624 ؤ */ {joinR, 0x85},
	/* 0625 إ */ {joinR, 0x87},
	/* 0626 ئ */ {joinD, 0x89},
	/* 0627 ا */ {joinR, 0x8D},
	/* 0628 ب */ {joinD, 0x8F},
	/* 0629 ة */ {joinR, 0x93},
	/* 062A ت */ {joinD, 0x95},
	/* 062B ث */ {joinD, 0x99},
	/* 062C ج */ {joinD, 0x9D},
	/* 062D ح */ {joinD, 0xA1},
	/* 062E خ */ {joinD, 0xA5},
	/* 062F د */ {joinR, 0xA9},
	/* 0630 ذ */ {joinR, 0xAB},
	/* 0631 ر */ {joinR, 0xAD},
	/* 0632 ز */ {joinR, 0xAF},
	/* 0633 س */ {joinD, 0xB1},
	/* 0634 ش */ {joinD, 0xB5},
	/* 0635 ص */ {joinD, 0xB9},
	/* 0636 ض */ {joinD, 0xBD},
	/* 0637 ط */ {joinD, 0xC1},
	/* 0638 ظ */ {joinD, 0xC5},
	/* 0639 ع */ {joinD, 0xC9},
	/* 063A غ */ {joinD, 0xCD},
	/* 063B   */ {joinU, 0x00},
	/* 063C   */ {joinU, 0x00},
	/* 063D   */ {joinU, 0x00},
	/* 063E   */ {joinU, 0x00},
	/* 063F   */ {joinU, 0x00},
	/* 0640 ـ */ {joinC, 0x00},
	/* 0641 ف */ {joinD, 0xD1},
	/* 0642 ق */ {joinD, 0xD5},
	/* 0643 ك */ {joinD, 0xD9},
	/* 0644 ل */ {joinD, 0xDD},
	/* 0645 م */ {joinD, 0xE1},
	/* 0646 ن */ {joinD, 0xE5},
	/* 0647 ه */ {joinD, 0xE9},
	/* 0648 و */ {joinR, 0xED},
	/* 0649 ى */ {joinR, 0xEF},
	/* 064A ي */ {joinD, 0xF1},
}

type extJoinRange struct {
	first, last rune
	joinType    uint8
}

var extArabicJoinRanges = []extJoinRange{
	{0x0620, 0x0620, joinD}, // Kashmiri Yeh
	{0x066E, 0x066F, joinD}, // Dotless Beh/Qaf
	{0x0671, 0x0673, joinR}, // Alef Wasla (ٱ) + wavy-hamza alefs
	{0x0675, 0x0677, joinR}, // High-hamza alef/waw
	{0x0678, 0x0687, joinD}, // Peh (پ), Tteh (ٹ), Tcheh (چ), etc.
	{0x0688, 0x0699, joinR}, // Ddal (ڈ), Rreh (ڑ), Jeh (ژ), etc.
	{0x069A, 0x06BF, joinD}, // Keheh (ک), Gaf (گ), Noon Ghunna (ں), Heh Doachashmee (ھ), etc.
	{0x06C0, 0x06C0, joinR}, // Heh with Yeh above (ۀ)
	{0x06C1, 0x06C2, joinD}, // Heh Goal (ہ)
	{0x06C3, 0x06CB, joinR}, // Teh Marbuta Goal (ۃ), Waw group (ۆ ۇ ۋ)
	{0x06CC, 0x06CC, joinD}, // Farsi Yeh (ی)
	{0x06CD, 0x06CD, joinR}, // Yeh with tail
	{0x06CE, 0x06CE, joinD}, // Kurdish Yeh with V (ێ)
	{0x06CF, 0x06CF, joinR}, // Waw with dot above
	{0x06D0, 0x06D1, joinD}, // Pashto E (ې)
	{0x06D2, 0x06D3, joinR}, // Urdu Yeh Barree (ے ۓ)
	{0x06D5, 0x06D5, joinR}, // Kurdish Ae (ە)
	{0x06EE, 0x06EF, joinR}, // Dal/Reh with inverted V
	{0x06FA, 0x06FC, joinD}, // Sheen/Dad/Ghain with dot below
	{0x06FF, 0x06FF, joinD}, // Heh with inverted V
	{0x200C, 0x200C, joinU}, // ZWNJ
	{0x200D, 0x200D, joinC}, // ZWJ
}

type extShapeEntry struct {
	cp       rune
	isolated rune
	forms    uint8
}

// Presentation Forms-A (U+FB50..U+FBFF) for extended Persian/Urdu/Kurdish/Pashto/Sindhi letters
var extArabicShapeForms = []extShapeEntry{
	{0x0671, 0xFB50, 2}, // ٱ Alef Wasla
	{0x0679, 0xFB66, 4}, // ٹ Urdu Tteh
	{0x067A, 0xFB5E, 4}, // ٺ Tteheh
	{0x067B, 0xFB52, 4}, // ٻ Sindhi Beeh
	{0x067E, 0xFB56, 4}, // پ Persian/Urdu Peh
	{0x067F, 0xFB62, 4}, // ٿ Teheh
	{0x0680, 0xFB5A, 4}, // ڀ Sindhi Beheh
	{0x0683, 0xFB76, 4}, // ڃ Sindhi Nyeh
	{0x0684, 0xFB72, 4}, // ڄ Sindhi Dyeh
	{0x0686, 0xFB7A, 4}, // چ Persian/Urdu Tcheh
	{0x0687, 0xFB7E, 4}, // ڇ Sindhi Tcheheh
	{0x0688, 0xFB88, 2}, // ڈ Urdu Ddal
	{0x068C, 0xFB84, 2}, // ڌ Sindhi Dahal
	{0x068D, 0xFB82, 2}, // ڍ Sindhi Ddahal
	{0x068E, 0xFB86, 2}, // ڎ Sindhi Dul
	{0x0691, 0xFB8C, 2}, // ڑ Urdu Rreh
	{0x0698, 0xFB8A, 2}, // ژ Persian/Urdu Jeh
	{0x06A4, 0xFB6A, 4}, // ڤ Kurdish Veh
	{0x06A6, 0xFB6E, 4}, // ڦ Sindhi Peheh
	{0x06A9, 0xFB8E, 4}, // ک Persian/Urdu Keheh
	{0x06AD, 0xFBD3, 4}, // ڭ Ng
	{0x06AF, 0xFB92, 4}, // گ Persian/Urdu Gaf
	{0x06B1, 0xFB9A, 4}, // ڱ Sindhi Ngoeh
	{0x06B3, 0xFB96, 4}, // ڳ Sindhi Gueh
	{0x06BA, 0xFB9E, 2}, // ں Urdu Noon Ghunna (Initial/Medial use Noon 0xFEE7/0xFEE8)
	{0x06BB, 0xFBA0, 4}, // ڻ Sindhi Rnoon
	{0x06BE, 0xFBAA, 4}, // ھ Urdu Heh Doachashmee
	{0x06C0, 0xFBA4, 2}, // ۀ Heh with Yeh above
	{0x06C1, 0xFBA6, 4}, // ہ Urdu Heh Goal
	{0x06C5, 0xFBE0, 2}, // ۅ Kirghiz Oe
	{0x06C6, 0xFBD9, 2}, // ۆ Kurdish Oe
	{0x06C7, 0xFBD7, 2}, // ۇ U
	{0x06C8, 0xFBDB, 2}, // ۈ Yu
	{0x06C9, 0xFBE2, 2}, // ۉ Kirghiz Yu
	{0x06CB, 0xFBDE, 2}, // ۋ Ve
	{0x06CC, 0xFBFC, 4}, // ی Persian/Urdu Farsi Yeh
	{0x06D0, 0xFBE4, 4}, // ې Pashto E
	{0x06D2, 0xFBAE, 2}, // ے Urdu Yeh Barree
	{0x06D3, 0xFBB0, 2}, // ۓ Urdu Yeh Barree with Hamza
}

func arabicJoinType(c rune) uint8 {
	if c >= 0x0621 && c <= 0x064A {
		return baseArabicShapes[c-0x0621].joinType
	}
	lo, hi := 0, len(extArabicJoinRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		r := extArabicJoinRanges[mid]
		if c < r.first {
			hi = mid - 1
		} else if c > r.last {
			lo = mid + 1
		} else {
			return r.joinType
		}
	}
	return joinU
}

func arabicShapeForm(c rune, form uint8) rune {
	if c >= 0x0621 && c <= 0x064A {
		fb := baseArabicShapes[c-0x0621].formB
		if fb != 0 {
			return 0xFE00 + rune(fb) + rune(form)
		}
		return c
	}
	// Urdu Noon Ghunna (ں U+06BA) shares Initial/Medial glyph forms with standard Noon (ن U+0646)
	if c == 0x06BA && (form == shapeInitial || form == shapeMedial) {
		return 0xFE00 + 0xE5 + rune(form)
	}
	// Kurdish Ae (ە U+06D5) shares Isolated/Final glyph forms with Heh (ه U+0647)
	if c == 0x06D5 && (form == shapeIsolated || form == shapeFinal) {
		return 0xFE00 + 0xE9 + rune(form)
	}
	lo, hi := 0, len(extArabicShapeForms)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		e := extArabicShapeForms[mid]
		if c < e.cp {
			hi = mid - 1
		} else if c > e.cp {
			lo = mid + 1
		} else {
			if form < e.forms {
				return e.isolated + rune(form)
			}
			return c
		}
	}
	return c
}

// isCombining returns true ONLY for true Unicode Non-Spacing Combining Marks (Mn).
// Specifically excludes U+0671..U+06D5 (which are Persian/Urdu/Arabic letters, NOT combining marks).
func isCombining(r rune) bool {
	return (r >= 0x0300 && r <= 0x036F) ||
		(r >= 0x0591 && r <= 0x05BD) || r == 0x05BF ||
		(r >= 0x05C1 && r <= 0x05C2) || (r >= 0x05C4 && r <= 0x05C5) || r == 0x05C7 ||
		(r >= 0x0610 && r <= 0x061A) || (r >= 0x064B && r <= 0x065F) || r == 0x0670 ||
		(r >= 0x06D6 && r <= 0x06DC) || (r >= 0x06DF && r <= 0x06E4) ||
		(r >= 0x06E7 && r <= 0x06E8) || (r >= 0x06EA && r <= 0x06ED) ||
		r == 0x0711 || (r >= 0x0730 && r <= 0x074A) ||
		(r >= 0x07A6 && r <= 0x07B0) ||
		(r >= 0x08D3 && r <= 0x08E1) || (r >= 0x08E3 && r <= 0x08FF) ||
		(r >= 0x0E31 && r <= 0x0E3A) || (r >= 0x0E47 && r <= 0x0E4E) ||
		(r >= 0x0EB1 && r <= 0x0EBC) || (r >= 0x0EC8 && r <= 0x0ECD)
}

func isRTLTransparentMark(r rune) bool {
	if r < 0x0591 {
		return false
	}
	if isCombining(r) {
		return true
	}
	p, _ := bidi.LookupRune(r)
	return p.Class() == bidi.NSM
}

func isBidiClassNSM(r rune) bool {
	if isCombining(r) {
		return true
	}
	p, _ := bidi.LookupRune(r)
	return p.Class() == bidi.NSM
}

// mirrorRune implements UAX#9 Rule L4 character mirroring for odd (RTL) embedding levels.
func mirrorRune(r rune) rune {
	switch r {
	case '(':
		return ')'
	case ')':
		return '('
	case '[':
		return ']'
	case ']':
		return '['
	case '{':
		return '}'
	case '}':
		return '{'
	case '<':
		return '>'
	case '>':
		return '<'
	case '«':
		return '»'
	case '»':
		return '«'
	case '‹':
		return '›'
	case '›':
		return '‹'
	case '⟨':
		return '⟩'
	case '⟩':
		return '⟨'
	case '⟪':
		return '⟫'
	case '⟫':
		return '⟪'
	}
	p, _ := bidi.LookupRune(r)
	if p.IsBracket() {
		// Fallback for other Unicode paired brackets
	}
	return r
}

type bidiChar struct {
	wc      rune
	origwc  rune
	index   int
	joiners uint8
}

func nextNonNSM(line []bidiChar, i int) int {
	j := i + 1
	for j < len(line) && isBidiClassNSM(line[j].wc) {
		j++
	}
	return j
}

func prevNonNSM(line []bidiChar, i int) int {
	j := i - 1
	for j >= 0 && isBidiClassNSM(line[j].wc) {
		j--
	}
	return j
}

// shapeVisualLine resolves Arabic/Persian/Urdu contextual forms and Lam-Alef ligatures
// on a visual-order bidiChar slice (matching lib/MiniBidi/minibidi.c do_shape).
func shapeVisualLine(line []bidiChar) []bidiChar {
	count := len(line)
	to := make([]bidiChar, count)
	for i := 0; i < count; i++ {
		to[i] = line[i]
	}

	for i := 0; i < count; i++ {
		if to[i].wc == ligaturePlaceholder {
			continue
		}
		jt := arabicJoinType(line[i].wc)
		switch jt {
		case joinR:
			nx := nextNonNSM(line, i)
			nxType := uint8(joinU)
			if nx < count {
				nxType = arabicJoinType(line[nx].wc)
			}
			prevJoiners := line[i].joiners >> 4
			if (prevJoiners & zwnjFlag) != 0 {
				to[i].wc = arabicShapeForm(line[i].wc, shapeIsolated)
			} else if nxType == joinL || nxType == joinD || nxType == joinC || (prevJoiners&zwjFlag) != 0 {
				to[i].wc = arabicShapeForm(line[i].wc, shapeFinal)
			} else {
				to[i].wc = arabicShapeForm(line[i].wc, shapeIsolated)
			}

		case joinD:
			nx := nextNonNSM(line, i)
			pv := prevNonNSM(line, i)
			nxType := uint8(joinU)
			if nx < count {
				nxType = arabicJoinType(line[nx].wc)
			}

			// Mandatory Lam-Alef ligatures (Lam is at i, visually-left Alef is at pv)
			if line[i].wc == 0x0644 && (line[i].joiners&zwnjFlag) == 0 && pv >= 0 && ((line[pv].joiners>>4)&zwnjFlag) == 0 {
				var ligIso, ligFin rune
				switch line[pv].wc {
				case 0x0622: // لآ
					ligIso, ligFin = 0xFEF5, 0xFEF6
				case 0x0623: // لأ
					ligIso, ligFin = 0xFEF7, 0xFEF8
				case 0x0625: // لإ
					ligIso, ligFin = 0xFEF9, 0xFEFA
				case 0x0627, 0x0671: // لا / لٱ
					ligIso, ligFin = 0xFEFB, 0xFEFC
				}
				if ligIso != 0 {
					prevJoiners := line[i].joiners >> 4
					if (prevJoiners&zwnjFlag) == 0 && (nxType == joinL || nxType == joinD || nxType == joinC || (prevJoiners&zwjFlag) != 0) {
						to[i].wc = ligFin
					} else {
						to[i].wc = ligIso
					}
					to[pv].wc = ligaturePlaceholder
					continue
				}
			}

			joiners := line[i].joiners & 0xF
			prevJoiners := line[i].joiners >> 4
			pvType := uint8(joinU)
			if pv >= 0 {
				pvType = arabicJoinType(line[pv].wc)
			}

			if prevJoiners == zwnjFlag {
				// Backward join (to nx, visually right / logically previous) is blocked by ZWNJ;
				// can only join forward (to pv, visually left / logically next).
				if pvType == joinR || pvType == joinD || pvType == joinC {
					to[i].wc = arabicShapeForm(line[i].wc, shapeInitial)
				} else {
					to[i].wc = arabicShapeForm(line[i].wc, shapeIsolated)
				}
			} else if prevJoiners == (zwjFlag | zwnjFlag) {
				to[i].wc = arabicShapeForm(line[i].wc, shapeMedial)
			} else if prevJoiners == zwjFlag {
				if pvType == joinR || pvType == joinD || pvType == joinC {
					to[i].wc = arabicShapeForm(line[i].wc, shapeMedial)
				} else {
					to[i].wc = arabicShapeForm(line[i].wc, shapeFinal)
				}
			} else if (joiners & zwnjFlag) != 0 {
				// Forward join (to pv, visually left / logically next) is blocked by ZWNJ;
				// can only join backward (to nx, visually right / logically previous).
				if nxType == joinL || nxType == joinD || nxType == joinC {
					to[i].wc = arabicShapeForm(line[i].wc, shapeFinal)
				} else {
					to[i].wc = arabicShapeForm(line[i].wc, shapeIsolated)
				}
			} else if nxType == joinL || nxType == joinD || nxType == joinC {
				if pvType == joinR || pvType == joinD || pvType == joinC {
					to[i].wc = arabicShapeForm(line[i].wc, shapeMedial)
				} else {
					to[i].wc = arabicShapeForm(line[i].wc, shapeFinal)
				}
			} else {
				if pvType == joinR || pvType == joinD || pvType == joinC {
					to[i].wc = arabicShapeForm(line[i].wc, shapeInitial)
				} else {
					to[i].wc = arabicShapeForm(line[i].wc, shapeIsolated)
				}
			}
		}
	}
	return to
}

// doBidiAndShape runs UAX#9 level resolution, L2 visual reordering, L4 bracket mirroring,
// Arabic/Persian/Urdu contextual shaping, and L3 combining-mark ordering.
// baseDir: -1 = auto-detect (P2/P3), 0 = LTR, 1 = RTL.
func applyBidiVisual(input string, baseDir int) string {
	if input == "" {
		return ""
	}

	// Build logical bidiChar slice, attaching ZWJ/ZWNJ joiner flags to adjacent base chars.
	var line []bidiChar
	var rawRunes []rune
	lastBase := -1
	pendingJoiners := uint8(0)

	for _, r := range input {
		if r == 0x200C || r == 0x200D {
			jFlag := uint8(zwnjFlag)
			if r == 0x200D {
				jFlag = zwjFlag
			}
			if lastBase >= 0 {
				line[lastBase].joiners |= jFlag
			}
			pendingJoiners |= jFlag
			continue
		}
		// Strip zero-width space / BOM / word joiner
		if r == 0x200B || r == 0x2060 || r == 0xFEFF {
			continue
		}
		idx := len(line)
		line = append(line, bidiChar{
			wc:      r,
			origwc:  r,
			index:   idx,
			joiners: pendingJoiners << 4,
		})
		rawRunes = append(rawRunes, r)
		pendingJoiners = 0
		if !isBidiClassNSM(r) {
			lastBase = idx
		}
	}

	count := len(line)
	if count == 0 {
		return ""
	}

	// Determine paragraph embedding level (P2/P3 when baseDir < 0)
	paraLevel := baseDir
	if paraLevel < 0 {
		paraLevel = detectParagraphLevelRunes(rawRunes, 0)
	}

	// Compute UAX#9 resolved embedding levels for each rune in rawRunes
	levels := computeUAX9Levels(rawRunes, paraLevel)

	// Filter out explicit directional format characters (LRM, RLM, LRE..RLO, PDF, LRI..PDI)
	// before L2 reordering and Arabic shaping so they don't block adjacent Arabic joining.
	var filteredLine []bidiChar
	var filteredLevels []int
	for i := 0; i < count; i++ {
		r := line[i].wc
		if isExplicitBidiFormatRune(r) {
			continue
		}
		filteredLine = append(filteredLine, line[i])
		filteredLevels = append(filteredLevels, levels[i])
	}

	count = len(filteredLine)
	if count == 0 {
		return ""
	}

	// UAX#9 Rule L2: Reverse contiguous runs from highest level down to lowest odd level.
	maxLevel := paraLevel
	minOdd := 255
	for _, lv := range filteredLevels {
		if lv > maxLevel {
			maxLevel = lv
		}
		if (lv&1) == 1 && lv < minOdd {
			minOdd = lv
		}
	}
	for lv := maxLevel; lv >= minOdd; lv-- {
		i := 0
		for i < count {
			if filteredLevels[i] >= lv {
				start := i
				for i < count && filteredLevels[i] >= lv {
					i++
				}
				for l, r := start, i-1; l < r; l, r = l+1, r-1 {
					filteredLine[l], filteredLine[r] = filteredLine[r], filteredLine[l]
					filteredLevels[l], filteredLevels[r] = filteredLevels[r], filteredLevels[l]
				}
			} else {
				i++
			}
		}
	}

	// UAX#9 Rule L4: Mirror paired brackets/delimiters in odd (RTL) levels.
	for i := 0; i < count; i++ {
		if (filteredLevels[i] & 1) == 1 {
			filteredLine[i].wc = mirrorRune(filteredLine[i].wc)
		}
	}

	// Resolve contextual Arabic/Persian/Urdu glyph shapes in visual order.
	shaped := shapeVisualLine(filteredLine)

	// UAX#9 Rule L3: Reversing an RTL run places combining marks visually BEFORE their base glyph.
	// Since our left-to-right bitmap renderer draws the base glyph first and overlays marks on
	// the preceding base glyph, emit the base glyph first followed by its marks in logical order.
	var out strings.Builder
	out.Grow(len(input))
	isFiltered := func(r rune) bool {
		return r == ligaturePlaceholder || r == 0x200C || r == 0x200D
	}

	for i := 0; i < count; i++ {
		cp := shaped[i].wc
		if isFiltered(cp) {
			continue
		}
		if !isRTLTransparentMark(cp) {
			out.WriteRune(cp)
			continue
		}
		j := i
		for j < count && (isFiltered(shaped[j].wc) || isRTLTransparentMark(shaped[j].wc)) {
			j++
		}
		if j < count && shaped[j].index < shaped[i].index {
			out.WriteRune(shaped[j].wc)
			for k := j - 1; k >= i; k-- {
				if isRTLTransparentMark(shaped[k].wc) {
					out.WriteRune(shaped[k].wc)
				}
			}
			i = j
		} else {
			for k := i; k < j; k++ {
				if isRTLTransparentMark(shaped[k].wc) {
					out.WriteRune(shaped[k].wc)
				}
			}
			i = j - 1
		}
	}
	return out.String()
}

func isExplicitBidiFormatRune(r rune) bool {
	return (r >= 0x200E && r <= 0x200F) ||
		(r >= 0x202A && r <= 0x202E) ||
		(r >= 0x2066 && r <= 0x2069) ||
		(r >= 0x206A && r <= 0x206F)
}

func detectParagraphLevelRunes(runes []rune, fallback int) int {
	isolateDepth := 0
	for _, r := range runes {
		p, _ := bidi.LookupRune(r)
		cls := p.Class()
		switch cls {
		case bidi.LRI, bidi.RLI, bidi.FSI:
			isolateDepth++
		case bidi.PDI:
			if isolateDepth > 0 {
				isolateDepth--
			}
		case bidi.R, bidi.AL:
			if isolateDepth == 0 {
				return 1
			}
		case bidi.L:
			if isolateDepth == 0 {
				return 0
			}
		}
	}
	return fallback & 1
}

// computeUAX9Levels implements the UAX#9 level resolution algorithm for a single paragraph/line.
func computeUAX9Levels(runes []rune, paraLevel int) []int {
	n := len(runes)
	if n == 0 {
		return nil
	}

	types := make([]bidi.Class, n)
	origTypes := make([]bidi.Class, n)
	levels := make([]int, n)
	for i, r := range runes {
		if isCombining(r) {
			types[i] = bidi.NSM
		} else {
			p, _ := bidi.LookupRune(r)
			types[i] = p.Class()
		}
		origTypes[i] = types[i]
		levels[i] = paraLevel
	}

	// X1-X8: Explicit levels and isolates
	type dirFrame struct {
		level   int
		over    bidi.Class
		isolate bool
	}
	stack := []dirFrame{{level: paraLevel, over: bidi.ON, isolate: false}}
	curLevel := paraLevel
	curOver := bidi.ON
	isolateCount := 0

	for i := 0; i < n; i++ {
		t := types[i]
		if t == bidi.FSI {
			// Resolve FSI to RLI or LRI by scanning to matching PDI
			depth := 0
			t = bidi.LRI
			for k := i + 1; k < n; k++ {
				kt := types[k]
				if kt == bidi.FSI || kt == bidi.RLI || kt == bidi.LRI {
					depth++
				} else if kt == bidi.PDI {
					if depth > 0 {
						depth--
					} else {
						break
					}
				} else if depth == 0 {
					if kt == bidi.R || kt == bidi.AL {
						t = bidi.RLI
						break
					} else if kt == bidi.L {
						t = bidi.LRI
						break
					}
				}
			}
		}

		switch t {
		case bidi.RLE:
			levels[i] = curLevel
			nextOdd := (curLevel + 1) | 1
			if nextOdd <= 125 {
				curLevel = nextOdd
				curOver = bidi.ON
				stack = append(stack, dirFrame{curLevel, curOver, false})
			}
			types[i] = bidi.BN
		case bidi.LRE:
			levels[i] = curLevel
			nextEven := (curLevel + 2) &^ 1
			if nextEven <= 124 {
				curLevel = nextEven
				curOver = bidi.ON
				stack = append(stack, dirFrame{curLevel, curOver, false})
			}
			types[i] = bidi.BN
		case bidi.RLO:
			levels[i] = curLevel
			nextOdd := (curLevel + 1) | 1
			if nextOdd <= 125 {
				curLevel = nextOdd
				curOver = bidi.R
				stack = append(stack, dirFrame{curLevel, curOver, false})
			}
			types[i] = bidi.BN
		case bidi.LRO:
			levels[i] = curLevel
			nextEven := (curLevel + 2) &^ 1
			if nextEven <= 124 {
				curLevel = nextEven
				curOver = bidi.L
				stack = append(stack, dirFrame{curLevel, curOver, false})
			}
			types[i] = bidi.BN
		case bidi.RLI:
			levels[i] = curLevel
			if curOver != bidi.ON {
				types[i] = curOver
			} else {
				types[i] = bidi.ON
			}
			nextOdd := (curLevel + 1) | 1
			if nextOdd <= 125 {
				curLevel = nextOdd
				curOver = bidi.ON
				isolateCount++
				stack = append(stack, dirFrame{curLevel, curOver, true})
			}
		case bidi.LRI:
			levels[i] = curLevel
			if curOver != bidi.ON {
				types[i] = curOver
			} else {
				types[i] = bidi.ON
			}
			nextEven := (curLevel + 2) &^ 1
			if nextEven <= 124 {
				curLevel = nextEven
				curOver = bidi.ON
				isolateCount++
				stack = append(stack, dirFrame{curLevel, curOver, true})
			}
		case bidi.PDI:
			if isolateCount > 0 {
				for len(stack) > 1 && !stack[len(stack)-1].isolate {
					stack = stack[:len(stack)-1]
				}
				if len(stack) > 1 {
					stack = stack[:len(stack)-1]
				}
				isolateCount--
				top := stack[len(stack)-1]
				curLevel = top.level
				curOver = top.over
			}
			levels[i] = curLevel
			if curOver != bidi.ON {
				types[i] = curOver
			} else {
				types[i] = bidi.ON
			}
		case bidi.PDF:
			levels[i] = curLevel
			if len(stack) > 1 && !stack[len(stack)-1].isolate {
				stack = stack[:len(stack)-1]
				top := stack[len(stack)-1]
				curLevel = top.level
				curOver = top.over
			}
			types[i] = bidi.BN
		case bidi.B:
			levels[i] = paraLevel
		default:
			levels[i] = curLevel
			if curOver != bidi.ON && t != bidi.NSM {
				types[i] = curOver
			}
		}
	}

	// W1: NSM takes the type of the preceding character (or embedding direction at start)
	prevType := bidi.L
	if (paraLevel & 1) == 1 {
		prevType = bidi.R
	}
	for i := 0; i < n; i++ {
		if types[i] == bidi.BN {
			continue
		}
		if types[i] == bidi.NSM {
			types[i] = prevType
		} else {
			if types[i] == bidi.LRI || types[i] == bidi.RLI || types[i] == bidi.FSI || types[i] == bidi.PDI {
				prevType = bidi.ON
			} else {
				prevType = types[i]
			}
		}
	}

	// W2: EN -> AN when last strong is AL; W3: AL -> R
	lastStrong := bidi.L
	if (paraLevel & 1) == 1 {
		lastStrong = bidi.R
	}
	for i := 0; i < n; i++ {
		t := types[i]
		if t == bidi.L || t == bidi.R || t == bidi.AL {
			lastStrong = t
			if t == bidi.AL {
				types[i] = bidi.R
			}
		} else if t == bidi.EN && lastStrong == bidi.AL {
			types[i] = bidi.AN
		}
	}

	// W4: Single ES between ENs -> EN; single CS between ENs or ANs -> that type
	for i := 1; i < n-1; i++ {
		if types[i] == bidi.ES && types[i-1] == bidi.EN && types[i+1] == bidi.EN {
			types[i] = bidi.EN
		} else if types[i] == bidi.CS {
			if types[i-1] == bidi.EN && types[i+1] == bidi.EN {
				types[i] = bidi.EN
			} else if types[i-1] == bidi.AN && types[i+1] == bidi.AN {
				types[i] = bidi.AN
			}
		}
	}

	// W5: ET adjacent to EN -> EN
	for i := 0; i < n; i++ {
		if types[i] == bidi.ET {
			end := i
			for end+1 < n && types[end+1] == bidi.ET {
				end++
			}
			hasEN := (i > 0 && types[i-1] == bidi.EN) || (end+1 < n && types[end+1] == bidi.EN)
			if hasEN {
				for k := i; k <= end; k++ {
					types[k] = bidi.EN
				}
			}
			i = end
		}
	}

	// W6: Remaining ES, ET, CS -> ON
	for i := 0; i < n; i++ {
		if types[i] == bidi.ES || types[i] == bidi.ET || types[i] == bidi.CS {
			types[i] = bidi.ON
		}
	}

	// W7: EN -> L when last strong is L
	lastStrong = bidi.L
	if (paraLevel & 1) == 1 {
		lastStrong = bidi.R
	}
	for i := 0; i < n; i++ {
		if types[i] == bidi.L || types[i] == bidi.R {
			lastStrong = types[i]
		} else if types[i] == bidi.EN && lastStrong == bidi.L {
			types[i] = bidi.L
		}
	}

	isNeutral := func(c bidi.Class) bool {
		return c == bidi.B || c == bidi.S || c == bidi.WS || c == bidi.ON ||
			c == bidi.LRI || c == bidi.RLI || c == bidi.FSI || c == bidi.PDI
	}

	// N1 & N2: Resolve neutrals per level run
	for i := 0; i < n; i++ {
		if types[i] == bidi.BN || !isNeutral(types[i]) {
			continue
		}
		runLvl := levels[i]
		end := i
		for end+1 < n && levels[end+1] == runLvl && (types[end+1] == bidi.BN || isNeutral(types[end+1])) {
			end++
		}
		embDir := bidi.L
		if (runLvl & 1) == 1 {
			embDir = bidi.R
		}
		prevS := embDir
		for j := i - 1; j >= 0 && levels[j] == runLvl; j-- {
			if types[j] == bidi.BN {
				continue
			}
			if types[j] == bidi.L {
				prevS = bidi.L
				break
			}
			if types[j] == bidi.R || types[j] == bidi.EN || types[j] == bidi.AN {
				prevS = bidi.R
				break
			}
		}
		nextS := embDir
		for j := end + 1; j < n && levels[j] == runLvl; j++ {
			if types[j] == bidi.BN {
				continue
			}
			if types[j] == bidi.L {
				nextS = bidi.L
				break
			}
			if types[j] == bidi.R || types[j] == bidi.EN || types[j] == bidi.AN {
				nextS = bidi.R
				break
			}
		}
		resolved := embDir
		if prevS == nextS {
			resolved = prevS
		}
		for j := i; j <= end; j++ {
			if types[j] != bidi.BN {
				types[j] = resolved
			}
		}
		i = end
	}

	// I1 & I2: Implicit level adjustments
	for i := 0; i < n; i++ {
		if types[i] == bidi.BN {
			continue
		}
		if (levels[i] & 1) == 0 {
			if types[i] == bidi.R {
				levels[i] += 1
			} else if types[i] == bidi.AN || types[i] == bidi.EN {
				levels[i] += 2
			}
		} else {
			if types[i] == bidi.L || types[i] == bidi.AN || types[i] == bidi.EN {
				levels[i] += 1
			}
		}
	}

	// L1: Reset trailing whitespace to paragraph level
	for i := n - 1; i >= 0; i-- {
		ot := origTypes[i]
		if ot == bidi.WS || ot == bidi.B || ot == bidi.S ||
			ot == bidi.LRI || ot == bidi.RLI || ot == bidi.FSI || ot == bidi.PDI || ot == bidi.BN {
			levels[i] = paraLevel
		} else {
			break
		}
	}

	return levels
}

// isRTLLangCode returns true if a BCP-47 lang attribute specifies an RTL language
// and does NOT specify a non-RTL script subtag (such as -Latn).
func isRTLLangCode(lang string) bool {
	if lang == "" {
		return false
	}
	l := strings.ToLower(strings.TrimSpace(lang))
	// Explicit romanization / Latin / Cyrillic / Greek script subtags are LTR
	if strings.Contains(l, "-latn") || strings.Contains(l, "-cyrl") || strings.Contains(l, "-grek") || strings.Contains(l, "-deva") {
		return false
	}
	// Explicit RTL script subtags
	if strings.Contains(l, "-arab") || strings.Contains(l, "-hebr") || strings.Contains(l, "-syrc") ||
		strings.Contains(l, "-thaa") || strings.Contains(l, "-nkoo") || strings.Contains(l, "-adlm") {
		return true
	}
	primary := l
	if idx := strings.IndexByte(l, '-'); idx != -1 {
		primary = l[:idx]
	}
	switch primary {
	case "ar", "fa", "ur", "he", "iw", "yi", "ji", "ps", "sd", "ug", "ckb",
		"azb", "mzn", "glk", "lrc", "pnb", "skr", "ks", "bal", "prs", "pes",
		"arz", "ary", "apc", "afb", "acm", "acw", "aeb", "ajp", "apd", "ars",
		"ayn", "hbo", "syr", "syc", "arc", "sam", "myz", "dv", "nqo", "adlm",
		"khw", "bqi", "hac", "ota", "jrb", "jpr", "lad":
		return true
	}
	return false
}

// classifyParsoidTagDir returns:
//   'r' if the tag establishes an RTL context (dir="rtl", RTL lang, script-arabic/hebrew, Nastaleeq)
//   'l' if the tag explicitly establishes an LTR context (dir="ltr", or -Latn lang)
//   'a' if the tag establishes an auto-direction isolate (<bdi> or dir="auto")
//   0   otherwise
func classifyParsoidTagDir(tagName string, attrs []html.Attribute) byte {
	var dirVal, langVal, classVal, styleVal string
	for _, a := range attrs {
		switch a.Key {
		case "dir":
			dirVal = strings.ToLower(strings.TrimSpace(a.Val))
		case "lang":
			langVal = a.Val
		case "class":
			classVal = a.Val
		case "style":
			styleVal = a.Val
		}
	}

	if dirVal == "rtl" {
		return 'r'
	}
	if dirVal == "ltr" {
		return 'l'
	}

	if langVal != "" {
		l := strings.ToLower(langVal)
		if strings.Contains(l, "-latn") {
			return 'l'
		}
		if isRTLLangCode(langVal) {
			return 'r'
		}
	}

	if classVal != "" {
		if hasClassToken(classVal, "script-arabic", "script-Arab", "script-hebrew", "script-Hebr",
			"script-Syrc", "script-Thaa", "script-Nkoo", "mw-content-rtl") {
			return 'r'
		}
	}

	if styleVal != "" {
		sl := strings.ToLower(styleVal)
		if strings.Contains(sl, "nastaleeq") || strings.Contains(sl, "nastaliq") || strings.Contains(sl, "scheherazade") || strings.Contains(sl, "amiri") {
			return 'r'
		}
	}

	if dirVal == "auto" || tagName == "bdi" {
		return 'a'
	}
	return 0
}

// hasRTLOrFormatUTF8 performs a fast byte scan to check whether s contains any
// RTL script bytes (U+0590..U+08FF, U+FB1D..U+FEFF) or invisible Bidi formatters (U+200B..U+206F, U+FEFF).
func hasRTLOrFormatUTF8(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x80 {
			continue
		}
		// 2-byte UTF-8: 0xD6..0xDF covers U+0580..U+07FF (Hebrew, Arabic, Syriac, Thaana, NKo)
		if b >= 0xD6 && b <= 0xDF {
			return true
		}
		if i+2 < len(s) {
			b1, b2 := s[i+1], s[i+2]
			switch b {
			case 0xE0:
				// U+0800..U+08FF (Samaritan, Mandaic, Arabic Extended-A/B)
				if b1 >= 0xA0 && b1 <= 0xA3 {
					return true
				}
			case 0xE2:
				// U+200B..U+200F, U+202A..U+202E, U+2060..U+206F
				if (b1 == 0x80 && ((b2 >= 0x8B && b2 <= 0x8F) || (b2 >= 0xAA && b2 <= 0xAE))) ||
					(b1 == 0x81 && b2 >= 0xA0 && b2 <= 0xAF) {
					return true
				}
			case 0xEF:
				// U+FB1D..U+FDFF (Hebrew & Arabic Presentation Forms-A) and U+FE70..U+FEFF (Forms-B + BOM)
				if b1 >= 0xAC && b1 <= 0xBF {
					return true
				}
			}
		}
	}
	return false
}

func isRTLScriptRune(r rune) bool {
	return (r >= 0x0590 && r <= 0x08FF) ||
		(r >= 0xFB1D && r <= 0xFDFF) ||
		(r >= 0xFE70 && r <= 0xFEFC)
}

func hasRTLRune(s string) bool {
	for _, r := range s {
		if isRTLScriptRune(r) {
			return true
		}
	}
	return false
}

func hasStrongLTRRune(s string) bool {
	for _, r := range s {
		if isExplicitBidiFormatRune(r) || isRTLScriptRune(r) {
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return true
		}
		if r >= 0x0080 {
			p, _ := bidi.LookupRune(r)
			if p.Class() == bidi.L {
				return true
			}
		}
	}
	return false
}

func stripInvisibleFormatRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 0x200B && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) ||
			(r >= 0x2060 && r <= 0x206F) || r == 0xFEFF {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mergeAdjacentRTLIsolates merges consecutive \u2067...\u2069 (RLI...PDI) spans
// that are adjacent or separated only by whitespace (e.g., `<span lang="fa">لغت</span> <span lang="fa">انجمن</span>`).
func mergeAdjacentRTLIsolates(s string) string {
	if !strings.ContainsRune(s, '\u2067') && !strings.ContainsRune(s, '\u2068') {
		return s
	}
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\u2069' {
			j := i + 1
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t' || runes[j] == '\u00a0') {
				j++
			}
			if j < len(runes) && (runes[j] == '\u2067' || runes[j] == '\u2068') {
				// Merge the two adjacent RLI/FSI...PDI spans across any intervening whitespace
				out = append(out, runes[i+1:j]...)
				i = j
				continue
			}
		}
		out = append(out, runes[i])
	}
	return string(out)
}

// wrapLongRTLIsolateForTable splits very long RTL phrases (> maxRunes) at logical word boundaries
// separated by `\n` (`\\n` in table cells) BEFORE visual reordering so multi-line RTL phrases in
// infoboxes/tables read top-to-bottom in logical order and right-to-left on each line.
func applyBidiVisualTableIsolate(inner string, baseDir int, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(inner) <= maxRunes || !strings.ContainsRune(inner, ' ') {
		return applyBidiVisual(inner, baseDir)
	}
	words := strings.Fields(inner)
	if len(words) <= 1 {
		return applyBidiVisual(inner, baseDir)
	}
	var lines []string
	var cur strings.Builder
	curRunes := 0
	for _, w := range words {
		wRunes := utf8.RuneCountInString(w)
		if curRunes > 0 && curRunes+1+wRunes > maxRunes {
			lines = append(lines, applyBidiVisual(cur.String(), baseDir))
			cur.Reset()
			cur.WriteString(w)
			curRunes = wRunes
		} else {
			if curRunes > 0 {
				cur.WriteByte(' ')
				curRunes++
			}
			cur.WriteString(w)
			curRunes += wRunes
		}
	}
	if cur.Len() > 0 {
		lines = append(lines, applyBidiVisual(cur.String(), baseDir))
	}
	return strings.Join(lines, `\n`)
}

// processRTLInTextSpan shapes and visually orders RTL content in a plain-text span
// (which may contain \u2067..\u2069 isolate markers from HTML dir/lang/bdi tags).
func processRTLInTextSpan(s string, inTableCell bool) string {
	if !hasRTLOrFormatUTF8(s) {
		return s
	}
	if !hasRTLRune(s) {
		return stripInvisibleFormatRunes(s)
	}

	s = mergeAdjacentRTLIsolates(s)

	// Preserve leading/trailing ASCII whitespace outside Bidi reordering so inter-word
	// spaces around inline RTL spans are never swallowed or moved.
	leadLen := 0
	for leadLen < len(s) && (s[leadLen] == ' ' || s[leadLen] == '\t' || s[leadLen] == '\n' || s[leadLen] == '\r') {
		leadLen++
	}
	trailStart := len(s)
	for trailStart > leadLen && (s[trailStart-1] == ' ' || s[trailStart-1] == '\t' || s[trailStart-1] == '\n' || s[trailStart-1] == '\r') {
		trailStart--
	}
	core := s[leadLen:trailStart]
	if core == "" {
		return stripInvisibleFormatRunes(s)
	}

	// If core contains explicit \u2067 (RLI) or \u2068 (FSI) isolate blocks,
	// process each isolate block with its isolate direction and each outside chunk appropriately.
	if strings.ContainsRune(core, '\u2067') || strings.ContainsRune(core, '\u2068') {
		var out strings.Builder
		out.Grow(len(s))
		out.WriteString(s[:leadLen])

		runes := []rune(core)
		i := 0
		for i < len(runes) {
			if runes[i] == '\u2067' || runes[i] == '\u2068' {
				openMarker := runes[i]
				depth := 1
				j := i + 1
				for j < len(runes) && depth > 0 {
					if runes[j] == '\u2067' || runes[j] == '\u2068' {
						depth++
					} else if runes[j] == '\u2069' {
						depth--
						if depth == 0 {
							break
						}
					}
					j++
				}
				inner := string(runes[i+1 : j])
				if hasRTLRune(inner) {
					dir := 1
					if openMarker == '\u2068' {
						dir = -1
					}
					// Keep leading/trailing spaces of inner outside the isolate reversal
					iLead := 0
					for iLead < len(inner) && inner[iLead] == ' ' {
						iLead++
					}
					iTrail := len(inner)
					for iTrail > iLead && inner[iTrail-1] == ' ' {
						iTrail--
					}
					out.WriteString(inner[:iLead])
					trimmedInner := inner[iLead:iTrail]
					if inTableCell {
						out.WriteString(applyBidiVisualTableIsolate(trimmedInner, dir, 34))
					} else {
						out.WriteString(applyBidiVisual(trimmedInner, dir))
					}
					out.WriteString(inner[iTrail:])
				} else {
					out.WriteString(stripInvisibleFormatRunes(inner))
				}
				if j < len(runes) && runes[j] == '\u2069' {
					i = j + 1
				} else {
					i = j
				}
			} else {
				j := i
				for j < len(runes) && runes[j] != '\u2067' && runes[j] != '\u2068' {
					j++
				}
				outside := string(runes[i:j])
				if hasRTLRune(outside) {
					dir := 0
					if !hasStrongLTRRune(outside) {
						dir = 1
					}
					out.WriteString(applyBidiVisual(outside, dir))
				} else {
					out.WriteString(stripInvisibleFormatRunes(outside))
				}
				i = j
			}
		}

		out.WriteString(s[trailStart:])
		return out.String()
	}

	// No explicit \u2067/\u2068 markers: bare RTL text in a span/cell/paragraph
	dir := 0
	if !hasStrongLTRRune(core) {
		dir = 1
	}
	var out strings.Builder
	out.Grow(len(s))
	out.WriteString(s[:leadLen])
	if inTableCell && dir == 1 {
		out.WriteString(applyBidiVisualTableIsolate(core, dir, 34))
	} else {
		out.WriteString(applyBidiVisual(core, dir))
	}
	out.WriteString(s[trailStart:])
	return out.String()
}

// decodeTextPreserveBidi unescapes HTML entities and strips only non-directional
// zero-width characters (ZWSP U+200B, Word Joiner U+2060, BOM U+FEFF), while preserving
// ZWNJ (U+200C), ZWJ (U+200D), LRM/RLM (U+200E..U+200F), and Bidi Isolates (U+2066..U+2069)
// so that the subsequent UAX#9 + Arabic shaping pass can use them.
func decodeTextPreserveBidi(text string) string {
	if strings.IndexByte(text, '&') != -1 {
		text = stdhtml.UnescapeString(text)
	}
	if !strings.ContainsAny(text, "\u200b\u2060\ufeff") {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == 0x200B || r == 0x2060 || r == 0xFEFF {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// processRTLInBodyMarkup processes all text segments between preserved HTML tags (<...>)
// and block markers (\n) in extracted article body markup.
func processRTLInBodyMarkup(markup string) string {
	if !hasRTLOrFormatUTF8(markup) {
		return markup
	}
	var out strings.Builder
	out.Grow(len(markup))

	n := len(markup)
	i := 0
	for i < n {
		if markup[i] == '<' {
			end := strings.IndexByte(markup[i:], '>')
			if end != -1 {
				out.WriteString(markup[i : i+end+1])
				i += end + 1
				continue
			}
		}
		if markup[i] == '\n' {
			out.WriteByte('\n')
			i++
			continue
		}
		start := i
		for i < n && markup[i] != '<' && markup[i] != '\n' {
			i++
		}
		seg := markup[start:i]
		if !hasRTLOrFormatUTF8(seg) {
			out.WriteString(seg)
			continue
		}
		unesc := stdhtml.UnescapeString(seg)
		processed := processRTLInTextSpan(unesc, false)
		out.WriteString(stdhtml.EscapeString(processed))
	}
	return out.String()
}

// processRTLInTablePayload processes all cell text segments in an extracted table or infobox payload,
// respecting row breaks (\n), cell pipes (|), intra-cell line breaks (\n), cell prefixes ([H], [C..], [R..]),
// [IMG:...] markers, and <a href="...">...</a> tags.
func processRTLInTablePayload(payload string) string {
	if !hasRTLOrFormatUTF8(payload) {
		return payload
	}
	lines := strings.Split(payload, "\n")
	for li, line := range lines {
		if !hasRTLOrFormatUTF8(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "===") || strings.HasPrefix(trimmed, "[COLS:") {
			continue
		}
		lines[li] = processRTLInTableRow(line)
	}
	return strings.Join(lines, "\n")
}

func processRTLInTableRow(line string) string {
	// Split row by unescaped '|'
	var cells []string
	var cur strings.Builder
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			cur.WriteByte(c)
			escaped = false
		} else if c == '\\' {
			cur.WriteByte(c)
			escaped = true
		} else if c == '|' {
			cells = append(cells, cur.String())
			cur.Reset()
		} else {
			cur.WriteByte(c)
		}
	}
	cells = append(cells, cur.String())

	for ci, cell := range cells {
		if !hasRTLOrFormatUTF8(cell) {
			continue
		}
		cells[ci] = processRTLInSingleCell(cell)
	}
	return strings.Join(cells, "|")
}

func processRTLInSingleCell(cell string) string {
	// Preserve leading [H], [C<n>], [R<n>] prefixes
	prefixEnd := 0
	rem := cell
	for strings.HasPrefix(rem, "[H]") ||
		(strings.HasPrefix(rem, "[C") && strings.IndexByte(rem, ']') != -1) ||
		(strings.HasPrefix(rem, "[R") && strings.IndexByte(rem, ']') != -1) {
		idx := strings.IndexByte(rem, ']')
		prefixEnd += idx + 1
		rem = rem[idx+1:]
	}
	prefix := cell[:prefixEnd]
	body := cell[prefixEnd:]

	// Split cell body by literal `\n` line breaks
	subLines := strings.Split(body, `\n`)
	for si, sub := range subLines {
		if !hasRTLOrFormatUTF8(sub) {
			continue
		}
		subLines[si] = processRTLInCellLine(sub)
	}
	return prefix + strings.Join(subLines, `\n`)
}

func processRTLInCellLine(sub string) string {
	var out strings.Builder
	out.Grow(len(sub))
	n := len(sub)
	i := 0
	for i < n {
		if sub[i] == '<' {
			end := strings.IndexByte(sub[i:], '>')
			if end != -1 {
				out.WriteString(sub[i : i+end+1])
				i += end + 1
				continue
			}
		}
		if strings.HasPrefix(sub[i:], "[IMG:") {
			end := strings.IndexByte(sub[i:], ']')
			if end != -1 {
				out.WriteString(sub[i : i+end+1])
				i += end + 1
				continue
			}
		}
		start := i
		for i < n && sub[i] != '<' && !strings.HasPrefix(sub[i:], "[IMG:") {
			i++
		}
		seg := sub[start:i]
		if !hasRTLOrFormatUTF8(seg) {
			out.WriteString(seg)
			continue
		}
		// Unescape \| before HTML unescaping
		segUnpipe := strings.ReplaceAll(seg, `\|`, "|")
		unesc := stdhtml.UnescapeString(segUnpipe)
		processed := processRTLInTextSpan(unesc, true)
		escaped := stdhtml.EscapeString(processed)
		escaped = strings.ReplaceAll(escaped, "|", `\|`)
		out.WriteString(escaped)
	}
	return out.String()
}
