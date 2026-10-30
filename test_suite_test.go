package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	zim "github.com/akhenakh/gozim"
)

func TestReadRandomArticle(t *testing.T) {
	app := &App{}
	imgPath := os.Getenv("TEST_IMG_PATH")
	if imgPath == "" {
		t.Skip("TEST_IMG_PATH not set")
	}
	res, err := app.TestRandomArticle(imgPath)
	if err != nil {
		t.Fatalf("TestRandomArticle failed: %v", err)
	}
	fmt.Printf("Title: %s\nContent starts with: %.100s\n", res.Title, res.Content)
}

func TestMathJaxFlattening(t *testing.T) {
	svgData, err := os.ReadFile("MathJaxFlatten/MathJaxExample.svg")
	if err != nil {
		t.Fatalf("Could not read test SVG: %v", err)
	}

	// Call the internal image processor
	outBytes := processImageBytes(svgData, 4, "image/svg+xml", "floydsteinberg", "png")
	if len(outBytes) < 100 {
		t.Fatalf("Output bytes too small, something failed.")
	}

	os.WriteFile("test_mathjax_out.png", outBytes, 0644)
	fmt.Println("Test completed, wrote test_mathjax_out.png")
}

func TestCladeProcessing(t *testing.T) {
	cladeHTML, err := os.ReadFile("html-examples/clade.txt")
	if err != nil {
		t.Skipf("clade.txt not found: %v", err)
	}

	result := extractHTMLFallback(cladeHTML)

	if !strings.Contains(result, "[CLADE:1]") {
		t.Errorf("Expected [CLADE:1] link placeholder in text, got: %.200s", result)
	}
	if !strings.Contains(result, "===CLADE:1===") {
		t.Errorf("Expected ===CLADE:1=== table payload header in text, got: %.200s", result)
	}
	if strings.Contains(result, "TemplateStyles") || strings.Contains(result, ".mw-parser-output") {
		t.Errorf("Raw CSS style leaked into text payload!")
	}
	if !strings.Contains(result, "Laurasiatheria") {
		t.Errorf("Expected Laurasiatheria root clade")
	}
	if !strings.Contains(result, "c. 99 Mya") {
		t.Errorf("Expected c. 99 Mya divergence date")
	}
	if !strings.Contains(result, "├──") || !strings.Contains(result, "└──") {
		t.Errorf("Expected box-drawing characters in dendrogram")
	}
	if !strings.Contains(result, "[IMG:") {
		t.Errorf("Expected image tags in cladogram table")
	}

	fmt.Printf("Clade test passed! Output:\n%s\n", result)
}

