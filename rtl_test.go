package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"

	zim "github.com/akhenakh/gozim"
)

func TestArabicContextualShapingAndLigatures(t *testing.T) {
	// 1. Single word "مصر" (Miṣr = Egypt):
	// Logical: م (0645 Initial -> FEE3), ص (0635 Medial -> FEBC), ر (0631 Final -> FEAE)
	// Visual L-to-R order: ر (FEAE), ص (FEBC), م (FEE3)
	gotMisr := applyBidiVisual("مصر", 1)
	wantMisr := string([]rune{0xFEAE, 0xFEBC, 0xFEE3})
	if gotMisr != wantMisr {
		t.Fatalf("shape 'مصر': got %+X (%s), want %+X (%s)", []rune(gotMisr), gotMisr, []rune(wantMisr), wantMisr)
	}

	// 2. Mandatory Lam-Alef ligatures (Isolated and Final):
	// "لا" (Lam + Alef) -> Isolated Lam-Alef 0xFEFB
	gotLa := applyBidiVisual("لا", 1)
	if gotLa != string([]rune{0xFEFB}) {
		t.Fatalf("shape 'لا' isolated: got %+X, want [FEFB]", []rune(gotLa))
	}

	// "بلا" (Beh + Lam + Alef) -> Visual: Final Lam-Alef (0xFEFC), Initial Beh (0xFE91)
	gotBila := applyBidiVisual("بلا", 1)
	wantBila := string([]rune{0xFEFC, 0xFE91})
	if gotBila != wantBila {
		t.Fatalf("shape 'بلا' final Lam-Alef: got %+X, want %+X", []rune(gotBila), []rune(wantBila))
	}

	// "الإسلام" (Al-Islām):
	// Logical: ا (0627 Iso FE8D), ل+إ (Lam-Alef Hamming below Iso FEF9), س (0633 Init FEB3), ل+ا (Lam-Alef Final FEFC), م (0645 Iso FEE1)
	// Visual L-to-R: م (FEE1), لا-final (FEFC), س-init (FEB3), لإ-iso (FEF9), ا-iso (FE8D)
	gotIslam := applyBidiVisual("الإسلام", 1)
	wantIslam := string([]rune{0xFEE1, 0xFEFC, 0xFEB3, 0xFEF9, 0xFE8D})
	if gotIslam != wantIslam {
		t.Fatalf("shape 'الإسلام': got %+X, want %+X", []rune(gotIslam), []rune(wantIslam))
	}

	// "الآن" (Al-ʾĀn, with Lam-Alef Madda 0xFEF5):
	// Logical: ا (FE8D), ل+آ (FEF5), ن (FEE5) -> Visual: ن (FEE5), لآ (FEF5), ا (FE8D)
	gotAlan := applyBidiVisual("الآن", 1)
	wantAlan := string([]rune{0xFEE5, 0xFEF5, 0xFE8D})
	if gotAlan != wantAlan {
		t.Fatalf("shape 'الآن': got %+X, want %+X", []rune(gotAlan), []rune(wantAlan))
	}

	// "لأ" (Lam + Alef Hamza Above -> 0xFEF7) and "بلأ" (-> 0xFEF8, 0xFE91)
	if got := applyBidiVisual("لأ", 1); got != string([]rune{0xFEF7}) {
		t.Fatalf("shape 'لأ': got %+X, want [FEF7]", []rune(got))
	}
	if got := applyBidiVisual("بلأ", 1); got != string([]rune{0xFEF8, 0xFE91}) {
		t.Fatalf("shape 'بلأ': got %+X, want [FEF8 FE91]", []rune(got))
	}
}

