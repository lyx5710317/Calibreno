package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/bookconvert"
	"dalibre/internal/calibre"
)

type conversionSession struct {
	form conversionDialogState
	pdf  pdfReviewState

	lib   *calibre.Library
	book  int64
	local string

	workspace string
	pdfSource string
	loadPage  context.CancelFunc
	cancelJob context.CancelFunc
	jobDone   <-chan struct{}
	pageGen   uint64
	workGen   uint64
	picking   bool
	running   bool
}

func (a *App) conversionEnabled() bool {
	return bookconvert.Supported() && a.conversionHome != ""
}

func conversionSources(formats []string) []string {
	return slices.DeleteFunc(slices.Clone(formats), func(format string) bool {
		return len(bookconvert.OutputFormats(format)) == 0
	})
}

func (a *App) conversionMenuItem(m *ui.Menu, b *calibre.Book, source string) {
	if !a.conversionEnabled() || source != "" && len(bookconvert.OutputFormats(source)) == 0 ||
		len(conversionSources(b.Formats)) == 0 {
		return
	}
	if m.Item(locales.ConversionTitle()).Icon(icons.Menu(icons.Replace)).Disabled(a.busy != "").Chosen() {
		a.openBookConversion(b, source)
	}
}

func (a *App) openBookConversion(b *calibre.Book, preferred string) {
	if !a.conversionEnabled() || a.busy != "" || a.lib == nil || a.loading {
		return
	}
	sources := conversionSources(b.Formats)
	if len(sources) == 0 {
		return
	}
	s := &conversionSession{
		lib: a.lib, book: b.ID,
		form: conversionDialogState{open: true, title: b.Title, sources: sources, formats: slices.Clone(b.Formats),
			source: max(0, slices.Index(sources, preferred)), isBook: true, pages: "1"},
	}
	a.openConversion(s)
}

func (a *App) chooseLocalConversion() {
	if !a.conversionEnabled() || a.busy != "" || a.anyDialog() {
		return
	}
	options := mygo.OpenDialogOptions{
		Parent: a.win, Title: locales.ConversionLocal(),
		Filters: []mygo.FileFilter{{Name: locales.ConversionFiles(), Extensions: []string{"epub", "txt", "fb2", "pdf", "mobi", "azw3"}}},
	}
	a.run(func() {
		files, err := mygo.Dialog.Open(options)
		a.post(func() {
			if err != nil {
				a.toastError(err)
			} else if len(files) > 0 && a.busy == "" && !a.anyDialog() {
				a.openLocalConversion(files[0])
			}
		})
	})
}

func (a *App) openLocalConversion(file string) {
	format := strings.ToUpper(strings.TrimPrefix(filepath.Ext(file), "."))
	if len(bookconvert.OutputFormats(format)) == 0 {
		a.toastError(errors.New(locales.ConversionUnsupported()))
		return
	}
	a.openConversion(&conversionSession{
		local: file,
		form:  conversionDialogState{open: true, title: filepath.Base(file), sources: []string{format}, pages: "1"},
	})
}

func (a *App) openConversion(s *conversionSession) {
	a.conversion = s
	if slices.Contains(s.form.sources, "PDF") {
		a.refreshConversionWorkspace(s)
	}
}

func (s *conversionSession) pdfPath() (string, error) {
	if s.lib == nil {
		return s.local, nil
	}
	b, err := s.lib.Book(s.book)
	if err != nil {
		return "", err
	}
	details, err := s.lib.Details(b)
	if err != nil {
		return "", err
	}
	for _, file := range details.Files {
		if file.Format == "PDF" {
			return s.lib.ResolveLibraryFile(file.Path)
		}
	}
	return "", os.ErrNotExist
}

// The original path and version select a checkpoint across app restarts.
// The worker verifies the PDF's content digest before reusing any page.
func conversionWorkspace(home, source string) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	file, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	key := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d", strings.ToLower(file), info.Size(), info.ModTime().UnixNano()))
	return filepath.Join(home, "pdf", fmt.Sprintf("%x", key)), nil
}

func (a *App) refreshConversionWorkspace(s *conversionSession) {
	home := a.conversionHome
	gen := s.workGen
	a.run(func() {
		source, err := s.pdfPath()
		if err != nil {
			return
		}
		workspace, err := conversionWorkspace(home, source)
		if err != nil {
			return
		}
		summary, err := bookconvert.ReadPDFWorkspace(context.Background(), workspace)
		if err != nil {
			return
		}
		a.post(func() {
			if a.conversion != s || s.workGen != gen {
				return
			}
			a.showConversionWorkspace(s, source, workspace, summary)
		})
	})
}

func (a *App) showConversionWorkspace(s *conversionSession, source, workspace string, summary bookconvert.PDFWorkspaceSummary) {
	s.pdfSource, s.workspace = source, workspace
	s.pdf.summary = summary
	s.form.reviewAvailable = summary.Total > 0
}