func TestLookupWhale(t *testing.T) {
	app := &App{}
	imgPath := os.Getenv("TEST_IMG_PATH")
	if imgPath == "" {
		t.Skip("TEST_IMG_PATH not set")
	}
	res, err := app.TestLookupArticle(imgPath, "Whale")
	if err != nil {
		t.Fatalf("TestLookupArticle failed: %v", err)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Whale content length: %d\n", len(res.Content)))
	lines := strings.Split(res.Content, "\n")
	for _, l := range lines {
		if strings.Contains(l, "[TABLE:") || strings.Contains(l, "[CLADE:") || strings.Contains(l, "===TABLE:") || strings.Contains(l, "===CLADE:") || strings.Contains(l, "Laurasiatheria") || strings.Contains(l, "INFOBOX") {
			sb.WriteString(fmt.Sprintf("MATCH: %s\n", l))
		}
	}
	os.WriteFile("out_whale.txt", []byte(sb.String()), 0644)
}

func TestZimWhaleHTML(t *testing.T) {
	zimPath := os.Getenv("TEST_ZIM_PATH")
	if zimPath == "" {
		t.Skip("TEST_ZIM_PATH not set")
	}
	zr, err := zim.NewReader(zimPath, false)
	if err != nil {
		t.Fatalf("Failed to open ZIM: %v", err)
	}
	defer zr.Close()

	var targetArt *zim.Article
	for i := uint32(0); i < zr.ArticleCount; i++ {
		a, err := zr.ArticleAtURLIdx(i)
		if err == nil {
			title := a.Title
			url := a.FullURL()
			if strings.EqualFold(title, "Whale") || strings.HasSuffix(strings.ToLower(url), "/whale") || strings.HasSuffix(strings.ToLower(url), "/whale.html") || url == "Whale" {
				t.Logf("Candidate: Title=%q, URL=%q, Type=%d\n", title, url, a.EntryType)
				if a.EntryType != zim.RedirectEntry && a.EntryType != zim.LinkTargetEntry {
					targetArt = a
					break
				}
			}
		}
	}
	if targetArt == nil {
		t.Fatalf("Whale article not found in ZIM")
	}
	data, err := targetArt.Data()
	if err != nil {
		t.Fatalf("Failed to get article data: %v", err)
	}
	t.Logf("Found Whale article! Data len: %d\n", len(data))
	rawHTML := string(data)
	offset := 0
	for {
		idx := strings.Index(rawHTML[offset:], "Laurasiatheria")
		if idx == -1 {
			break
		}
		realIdx := offset + idx
		start := realIdx - 500
		if start < 0 { start = 0 }
		end := realIdx + 700
		if end > len(rawHTML) { end = len(rawHTML) }
		t.Logf("Occurrence at %d:\n%s\n", realIdx, rawHTML[start:end])
		offset = realIdx + 14
	}
	extracted := extractHTML(data)
	lines := strings.Split(extracted, "\n")
	printLines := false
	for _, l := range lines {
		if strings.HasPrefix(l, "===CLADE:1===") {
			printLines = true
		}
		if printLines {
			t.Logf("CLADE LINE: %s\n", l)
			if strings.HasPrefix(l, "===END===") {
				printLines = false
			}
		}
	}
}

func TestConvertAndVerifyWhale(t *testing.T) {
	zimPath := os.Getenv("TEST_ZIM_PATH")
	outPath := os.Getenv("TEST_IMG_PATH")
	if zimPath == "" || outPath == "" {
		t.Skip("TEST_ZIM_PATH / TEST_IMG_PATH not set")
	}

	outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to open output img: %v", err)
	}
	defer outFile.Close()

	ctx := context.Background()
	idxSec, chunkSec, fontSec, err := ProcessZIM(ctx, zimPath, outFile, 0, true, 1, "floydsteinberg", "jpeg")
	if err != nil {
		t.Fatalf("ProcessZIM failed: %v", err)
	}
	outFile.Sync()
	outFile.Close()

	t.Logf("ProcessZIM finished! idxSec=%d, chunkSec=%d, fontSec=%d\n", idxSec, chunkSec, fontSec)
	if fontSec == 0 {
		t.Errorf("Expected fontSec > 0, got 0!")
	}

	app := &App{}
	res, err := app.TestLookupArticle(outPath, "Whale")
	if err != nil {
		t.Fatalf("TestLookupArticle Whale failed: %v", err)
	}

	if !strings.Contains(res.Content, "[CLADE:1]") {
		t.Errorf("Expected [CLADE:1] in Whale article text!")
	}
	if !strings.Contains(res.Content, "===CLADE:1===") {
		t.Errorf("Expected ===CLADE:1=== in Whale article payload!")
	}
	if !strings.Contains(res.Content, "├──") || !strings.Contains(res.Content, "└──") {
		t.Errorf("Expected box drawing characters in Whale clade payload!")
	}
	t.Logf("Verification succeeded! Whale has [CLADE:1] and ===CLADE:1=== with box drawing characters.")
}

func TestMathFormulaExtraction(t *testing.T) {
	htmlSample := []byte(`
		<div id="mw-content-text">
			<p>Radiative forcing equation:
			<span class="mwe-math-element">
				<span class="mwe-math-mathml-inline" style="display: none;">
					<math xmlns="http://www.w3.org/1998/Math/MathML">
						Delta F = 5.35 * ln(C/C0)
						<annotation encoding="application/x-tex">{\displaystyle \Delta F=5.35\ln(C/C_{0})}</annotation>
					</math>
				</span>
				<img class="mwe-math-fallback-image-inline" src="math_eq_1.svg">
			</span>
			where C0 is reference concentration.</p>
		</div>
	`)

	// 1. With images enabled:
	withImages := extractHTML(htmlSample, true)
	if strings.Contains(withImages, `\displaystyle`) {
		t.Errorf("Expected LaTeX syntax to be stripped when images are on, got: %s", withImages)
	}
	if strings.Contains(withImages, "Delta F =") {
		t.Errorf("Expected MathML text to be stripped when images are on, got: %s", withImages)
	}
	if !strings.Contains(withImages, "[IMG:") {
		t.Errorf("Expected [IMG:...] equation image marker when images are on, got: %s", withImages)
	}

	// 2. Without images:
	noImages := extractHTML(htmlSample, false)
	if strings.Contains(noImages, `\displaystyle`) {
		t.Errorf("Expected LaTeX syntax to be stripped in no-images mode, got: %s", noImages)
	}
	if !strings.Contains(noImages, "Delta F =") {
		t.Errorf("Expected clean text formula in no-images mode, got: %s", noImages)
	}
}