func TestExtendedArabicPersianUrduZWNJ(t *testing.T) {
	// 1. Ensure 0x0671..0x06D5 (Extended Arabic letters) are NEVER classified as combining marks!
	extendedLetters := []rune{
		0x0671, // ٱ Alef Wasla
		0x0679, // ٹ Urdu Tteh
		0x067E, // پ Persian/Urdu Peh
		0x0686, // چ Persian/Urdu Tcheh
		0x0688, // ڈ Urdu Ddal
		0x0691, // ڑ Urdu Rreh
		0x0698, // ژ Persian/Urdu Jeh
		0x06A9, // ک Persian/Urdu Keheh
		0x06AF, // گ Persian/Urdu Gaf
		0x06BA, // ں Urdu Noon Ghunna
		0x06BE, // ھ Urdu Heh Doachashmee
		0x06C1, // ہ Urdu Heh Goal
		0x06CC, // ی Persian/Urdu Farsi Yeh
		0x06D2, // ے Urdu Yeh Barree
		0x06D5, // ە Kurdish Ae
	}
	for _, r := range extendedLetters {
		if isCombining(r) {
			t.Errorf("Extended Arabic letter U+%04X (%c) must NOT be classified as combining mark", r, r)
		}
	}

	// 2. Persian "فارسی" (Fārsī):
	// Logical: ف (0641 Init FED3), ا (0627 Final FE8E), ر (0631 Iso FEAD), س (0633 Init FEB3), ی (06CC Final FBFD)
	// Visual L-to-R: ی (FBFD), س (FEB3), ر (FEAD), ا (FE8E), ف (FED3)
	gotFarsi := applyBidiVisual("فارسی", 1)
	wantFarsi := string([]rune{0xFBFD, 0xFEB3, 0xFEAD, 0xFE8E, 0xFED3})
	if gotFarsi != wantFarsi {
		t.Fatalf("shape Persian 'فارسی': got %+X, want %+X", []rune(gotFarsi), []rune(wantFarsi))
	}

	// 3. Urdu "پاکستان" (Pākistān) with Peh (067E) and Keheh (06A9):
	// Logical: پ (Init FB58), ا (Final FE8E), ک (Init FB90), س (Med FEB4), ت (Med FE98), ا (Final FE8E), ن (Iso FEE5)
	// Visual L-to-R: ن (FEE5), ا (FE8E), ت (FE98), س (FEB4), ک (FB90), ا (FE8E), پ (FB58)
	gotPak := applyBidiVisual("پاکستان", 1)
	wantPak := string([]rune{0xFEE5, 0xFE8E, 0xFE98, 0xFEB4, 0xFB90, 0xFE8E, 0xFB58})
	if gotPak != wantPak {
		t.Fatalf("shape Urdu 'پاکستان': got %+X, want %+X", []rune(gotPak), []rune(wantPak))
	}

	// 4. Persian ZWNJ (U+200C) in "می‌شود" (mī-shavad):
	// Logical: م (0645 Init FEE3) + ی (06CC Final FBFD due to ZWNJ!) + ZWNJ + ش (0634 Init FEB7) + و (0648 Final FEEE) + د (062F Iso FEA9)
	// Without ZWNJ preservation, ی would become Medial (FBFF) and ش would become Medial (FEB8).
	// Visual L-to-R: د (FEA9), و (FEEE), ش (FEB7), ی (FBFD), م (FEE3)
	gotZWNJ := applyBidiVisual("می\u200cشود", 1)
	wantZWNJ := string([]rune{0xFEA9, 0xFEEE, 0xFEB7, 0xFBFD, 0xFEE3})
	if gotZWNJ != wantZWNJ {
		t.Fatalf("shape Persian ZWNJ 'می‌شود': got %+X, want %+X", []rune(gotZWNJ), []rune(wantZWNJ))
	}

	// 5. Persian ZWNJ in "راه‌آهن" (rāh-āhan = railway):
	// Logical: ر (FEAD), ا (FE8D), ه (0647 Iso FEE9 due to ZWNJ after non-left-joining Alef), ZWNJ, آ (0622 Iso FE81), ه (0647 Init FEEB), ن (0646 Final FEE6)
	// Visual L-to-R: ن (FEE6), ه (FEEB), آ (FE81), ه (FEE9), ا (FE8D), ر (FEAD)
	gotRailway := applyBidiVisual("راه\u200cآهن", 1)
	wantRailway := string([]rune{0xFEE6, 0xFEEB, 0xFE81, 0xFEE9, 0xFE8D, 0xFEAD})
	if gotRailway != wantRailway {
		t.Fatalf("shape Persian ZWNJ 'راه‌آهن': got %+X, want %+X", []rune(gotRailway), []rune(wantRailway))
	}
}

