package app

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/batch"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
	builtin "dalibre/plugins"
)

type importSample struct {
	title, author, publisher, language, isbn, translator string
}

func sampleEPUB(t *testing.T, dir string, book importSample) string {
	t.Helper()
	name := book.title
	if book.author != "" {
		name += " - " + book.author
	}
	file := filepath.Join(dir, name+".epub")
	var body bytes.Buffer
	z := zip.NewWriter(&body)
	add := func(name, content string, method uint16) {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	field := func(tag, value string) string {
		if value == "" {
			return ""
		}
		var escaped bytes.Buffer
		xml.EscapeText(&escaped, []byte(value))
		return "<" + tag + ">" + escaped.String() + "</" + tag + ">"
	}
	translator := strings.Replace(field("dc:creator", book.translator), "<dc:creator>", "<dc:creator opf:role=\"trl\">", 1)
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate)
	add("content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf"><dc:identifier id="uid">urn:calibreno:acceptance</dc:identifier>`+field("dc:title", book.title)+field("dc:creator", book.author)+field("dc:publisher", book.publisher)+field("dc:language", book.language)+field("dc:identifier", "urn:isbn:"+book.isbn)+translator+`</metadata><manifest><item id="c" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`, zip.Deflate)
	add("chapter.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Sample</title></head><body><p>calibreno acceptance sample</p></body></html>`, zip.Deflate)
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// samplePDF writes a readable one-page PDF with a valid cross-reference table.
func samplePDF(t *testing.T, dir, name, text string) string {
	t.Helper()
	stream := "BT /F1 14 Tf 40 760 Td (" + text + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var file bytes.Buffer
	file.WriteString("%PDF-1.4\n")
	var offsets []int
	for i, object := range objects {
		offsets = append(offsets, file.Len())
		fmt.Fprintf(&file, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := file.Len()
	fmt.Fprintf(&file, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&file, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&file, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	path := filepath.Join(dir, name+".pdf")
	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBatchTwelveFiles(t *testing.T) {
	loc := "zh-Hans"
	i18n.SetLocaleFunc(func() string { return loc })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	// Opt in with the directory of a previous acceptance run. Reuse its
	// source files byte for byte and give the real plugin a fresh library.
	liveSamples := os.Getenv("CALIBRENO_NEODB_SAMPLE_DIR")
	live := liveSamples != ""
	for _, metadataFirst := range []bool{false, true} {
		if live && metadataFirst {
			continue
		}
		t.Run(fmt.Sprint("metadata-first=", metadataFirst), func(t *testing.T) {
			root := t.TempDir()
			if base := os.Getenv("CALIBRENO_ACCEPTANCE_DIR"); base != "" && !metadataFirst {
				var err error
				root, err = os.MkdirTemp(base, "run-")
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("persistent acceptance library and samples: %s", root)
			}
			input, seed := filepath.Join(root, "阅读资料"), filepath.Join(root, "Seed")
			for _, dir := range []string{input, seed} {
				if live {
					if err := os.CopyFS(dir, os.DirFS(filepath.Join(liveSamples, filepath.Base(dir)))); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			library := filepath.Join(root, "个人书库")
			lib, err := calibre.Create(library)
			if err != nil {
				t.Fatal(err)
			}
			dune := importSample{"沙丘", "弗兰克·赫伯特", "江苏凤凰文艺出版社", "zh", "9787539979087", ""}
			prince := importSample{"小王子", "圣埃克苏佩里", "人民文学出版社", "zh", "9787020042494", ""}
			seedPDF := func(s importSample, variant string) int64 {
				t.Helper()
				p := filepath.Join(seed, s.title+".pdf")
				if !live {
					p = samplePDF(t, seed, s.title, variant)
				}
				id, err := lib.AddBook(calibre.NewBook{File: p, Metadata: calibre.Metadata{Title: s.title,
					Authors: []string{s.author}, Publisher: s.publisher, Languages: []string{"zho"}, Identifiers: map[string]string{"isbn": s.isbn}}})
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			duneID := seedPDF(dune, "Dune PDF")
			princeID := seedPDF(prince, "Little Prince edition A")
			duneBook, _ := lib.Book(duneID)
			duneDetails, _ := lib.Details(duneBook)
			oldPDF := duneDetails.Files[0].Path
			oldHash, _, err := calibre.FileFingerprint(oldPDF)
			if err != nil {
				t.Fatal(err)
			}
			for _, duplicate := range []importSample{
				{"三体", "刘慈欣", "重庆出版社", "zh", "", ""},
				{"围城", "钱钟书", "人民文学出版社", "zh", "", ""},
			} {
				path := filepath.Join(input, duplicate.title+" - "+duplicate.author+".epub")
				if !live {
					path = sampleEPUB(t, input, duplicate)
				}
				if _, err := lib.Import(path); err != nil {
					t.Fatal(err)
				}
			}
			if !live {
				for _, sample := range []importSample{
					{"月亮与六便士", "", "测试出版社", "zh", "", ""},
					{"献给阿尔吉侬的花束", "丹尼尔·凯斯", "", "zh", "", ""},
					{"置身事内", "兰小欢", "上海人民出版社", "", "", ""},
					{"人类简史", "尤瓦尔·赫拉利", "中信出版社", "zh", "", ""},
					{"活着", "余华", "作家出版社", "zh", "", ""},
					{"1984", "乔治·奥威尔", "译林出版社", "zh", "", ""},
					{"局外人", "阿尔贝·加缪", "上海译文出版社", "zh", "", ""},
					{"百年孤独", "加西亚·马尔克斯", "南海出版公司", "zh", "", ""},
					dune,
				} {
					sampleEPUB(t, input, sample)
				}
				samplePDF(t, input, "小王子（李继宏译） - "+prince.author, "Little Prince edition B")
			}
			lib.Close()

			profile := filepath.Join(root, "Profile", "settings.json")
			newApp := func() *App {
				app := New(profile)
				app.run = func(fn func()) { fn() }
				app.covers.thumbs = nil
				app.openLibrary(library)
				if app.lib == nil {
					t.Fatalf("open library: %s", msgText(app.loadErrDetail))
				}
				t.Cleanup(func() {
					app.batchStore.Close()
					if app.lib != nil {
						app.lib.Close()
					}
				})
				loadAcceptanceSource(t, app, live)
				return app
			}
			app := newApp()
			newView := func(a *App) *ui.Tester {
				tt := ui.NewTester(a.View, 1320, 850)
				tt.SetLocale(loc)
				tt.Frame()
				return tt
			}
			view := newView(app)
			if !view.DropFiles(590, 360, input) {
				t.Fatal("folder drop not accepted")
			}
			view.Frame()
			menu := locales.In(loc)
			s := app.batch.active.Summary()
			if s.Files != 12 || s.Processed != 10 || s.Added != 8 || s.Duplicates != 2 || s.Review != 2 || s.Metadata != 3 {
				t.Fatalf("initial batch: %+v", s)
			}
			screenshot := func(name string) {
				if !metadataFirst {
					shot(t, view, "calibreno-"+name)
				}
			}
			screenshot("01-inbox")
			var liveValues map[string]string
			metadata := func() {
				clickBatch(t, view, menu.BatchStartMetadata())
				screenshot("04-metadata")
				if len(app.batchSelection()) != 3 {
					t.Fatalf("metadata selection: %v", app.batchSelection())
				}
				clickBatch(t, view, menu.BatchLookup())
				if live {
					screenshot("05-candidates")
					liveValues = reviewLiveNeoDB(t, app, view)
				}
				screenshot("05-preview")
				clickBatch(t, view, menu.BatchApplyMetadata())
				if s := app.batch.active.Summary(); s.Metadata != 0 || s.Enriched != 3 {
					t.Fatalf("metadata result: %+v", s)
				}
				clickBatch(t, view, menu.BatchBack())
			}
			if metadataFirst {
				metadata()
			}
			clickBatch(t, view, menu.BatchStartReview())
			// Files are scanned in filename order. Select Dune explicitly when the
			// edition appears first, then resume the remaining item after restart.
			for _, item := range app.batch.active.Items {
				if item.State == batch.Review && item.Metadata.Title == dune.title {
					app.openBatchCompare(item.ID)
					view.Frame()
					break
				}
			}
			screenshot("02-dupe-format")
			clickBatch(t, view, menu.BatchFormats())
			clickBatch(t, view, menu.BatchLater())
			app.lib.Close()
			app.lib = nil
			app.batchStore.Close()
			app = newApp()
			view = newView(app)
			clickBatch(t, view, menu.BatchInbox())
			if s := app.batch.active.Summary(); s.Review != 1 || s.Formats != 1 {
				t.Fatalf("resumed batch: %+v", s)
			}
			clickBatch(t, view, menu.BatchStartReview())
			if !view.HasText("李继宏译") {
				t.Fatalf("edition evidence is missing: %v", view.Texts())
			}
			screenshot("03-dupe-edition")
			clickBatch(t, view, menu.BatchKeepNew())
			if !metadataFirst {
				metadata()
			}
			clickBatch(t, view, menu.BatchResults())
			s = app.batch.active.Summary()
			if s.Files != 12 || s.Added != 9 || s.Formats != 1 || s.Duplicates != 2 || s.Enriched != 3 || s.Replaced != 0 || s.Failed != 0 || s.Pending() != 0 {
				t.Fatalf("final batch: %+v", s)
			}
			screenshot("06-result")
			t.Logf("final batch: 12 files, %d new books, %d added format, %d exact duplicates, %d enriched, %d pending", s.Added, s.Formats, s.Duplicates, s.Enriched, s.Pending())
			gotHash, _, err := calibre.FileFingerprint(oldPDF)
			if err != nil || oldHash != gotHash {
				t.Fatalf("Dune PDF changed: %v", err)
			}
			books, err := app.lib.Books()
			if err != nil || len(books) != 13 {
				t.Fatalf("library: %d books, error %v", len(books), err)
			}
			editions := 0
			for _, book := range books {
				switch book.Title {
				case "沙丘":
					if book.ID != duneID || !slices.Contains(book.Formats, "EPUB") || !slices.Contains(book.Formats, "PDF") {
						t.Fatalf("Dune formats: %+v", book)
					}
				case "小王子":
					editions++
					if book.ID == princeID && book.Publisher != prince.publisher {
						t.Fatalf("original Little Prince changed: %+v", book)
					}
				case "月亮与六便士":
					author := "威廉·萨默塞特·毛姆"
					if live {
						author = liveValues["authors"]
					}
					if !slices.Equal(book.Authors, []string{author}) || book.Publisher != "测试出版社" {
						t.Fatalf("Moon metadata: %+v", book)
					}
				case "献给阿尔吉侬的花束":
					publisher := "河南文艺出版社"
					if live {
						publisher = liveValues["publisher"]
					}
					if book.Publisher != publisher {
						t.Fatalf("Algernon metadata: %+v", book)
					}
				case "置身事内":
					if !slices.Equal(book.Languages, []string{"zho"}) {
						t.Fatalf("language: %+v", book)
					}
				}
				d, err := app.lib.Details(book)
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range d.Files {
					_, size, err := calibre.FileFingerprint(f.Path)
					if err != nil || size != f.Size {
						t.Fatalf("%s %s: size=%d library=%d error=%v", book.Title, f.Format, size, f.Size, err)
					}
				}
			}
			if editions != 2 {
				t.Fatalf("Little Prince editions: %d", editions)
			}
		})
	}
}

func loadAcceptanceSource(t *testing.T, a *App, live bool) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("neodb.social/api/catalog/search", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		candidate := ""
		switch {
		case strings.HasPrefix(query, "月亮与六便士"):
			candidate = `{"uuid":"moon","category":"book","title":"月亮与六便士","author":["威廉·萨默塞特·毛姆"],"pub_house":"应保留已有出版社","language":["zh"]}`
		case strings.HasPrefix(query, "献给阿尔吉侬的花束"):
			candidate = `{"uuid":"flowers","category":"book","title":"献给阿尔吉侬的花束","author":["丹尼尔·凯斯"],"pub_house":"河南文艺出版社","language":["zh"]}`
		case strings.HasPrefix(query, "置身事内"):
			candidate = `{"uuid":"china","category":"book","title":"置身事内","author":["兰小欢"],"pub_house":"上海人民出版社","language":["zh"]}`
		}
		fmt.Fprint(w, `{"data":[`+candidate+`]}`)
	})
	for _, files := range builtin.Builtin() {
		if data, _ := fs.ReadFile(files, "plugin.json"); !strings.Contains(string(data), "dalibre.neodb") {
			continue
		}
		var options plugin.Options
		if !live {
			options.Transport = fakeWeb{mux}
		}
		p, err := plugin.LoadBuiltin(files, options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		a.plugins = append(a.plugins, p)
	}
}

func reviewLiveNeoDB(t *testing.T, a *App, view *ui.Tester) map[string]string {
	t.Helper()
	want := map[string]struct{ id, field string }{
		"月亮与六便士":    {"0UL6PqFiFgF2yUJ9NcwhwN", "authors"},
		"献给阿尔吉侬的花束": {"25Cg2DMRWN3S9Ibv3ZRzEO", "publisher"},
		"置身事内":      {"4ljpMck8iNfGmYEnixA6QX", "language"},
	}
	for _, item := range a.batchSelection() {
		result := a.batch.lookup[item]
		if result == nil || result.err != nil {
			t.Fatalf("live NeoDB lookup for %q: %+v", a.batch.active.Item(item).Metadata.Title, result)
		}
		selection := want[result.book.Title]
		candidate := slices.IndexFunc(result.candidates, func(r plugin.Result) bool { return r.ID == selection.id })
		if candidate < 0 {
			t.Fatalf("live NeoDB did not return the reviewed candidate for %q: got %d candidates", result.book.Title, len(result.candidates))
		}
		book := result.candidates[candidate]
		if result.chosen != candidate {
			clickBatch(t, view, locales.BatchCandidateFor(result.book.Title))
			clickBatch(t, view, batchCandidateLabel(book))
		}
		if result.chosen != candidate {
			t.Fatalf("candidate choice for %q did not stick: %d", result.book.Title, result.chosen)
		}
		t.Logf("NeoDB live %q: %d candidates; selected %s title=%q author=%q publisher=%q language=%q ISBN=%q year=%q",
			result.book.Title, len(result.candidates), book.URL, book.Title, book.Authors, book.Publisher, book.Language, book.Identifiers["isbn"], book.Pubdate)
	}
	selected := map[string]string{}
	for i := range a.batch.preview {
		edit := &a.batch.preview[i]
		for j := range edit.fields {
			field := &edit.fields[j]
			field.selected = field.key == want[edit.title].field && field.before == ""
			if field.selected {
				selected[field.key] = field.after
				t.Logf("NeoDB live %q: approved %s=%q", edit.title, field.key, field.after)
			}
		}
	}
	if len(selected) != 3 || selected["language"] != "zho" {
		t.Fatalf("live fields offered: %v", selected)
	}
	view.Frame()
	return selected
}

func TestBatchMetadataPreservesLaterFields(t *testing.T) {
	a := newTestApp(t)
	book := a.books[0]
	item := batch.Item{Book: batch.Ref(book), State: batch.Added, NeedPublisher: true}
	_, m, err := readBatchBook(a.lib, &item)
	if err != nil {
		t.Fatal(err)
	}
	tag := m.Tags[0]
	edit := makeBatchEdit(&item, book, m, map[string]string{"publisher": "Older candidate", "tags": strings.Join([]string{tag, "Imported"}, "\x00")}, true)
	m.Publisher = "Updated in Calibre"
	if err := a.lib.UpdateMetadata(book.ID, m); err != nil {
		t.Fatal(err)
	}
	wrote, conflict, err := applyBatchMetadataEdit(a.lib, &item, edit)
	if err != nil || !wrote || !conflict {
		t.Fatalf("apply: wrote=%v conflict=%v error=%v", wrote, conflict, err)
	}
	book, m, err = readBatchBook(a.lib, &item)
	if err != nil || m.Publisher != "Updated in Calibre" || !slices.Contains(book.Tags, "Imported") ||
		strings.Count(strings.Join(book.Tags, "\x00"), tag) != 1 {
		t.Fatalf("after applying: %+v, %v", book, err)
	}
}

func TestBatchRetryAfterMissingSource(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := newTestApp(t)
	view := ui.NewTester(a.View, 1320, 850)
	view.SetLocale("zh-Hans")
	view.Frame()
	input := filepath.Join(t.TempDir(), "恢复的书 - 作者.txt")
	a.importFiles([]string{input})
	view.Frame()
	if s := a.batch.active.Summary(); s.Failed != 1 || s.Pending() != 1 || !view.HasText(locales.BatchReadError()) {
		t.Fatalf("failed source: %+v, %v", s, view.Texts())
	}
	if err := os.WriteFile(input, []byte("reconnected"), 0o644); err != nil {
		t.Fatal(err)
	}
	clickBatch(t, view, locales.CommonRetry())
	if s := a.batch.active.Summary(); s.Added != 1 || s.Pending() != 0 {
		t.Fatalf("retry: %+v", s)
	}
}

func TestBatchRecoversMetadataCheckpoint(t *testing.T) {
	a := newTestApp(t)
	book := a.books[0]
	b := batch.New(a.batchKey, "recovery")
	item := batch.NewItem("book.epub", batch.Added)
	item.Book = batch.Ref(book)
	item.NeedPublisher = true
	item.MetadataIntent = map[string]string{"publisher": "Updated before exit", "language": "zho"}
	b.Items = append(b.Items, item)
	if err := a.batchStore.Save(b); err != nil {
		t.Fatal(err)
	}
	_, metadata, err := readBatchBook(a.lib, &item)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Publisher = "Updated before exit"
	if err := a.lib.UpdateMetadata(book.ID, metadata); err != nil {
		t.Fatal(err)
	}
	a.selectBatch(b.ID)
	if active := a.batch.active; active == nil || active.Items[0].NeedsMetadata() || active.Summary().Enriched != 1 {
		t.Fatalf("metadata checkpoint: %+v", active)
	}
}