func (a *App) conversionDialogs(c *ui.Context) {
	s := a.conversion
	if s == nil {
		return
	}
	if !s.form.open && !s.pdf.open && !s.running && !s.picking {
		a.conversion = nil
		return
	}
	busy := s.running || s.picking
	progress := ""
	if s.running {
		progress = a.busy
		if a.busyTotal > 0 {
			progress = locales.MainBusyProgress(a.busy, a.busyDone, a.busyTotal)
		}
	}
	if s.pdf.open {
		pdfReviewDialog(c, &s.pdf, busy, progress, func(page int) { a.loadConversionPDFPage(s, page) },
			func(pages []int) { a.startConversion(s, "", "", pages) },
			func() { a.backToConversion(s) }, a.busyStop)
		return
	}
	conversionDialog(c, &s.form, conversionDialogCallbacks{
		busy: busy, progress: progress, targets: bookconvert.OutputFormats, cancelWork: a.busyStop,
		submit: func() { a.submitConversion(s) },
		preview: func() {
			if pages, err := s.form.selectedPages(); err == nil && len(pages) > 0 {
				a.startConversion(s, "", "", pages)
			}
		},
		review: func() { a.openConversionPDF(s) },
	})
}

func (a *App) submitConversion(s *conversionSession) {
	target := s.form.selectedTarget(bookconvert.OutputFormats)
	if target == "" || s.running || s.picking || a.busy != "" {
		return
	}
	if s.form.writesToLibrary(target) {
		a.startConversion(s, target, "", nil)
		return
	}
	ext := "." + strings.ToLower(target)
	name := strings.TrimSuffix(s.form.title, filepath.Ext(s.local))
	if s.form.selectedSource() == target {
		name += " - " + locales.ConversionCopySuffix()
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	options := mygo.SaveDialogOptions{
		Parent: a.win, Title: locales.ConversionToCopy(), DefaultPath: name + ext,
		Filters: []mygo.FileFilter{{Name: target, Extensions: []string{strings.TrimPrefix(ext, ".")}}},
	}
	if s.local != "" {
		options.DefaultPath = filepath.Join(filepath.Dir(s.local), options.DefaultPath)
	}
	s.picking = true
	a.run(func() {
		output, err := mygo.Dialog.Save(options)
		a.post(func() {
			s.picking = false
			if a.conversion != s || !s.form.open {
				return
			}
			if err != nil {
				a.conversionError(s, err)
				return
			}
			if output == "" {
				return
			}
			if filepath.Ext(output) == "" {
				output += ext
			}
			if !strings.EqualFold(filepath.Ext(output), ext) {
				s.form.err = locales.ConversionExpectedExtension(ext)
				return
			}
			a.startConversion(s, target, output, nil)
		})
	})
}

func (a *App) startConversion(s *conversionSession, target, output string, pages []int) {
	if a.conversion != s || a.busy != "" || s.running || (s.lib != nil && (s.lib != a.lib || a.loading)) {
		return
	}
	source := s.form.selectedSource()
	if len(pages) > 0 && source != "PDF" || len(pages) == 0 && !slices.Contains(bookconvert.OutputFormats(source), target) {
		return
	}
	req := s.form.requestOptions(target)
	req.Input, req.Output, req.Pages = s.local, output, pages
	s.form.err = ""
	s.running = true
	s.workGen++
	if s.loadPage != nil {
		s.loadPage()
		s.loadPage = nil
	}
	s.pageGen++
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancelJob, s.jobDone = cancel, done
	a.busy, a.busyDone, a.busyTotal, a.busyStop = locales.ConversionBusy(), 0, 0, cancel
	engine, home := a.conversionEngine, a.conversionHome
	progress := &conversionProgress{onPage: func(done, total int) {
		a.post(func() {
			if a.conversion == s && s.running {
				a.busyDone, a.busyTotal = done, total
			}
		})
	}}
	a.run(func() {
		defer cancel()
		var pdfFile string
		var err error
		if source == "PDF" {
			pdfFile, err = s.pdfPath()
			if err == nil {
				req.Workspace, err = conversionWorkspace(home, pdfFile)
			}
		}
		var out bookconvert.Result
		if err == nil {
			if s.lib == nil {
				out, err = engine.Convert(ctx, req, progress)
			} else {
				out, err = engine.ConvertBook(ctx, s.lib, s.book, source, target, req, progress)
			}
		}
		var summary bookconvert.PDFWorkspaceSummary
		if req.Workspace != "" {
			summary, _ = bookconvert.ReadPDFWorkspace(context.Background(), req.Workspace)
		}
		close(done)
		a.post(func() {
			s.running = false
			a.busy, a.busyDone, a.busyTotal, a.busyStop = "", 0, 0, nil
			if a.conversion != s {
				return
			}
			if summary.Total > 0 {
				a.showConversionWorkspace(s, pdfFile, req.Workspace, summary)
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					a.toast("%s", locales.ErrorCancelled())
				} else {
					a.conversionError(s, err)
				}
				if s.pdf.open {
					a.loadConversionPDFPage(s, s.pdf.page)
				}
				return
			}
			if len(pages) > 0 {
				s.pdf.page = pages[0]
				a.openConversionPDF(s)
				return
			}
			a.completeConversion(s, target, output, out.Warnings)
		})
	})
}