func TestLinkAndTableExtraction(t *testing.T) {
	htmlSample := []byte(`
		<div id="mw-content-text">
			<p>See <a href="/wiki/Global_warming#Impacts">global warming</a> for details.</p>
			<table>
				<tr><th>Year</th><th>Baseline</th><th>Change</th><th>Forcing</th></tr>
				<tr><td>1979–1989</td><td>336.8</td><td>+16.0</td><td>+0.248</td></tr>
			</table>
		</div>
	`)

	extracted := extractHTML(htmlSample, true)
	if !strings.Contains(extracted, `<a href="Global_warming">global warming</a>`) {
		t.Errorf("Expected link '<a href=\"Global_warming\">global warming</a>', got: %s", extracted)
	}
	if !strings.Contains(extracted, "[COLS:") {
		t.Errorf("Expected '[COLS:...]' in table payload, got: %s", extracted)
	}
	t.Logf("Extracted result:\n%s", extracted)
}

// articleBody returns the article text before any appended ===...=== payload blocks.
func articleBody(s string) string {
	if idx := strings.Index(s, "\n==="); idx != -1 {
		return s[:idx]
	}
	return s
}

func TestParsoidMathKeepsFollowingText(t *testing.T) {
	htmlSample := []byte(`<html><body><div class="mw-content-ltr mw-parser-output"><section data-mw-section-id="0">
		<p>Intro paragraph.</p>
		<p><span class="mwe-math-element"><span class="mwe-math-mathml-inline mwe-math-mathml-a11y" style="display: none;"><math><mi>λ</mi><annotation encoding="application/x-tex">\lambda</annotation></math></span><img src="./_assets_/x/math1.svg" class="mwe-math-fallback-image-inline"></span></p>
		<p>Paragraph after math.</p>
		<h2>Section 2</h2><p>More text with <span class="mwe-math-element"><span class="mwe-math-mathml-inline"><math><mi>π</mi></math></span><img src="./_assets_/x/math2.svg" class="mwe-math-fallback-image-inline" width="10" height="12"></span> inline.</p>
		<table class="wikitable"><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>
		<p>Final paragraph.</p>
	</section></div></body></html>`)

	out := extractHTML(htmlSample, true)
	body := articleBody(out)
	for _, want := range []string{"Intro paragraph.", "Paragraph after math.", "Section 2", "More text with", "inline.", "Final paragraph.", "[TABLE:1]"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in article body, got:\n%s", want, body)
		}
	}
	if n := strings.Count(body, "[IMG:"); n != 2 {
		t.Errorf("expected 2 math images (tiny math images must not be skipped), got %d:\n%s", n, body)
	}
	if strings.Contains(body, `\lambda`) {
		t.Errorf("LaTeX annotation leaked into body:\n%s", body)
	}
}