func TestVocalizedArabicAndHebrewMarks(t *testing.T) {
	// 1. Vocalized Arabic "مُحَمَّد" (Muḥammad):
	// Logical:
	//   م (0645 Init FEE3) + ُ (064F)
	//   ح (062D Med FEA4)  + َ (064E)
	//   م (0645 Med FEE4)  + َ (064E) + ّ (0651)
	//   د (062F Final FEAA)
	// Visual L-to-R with Rule L3 (each base glyph followed by its logical marks):
	//   د (FEAA), م (FEE4) + َ (064E) + ّ (0651), ح (FEA4) + َ (064E), م (FEE3) + ُ (064F)
	gotMuhammad := applyBidiVisual("مُحَم\u064e\u0651د", 1)
	wantMuhammad := string([]rune{
		0xFEAA,
		0xFEE4, 0x064E, 0x0651,
		0xFEA4, 0x064E,
		0xFEE3, 0x064F,
	})
	if gotMuhammad != wantMuhammad {
		t.Fatalf("vocalized Arabic 'مُحَمَّد': got %+X, want %+X", []rune(gotMuhammad), []rune(wantMuhammad))
	}

	// 2. Vocalized Hebrew "מִצְרַיִם" (Mitzrayim = Egypt):
	// Logical:
	//   מ (05DE) + ִ (05B4)
	//   צ (05E6) + ְ (05B0)
	//   ר (05E8) + ַ (05B7)
	//   י (05D9) + ִ (05B4)
	//   ם (05DD)
	// Visual L-to-R with Rule L3:
	//   ם (05DD), י (05D9)+ִ (05B4), ר (05E8)+ַ (05B7), צ (05E6)+ְ (05B0), מ (05DE)+ִ (05B4)
	gotHebrew := applyBidiVisual("מִצְרַיִם", 1)
	wantHebrew := string([]rune{
		0x05DD,
		0x05D9, 0x05B4,
		0x05E8, 0x05B7,
		0x05E6, 0x05B0,
		0x05DE, 0x05B4,
	})
	if gotHebrew != wantHebrew {
		t.Fatalf("vocalized Hebrew 'מִצְרַיִם': got %+X, want %+X", []rune(gotHebrew), []rune(wantHebrew))
	}
}

func TestUAX9BidiNumbersAndMirroring(t *testing.T) {
	// 1. Multi-word RTL phrase with embedded number: "عام 1948 م"
	// Logical words: "عام" (first), "1948" (second), "م" (third)
	// Visual L-to-R order: "م" on left, "1948" (digits in LTR order!) in middle, "عام" on right
	gotMixed := applyBidiVisual("عام 1948 م", 1)
	wantMixed := string([]rune{
		0xFEE1, ' ', // م
		'1', '9', '4', '8', ' ', // 1948 (LTR!)
		0xFEE1, 0xFE8E, 0xFECB, // عام (ع Init FECB, ا Final FE8E, م Iso FEE1)
	})
	if gotMixed != wantMixed {
		t.Fatalf("RTL with embedded number 'عام 1948 م': got %+X (%s), want %+X (%s)",
			[]rune(gotMixed), gotMixed, []rune(wantMixed), wantMixed)
	}

	// 2. Bracket mirroring (UAX#9 Rule L4) in RTL context: "(مصر)"
	// Logical: '(' + مصر + ')'
	// In RTL visual order from left to right: '(' on the left, shaped مصر in the middle, ')' on the right!
	// (Because logical '(' is on the right and mirrors to ')', while logical ')' is on the left and mirrors to '('.)
	gotParen := applyBidiVisual("(مصر)", 1)
	wantParen := "(" + string([]rune{0xFEAE, 0xFEBC, 0xFEE3}) + ")"
	if gotParen != wantParen {
		t.Fatalf("bracket mirroring '(مصر)': got %q (%+X), want %q (%+X)",
			gotParen, []rune(gotParen), wantParen, []rune(wantParen))
	}
}