// ShutdownConversion cancels native workers before the application exits.
// PDF pages already written by the worker are available on the next launch.
func (a *App) ShutdownConversion() {
	s := a.conversion
	if s == nil {
		return
	}
	if s.loadPage != nil {
		s.loadPage()
	}
	if s.cancelJob != nil {
		s.cancelJob()
		select {
		case <-s.jobDone:
		case <-time.After(4 * time.Second):
		}
	}
}

func (a *App) conversionError(s *conversionSession, err error) {
	s.form.err = presentError(err)
	if s.pdf.open || s.form.err != err.Error() {
		a.toastErrorSummary(locales.ConversionFailed(), err)
	}
}

func (a *App) completeConversion(s *conversionSession, format, output string, warnings bool) {
	s.form.open, s.pdf.open = false, false
	message := locales.ConversionFinished(format)
	if warnings {
		message = locales.ConversionFinishedWarnings(format)
	}
	if output != "" {
		a.toasts = append(a.toasts, toastMsg{text: message, action: locales.CommonShowInFolder(), onAction: func() { revealPath(output) }})
	} else {
		a.toast("%s", message)
		if a.lib == s.lib {
			a.reload(nil)
		}
	}
}

func (a *App) openConversionPDF(s *conversionSession) {
	if s.pdf.summary.Total == 0 {
		return
	}
	page := s.pdf.page
	if page < 1 && len(s.pdf.summary.Saved) > 0 {
		page = s.pdf.summary.Saved[0]
	}
	s.pdf.title, s.pdf.open, s.form.open = s.form.title, true, false
	a.loadConversionPDFPage(s, max(1, page))
}

func (a *App) backToConversion(s *conversionSession) {
	if s.loadPage != nil {
		s.loadPage()
		s.loadPage = nil
	}
	s.pageGen++
	s.pdf.open, s.form.open = false, true
}

func (a *App) loadConversionPDFPage(s *conversionSession, page int) {
	if page < 1 || page > s.pdf.summary.Total {
		return
	}
	if s.loadPage != nil {
		s.loadPage()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.loadPage = cancel
	s.pageGen++
	gen := s.pageGen
	s.pdf.page, s.pdf.loading = page, true
	s.pdf.preview = nil
	s.pdf.record, s.pdf.loadError, s.pdf.previewError = bookconvert.PDFPage{}, "", ""
	source, workspace, total := s.pdfSource, s.workspace, s.pdf.summary.Total
	engine, home := a.conversionEngine, a.conversionHome
	a.run(func() {
		defer cancel()
		record, readErr := bookconvert.ReadPDFPage(workspace, page, total)
		current := func() bool { return a.conversion == s && s.pdf.open && s.pageGen == gen }
		publishText := func() {
			s.pdf.record = record
			if readErr != nil {
				s.pdf.loadError = presentError(readErr)
			}
		}
		preview := record.Preview
		previewErr := record.PreviewError
		if preview == nil && ctx.Err() == nil {
			a.post(func() {
				if current() {
					publishText()
				}
			})
			key, err := conversionWorkspace(home, source)
			if err != nil {
				previewErr = err
			} else if key != workspace {
				previewErr = calibre.ErrBookChanged
			} else {
				preview, previewErr = engine.RenderPDFPreview(ctx, source, page)
			}
		}
		var bitmap *ui.Bitmap
		if preview != nil {
			bitmap = ui.NewBitmap(preview)
		}
		if ctx.Err() != nil {
			return
		}
		a.post(func() {
			if !current() {
				return
			}
			s.pdf.loading, s.pdf.preview = false, bitmap
			publishText()
			if previewErr != nil {
				s.pdf.previewError = locales.ConversionPDFPreviewUnavailable()
			}
		})
	})
}

type conversionProgress struct {
	line   []byte
	onPage func(done, total int)
}

func (p *conversionProgress) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		part, rest, end := bytes.Cut(data, []byte{'\n'})
		if len(p.line)+len(part) < 256 {
			p.line = append(p.line, part...)
		}
		if end {
			var done, total int
			if parsed, _ := fmt.Sscanf(string(p.line), "PDF %d/%d", &done, &total); parsed == 2 && done >= 1 && done <= total {
				p.onPage(done, total)
			}
			p.line = p.line[:0]
		}
		data = rest
	}
	return n, nil
}