func TestParsoidSidebarAndInfoboxMerge(t *testing.T) {
	htmlSample := []byte(`<html><body><div class="mw-content-ltr mw-parser-output"><section data-mw-section-id="0">
		<div role="note" class="hatnote navigation-not-searchable">For other uses, see Market.</div>
		<table class="sidebar sidebar-collapse nomobile nowraplinks vlist; align:left;" role="navigation"><tbody>
			<tr><td class="sidebar-pretitle">Part of a series on</td></tr>
			<tr><th class="sidebar-title-with-pretitle"><a rel="mw:WikiLink" href="Economics">Economics</a></th></tr>
			<tr><td class="sidebar-image"><span class="notpageimage"><img src="./_assets_/x/Supply-demand-equilibrium.svg.png" width="120" height="120"></span></td></tr>
			<tr><td class="sidebar-above"><span class="noviewer sister-inline-image"><img src="./_assets_/x/Wikibooks-logo.svg.png" width="16" height="16"></span> <a rel="mw:WikiLink/Interwiki" href="https://en.wikibooks.org/wiki/Principles" class="extiw external">Principles of Economics</a></td></tr>
			<tr><td class="sidebar-content">
				<div class="sidebar-list mw-collapsible mw-collapsed"><div class="sidebar-list-title">Tools and <a rel="mw:WikiLink" href="Economic_methodology">methodology</a></div><div class="sidebar-list-content mw-collapsible-content"><ul><li><a rel="mw:WikiLink" href="Game_theory">Game theory</a></li><li><a rel="mw:WikiLink" href="Econometrics">Econometrics</a></li></ul></div></div>
				<div class="sidebar-list mw-collapsible mw-collapsed"><div class="sidebar-list-title">Branches</div><div class="sidebar-list-content"><ul><li><a rel="mw:WikiLink" href="Microeconomics">Micro</a></li></ul></div></div>
			</td></tr>
		</tbody></table>
		<table class="sidebar sidebar-collapse nomobile nowraplinks" role="navigation"><tr><th class="sidebar-title">Capitalism</th></tr></table>
		<p>A market is a composition of systems.</p>
		<table class="wikitable"><tr><th>X</th><th>Y</th></tr><tr><td>1</td><td>2</td></tr></table>
	</section></div></body></html>`)

	out := extractHTML(htmlSample, true)
	body := articleBody(out)
	if strings.Count(out, "[INFOBOX]") != 1 {
		t.Errorf("expected exactly one [INFOBOX] marker, got:\n%s", out)
	}
	if strings.Count(out, "===INFOBOX===") != 1 {
		t.Errorf("expected a single merged ===INFOBOX=== block, got:\n%s", out)
	}
	if !strings.Contains(body, "[TABLE:1]") || strings.Contains(out, "[TABLE:2]") {
		t.Errorf("sidebars must not consume table numbers; expected only [TABLE:1], got:\n%s", out)
	}
	if !strings.Contains(body, "For other uses, see Market.") {
		t.Errorf("hatnote should be kept inline, got:\n%s", body)
	}
	if !strings.Contains(out, `[H]Tools and <a href="Economic_methodology">methodology</a>|`) {
		t.Errorf("expected sidebar section title row with pipe, got:\n%s", out)
	}
	if !strings.Contains(out, `<a href="Game_theory">Game theory</a>`) {
		t.Errorf("expected Game theory link in sidebar content, got:\n%s", out)
	}
	if !strings.Contains(out, "[H]Branches|") {
		t.Errorf("expected second sidebar section, got:\n%s", out)
	}
	if !strings.Contains(out, "Capitalism") {
		t.Errorf("expected second sidebar merged into infobox payload, got:\n%s", out)
	}
	if strings.Contains(out, "wikibooks") || strings.Contains(out, hashImageKey("Wikibooks-logo.svg.png")) {
		t.Errorf("interwiki link / 16px noviewer icon should be dropped, got:\n%s", out)
	}
	if !strings.Contains(out, "Principles of Economics") {
		t.Errorf("text of excluded link should remain as plain text, got:\n%s", out)
	}
	if !strings.Contains(out, hashImageKey("Supply-demand-equilibrium.svg.png")) {
		t.Errorf("expected sidebar main image to be kept, got:\n%s", out)
	}
}