func TestParsoidRTLExtractionStandards(t *testing.T) {
	// Construct a realistic Parsoid v2.8.0 article snippet covering all 6 Parsoid RTL patterns:
	// 1. <span lang="ar" dir="rtl">جمهورية مصر العربية</span>
	// 2. <span lang="fa">فارسی</span> + romanization <i lang="fa-Latn">Fārsī</i> (no dir="rtl" attribute)
	// 3. Multi-tag Wiktionary interwiki links inside <span lang="fa"><a>لغت</a> <a>انجمن</a> <a>علمی</a></span>
	// 4. Bare RTL in parentheses: (מִצְרַיִם / מִצְרָיִם)
	// 5. Infobox (<table class="infobox">) with Nastaleeq Urdu name and Arabic motto
	// 6. Wikitable (<table class="wikitable">) with RTL cells
	parsoidHTML := []byte(`<!DOCTYPE html><html><body><div class="mw-parser-output">
		<table class="infobox ib-country vcard"><tbody>
			<tr><th colspan="2" class="infobox-above"><span lang="ur" style="font-family: 'Nafees Nastaleeq';">اسلامی جمہوریہ پاكستان</span></th></tr>
			<tr><th scope="row">Motto</th><td><span lang="ur">ایمان، اتحاد، نظم</span></td></tr>
		</tbody></table>
		<p><b>Egypt</b> (Arabic: <span lang="ar" dir="rtl">جمهورية مصر العربية</span>; Hebrew: (מִצְרַיִם)) is a country.</p>
		<p><b>Persian</b> (<span lang="fa">فارسی</span>, <i lang="fa-Latn">Fārsī</i>) has the dictionary (<span lang="fa"><a rel="mw:WikiLink/Interwiki" href="https://en.wiktionary.org/wiki/لغت" class="extiw">لغت</a> <a rel="mw:WikiLink/Interwiki" href="https://en.wiktionary.org/wiki/انجمن" class="extiw">انجمن</a> <a rel="mw:WikiLink/Interwiki" href="https://en.wiktionary.org/wiki/علمی" class="extiw">علمی</a></span>).</p>
		<table class="wikitable"><tbody>
			<tr><th>Language</th><th>Native name</th></tr>
			<tr><td>Arabic</td><td><span lang="ar" dir="rtl">العربية</span></td></tr>
		</tbody></table>
	</div></body></html>`)

	extracted := extractHTML(parsoidHTML, true)

	// Verify no raw Bidi isolate markers remain in the extracted text
	if strings.ContainsAny(extracted, "\u2066\u2067\u2068\u2069\u200c\u200d\u200e\u200f") {
		t.Fatalf("extracted output still contains raw invisible Bidi/joiner format chars:\n%s", extracted)
	}

	// 1. Check that "جمهورية مصر العربية" was shaped and visually ordered as a single 3-word phrase
	// ("العربية" on left, "مصر" in middle, "جمهورية" on right)
	wantEgyptAr := applyBidiVisual("جمهورية مصر العربية", 1)
	if !strings.Contains(extracted, wantEgyptAr) {
		t.Errorf("expected shaped 'جمهورية مصر العربية' (%q) in body, got:\n%s", wantEgyptAr, extracted)
	}

	// 2. Check that <span lang="fa">فارسی</span> (without dir="rtl") was shaped and <i lang="fa-Latn">Fārsī</i> remained LTR
	wantFarsi := applyBidiVisual("فارسی", 1)
	if !strings.Contains(extracted, wantFarsi) {
		t.Errorf("expected shaped 'فارسی' (%q) in body, got:\n%s", wantFarsi, extracted)
	}
	if !strings.Contains(extracted, "<i>Fārsī</i>") {
		t.Errorf("expected LTR romanization '<i>Fārsī</i>' preserved in body, got:\n%s", extracted)
	}

	// 3. Check that the 3 sibling Wiktionary links "لغت انجمن علمی" were merged into a single RTL phrase
	// ("علمی" on left, "انجمن" in middle, "لغت" on right)
	wantDict := applyBidiVisual("لغت انجمن علمی", 1)
	if !strings.Contains(extracted, wantDict) {
		t.Errorf("expected multi-tag Persian phrase 'لغت انجمن علمی' merged & shaped as %q, got:\n%s", wantDict, extracted)
	}

	// 4. Check that bare vocalized Hebrew "(מִצְרַיִם)" was visually ordered with L3 mark placement
	wantHeb := "(" + applyBidiVisual("מִצְרַיִם", 1) + ")"
	if !strings.Contains(extracted, wantHeb) {
		t.Errorf("expected bare Hebrew '(מִצְרַיִם)' ordered as %q, got:\n%s", wantHeb, extracted)
	}

	// 5. Check that Infobox Urdu header and motto were shaped & ordered inside ===INFOBOX===
	wantPakMotto := applyBidiVisual("ایمان، اتحاد، نظم", 1)
	if !strings.Contains(extracted, wantPakMotto) {
		t.Errorf("expected Infobox Urdu motto shaped as %q, got:\n%s", wantPakMotto, extracted)
	}

	// 6. Check that Wikitable Arabic cell "العربية" was shaped inside ===TABLE:1===
	wantAlArabiya := applyBidiVisual("العربية", 1)
	if !strings.Contains(extracted, "Arabic|"+wantAlArabiya) {
		t.Errorf("expected Wikitable cell 'Arabic|%s', got:\n%s", wantAlArabiya, extracted)
	}
}