func TestParsoidStripsNavboxesAndMaintenanceBoxes(t *testing.T) {
	htmlSample := []byte(`<html><body><div class="mw-content-ltr mw-parser-output">
		<table class="box-Unreferenced plainlinks metadata ambox ambox-content"><tr><td>This article needs citations.</td></tr></table>
		<p>Body text.</p>
		<div class="side-box side-box-right sistersitebox"><div class="side-box-text">Wikiquote has quotations</div></div>
		<div class="navbox-styles"><style>.x{}</style></div>
		<div role="navigation" class="navbox"><table class="nowraplinks vcard hlist mw-collapsible autocollapse navbox-inner"><tr><th>The Beatles</th></tr><tr><td><a rel="mw:WikiLink" href="John_Lennon">John</a></td></tr></table></div>
		<div class="navbox authority-control"><table class="nowraplinks hlist navbox-inner"><tr><td>VIAF</td></tr></table></div>
		<p>Closing text.</p>
	</div><div class="catlinks">Categories: Foo</div></body></html>`)

	out := extractHTML(htmlSample, true)
	for _, bad := range []string{"[INFOBOX]", "[TABLE:", "needs citations", "Wikiquote", "The Beatles", "John", "VIAF", "Categories"} {
		if strings.Contains(out, bad) {
			t.Errorf("did not expect %q in output, got:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "Body text.") || !strings.Contains(out, "Closing text.") {
		t.Errorf("expected body text to survive, got:\n%s", out)
	}
}

func TestParsoidUnwrapsLayoutTablesAndFlattensLegends(t *testing.T) {
	htmlSample := []byte(`<html><body><div class="mw-parser-output">
		<p>Cast:</p>
		<table class="col-begin" role="presentation"><tr><td><ul><li>Item A</li></ul></td><td><ul><li>Item B</li></ul></td></tr></table>
		<table id="mwXg"><tbody><tr><td style="background:#fc0"></td><td>= Indicates team leader</td></tr></tbody></table>
		<table style="width:70%"><tr><th>Player</th><th>Round</th></tr><tr><td>Bob</td><td>1</td></tr></table>
	</div></body></html>`)

	out := extractHTML(htmlSample, true)
	body := articleBody(out)
	if !strings.Contains(body, "Item A") || !strings.Contains(body, "Item B") {
		t.Errorf("layout table lists should flow inline, got:\n%s", body)
	}
	if !strings.Contains(body, "= Indicates team leader") {
		t.Errorf("single-row unclassed legend should be flattened inline, got:\n%s", body)
	}
	if !strings.Contains(body, "[TABLE:1]") || strings.Contains(out, "[TABLE:2]") {
		t.Errorf("expected only the multi-row unclassed table as [TABLE:1], got:\n%s", out)
	}
}

func TestParsoidLinkAndIconFiltering(t *testing.T) {
	htmlSample := []byte(`<html><body><div class="mw-parser-output"><p>
		<a rel="mw:ExtLink nofollow" href="https://example.com" class="external text">External</a>
		<a rel="mw:WikiLink" href="Market_failure" class="new">Redlink</a>
		<a rel="mw:WikiLink" href="Category%3AEconomics">CatLink</a>
		<a rel="mw:WikiLink" href="Supply_and_demand#History">Supply</a>
		<span class="noviewer"><img src="./_assets_/x/icon.png" width="16" height="16"></span>
		<img src="./_assets_/x/Lock-green.svg.png" width="9" height="14">
		<img src="./_assets_/x/Photo.jpg" width="220" height="160">
	</p></div></body></html>`)

	out := extractHTML(htmlSample, true)
	if !strings.Contains(out, `<a href="Supply_and_demand">Supply</a>`) {
		t.Errorf("expected internal link kept, got:\n%s", out)
	}
	if strings.Contains(out, `href="https://example.com"`) || strings.Contains(out, `href="Market_failure"`) || strings.Contains(out, `href="Category:Economics"`) {
		t.Errorf("external/red/namespace links should not be clickable, got:\n%s", out)
	}
	for _, txt := range []string{"External", "Redlink", "CatLink"} {
		if !strings.Contains(out, txt) {
			t.Errorf("expected plain text %q to remain, got:\n%s", txt, out)
		}
	}
	if n := strings.Count(out, "[IMG:"); n != 1 || !strings.Contains(out, hashImageKey("Photo.jpg")) {
		t.Errorf("expected only Photo.jpg image, got %d images:\n%s", n, out)
	}
}

func TestBTreeHasCalendarDates(t *testing.T) {
	sparse := NewBTree()
	sparse.Insert([]byte("taj mahal"), 100)
	sparse.Insert([]byte("february 25"), 200)
	sparse.Insert([]byte("june 6"), 300)
	if sparse.HasCalendarDates() {
		t.Fatalf("expected sparse top_maxi-style BTree (only 2 date articles) to return HasCalendarDates() == false")
	}

	fullUS := NewBTree()
	for i := 0; i < 300; i++ {
		fullUS.Insert([]byte("article_"+strings.Repeat("x", i%17)+string(rune('a'+(i%26)))), uint64(i+1))
	}
	for _, d := range []string{
		"january 15", "february 15", "march 19", "april 15", "may 12", "june 15",
		"july 22", "august 15", "september 18", "october 15", "november 14", "december 15",
	} {
		fullUS.Insert([]byte(d), 999)
	}
	if !fullUS.HasCalendarDates() {
		t.Fatalf("expected full US-format calendar BTree across node splits to return HasCalendarDates() == true")
	}

	fullUK := NewBTree()
	for _, d := range []string{
		"15 january", "15 february", "19 march", "15 april", "12 may", "15 june",
		"22 july", "15 august", "18 september", "15 october", "14 november", "15 december",
	} {
		fullUK.Insert([]byte(d), 888)
	}
	if !fullUK.HasCalendarDates() {
		t.Fatalf("expected full day-first calendar BTree to return HasCalendarDates() == true")
	}
}