func loadFallbackFontCodepoints(t *testing.T) ([]byte, int) {
	t.Helper()
	data, err := os.ReadFile(`font-production/fallback_noto.bin`)
	if err != nil {
		t.Skipf("fallback_noto.bin not found: %v", err)
	}
	if len(data) < 16 || string(data[:4]) != "FBF1" {
		t.Fatalf("invalid fallback_noto.bin header")
	}
	bytesPerGlyph := int(binary.LittleEndian.Uint16(data[6:8]))
	return data, bytesPerGlyph
}

func hasFallbackGlyph(fontData []byte, bytesPerGlyph int, cp rune) bool {
	off := 16 + int(cp)*bytesPerGlyph
	if off < 16 || off+bytesPerGlyph > len(fontData) {
		return false
	}
	for _, b := range fontData[off : off+bytesPerGlyph] {
		if b != 0 {
			return true
		}
	}
	return false
}

func TestRealZimArticlesRTLAndFontCoverage(t *testing.T) {
	zimPath := os.Getenv("TEST_ZIM_PATH")
	if zimPath == "" {
		t.Skip("TEST_ZIM_PATH not set")
	}
	zr, err := zim.NewReader(zimPath, false)
	if err != nil {
		t.Skipf("ZIM not found: %v", err)
	}
	defer zr.Close()

	fontData, bytesPerGlyph := loadFallbackFontCodepoints(t)

	articles := []string{
		"C/Egypt",
		"C/Iran",
		"C/Israel",
		"C/Pakistan",
		"C/Arabic",
		"C/Persian_language",
		"C/Hebrew_language",
		"C/Urdu",
		"C/Muhammad",
		"C/Jerusalem",
	}

	totalRTLRunes := 0
	missingCPs := make(map[rune]int)

	for _, url := range articles {
		art, err := zr.GetPageNoIndex(url)
		if err != nil || art == nil {
			t.Fatalf("failed to load %s from ZIM: %v", url, err)
		}
		rawHTML, err := art.Data()
		if err != nil {
			t.Fatalf("failed to read %s data: %v", url, err)
		}

		extracted := extractHTML(rawHTML, true)
		if extracted == "" {
			t.Fatalf("empty extraction for %s", url)
		}

		// Verify no raw invisible Bidi/joiner controls remain
		for _, r := range extracted {
			if (r >= 0x200B && r <= 0x200F) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2060 && r <= 0x206F) || r == 0xFEFF {
				t.Errorf("%s still contains raw invisible format rune U+%04X", url, r)
			}
			if isRTLScriptRune(r) {
				totalRTLRunes++
				if !hasFallbackGlyph(fontData, bytesPerGlyph, r) {
					missingCPs[r]++
				}
			}
		}
	}

	fmt.Printf("Verified %d shaped RTL glyphs across %d major Wikipedia articles; missing from fallback_noto.bin: %d\n",
		totalRTLRunes, len(articles), len(missingCPs))
	for cp, cnt := range missingCPs {
		fmt.Printf("  Missing CP U+%04X (%c): %d occurrences\n", cp, cp, cnt)
	}
	if totalRTLRunes < 500 {
		t.Errorf("expected at least 500 RTL runes across the 10 articles, got %d", totalRTLRunes)
	}
}
