package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
)

type libraryDuplicateScan struct {
	cancel context.CancelFunc
}

type libraryDuplicateResult struct {
	report  calibre.LibraryDuplicateReport
	version int
}

func (a *App) runLibraryDuplicateScan() {
	if a.lib == nil || a.loading || a.busy != "" || a.batch.loading {
		return
	}
	lib, generation, version := a.lib, a.loadGen, a.version
	ctx, cancel := context.WithCancel(context.Background())
	scan := &libraryDuplicateScan{cancel: cancel}
	a.duplicateScan = scan
	a.busy, a.busyDone, a.busyTotal, a.busyStop = locales.LibraryDuplicateScanning(), 0, 0, cancel
	a.run(func() {
		defer cancel()
		var last time.Time
		report, err := lib.ScanDuplicates(ctx, func(done, total int) {
			if done != 0 && done != total && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			a.post(func() {
				if a.duplicateScan == scan {
					a.busyDone, a.busyTotal = done, total
				}
			})
		})
		a.post(func() {
			if a.duplicateScan != scan {
				return
			}
			a.duplicateScan = nil
			a.busy, a.busyDone, a.busyTotal, a.busyStop = "", 0, 0, nil
			if a.lib != lib || a.loadGen != generation {
				return
			}
			switch {
			case errors.Is(err, context.Canceled):
				a.toast("%s", locales.LibraryDuplicateCancelled())
			case err != nil:
				a.toastError(err)
			default:
				a.act.results = &actionResults{
					duplicates: &libraryDuplicateResult{report: report, version: version},
					version:    -1,
					cur:        -1,
				}
				a.page, a.curID = pageLibrary, 0
				a.chosen.Clear()
			}
		})
	})
}

// buildDuplicateRows keeps each verified format group intact, even if a
// book has copies of more than one format.
func (r *actionResults) buildDuplicateRows(byID map[int64]*calibre.Book, version int) {
	if version != r.duplicates.version {
		return
	}
	shown := map[int64]bool{}
	for i, duplicate := range r.duplicates.report.Groups {
		var books []*calibre.Book
		for _, id := range duplicate.BookIDs {
			if book := byID[id]; book != nil {
				books = append(books, book)
			}
		}
		if len(books) < 2 {
			continue
		}
		group := i + 1
		r.rows = append(r.rows, resultRow{header: duplicate.Format, count: len(books), duplicateGroup: group})
		for _, book := range books {
			r.rows = append(r.rows, resultRow{book: book, duplicateGroup: group})
			if !shown[book.ID] {
				r.shown = append(r.shown, book)
				shown[book.ID] = true
			}
		}
	}
}

func (a *App) duplicateResultSummary(c *ui.Context, result *libraryDuplicateResult) {
	report := result.report
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		summary := locales.LibraryDuplicateSummary(groupDigits(report.BooksScanned), groupDigits(report.FilesScanned))
		ui.Text(c, summary).FontSize(12).TextColor(pal().TextSecondary).Shrink(1).SingleLine()
		if len(report.Unreadable) > 0 {
			ui.Text(c, locales.LibraryDuplicateUnreadable(len(report.Unreadable))).FontSize(12).TextColor(pal().Warning.FG).Shrink(1).SingleLine()
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.ErrorDetails(), theme.ControlS).Clicked() {
				a.errorDetails = strings.Join(report.Unreadable, "\n")
				a.errorDetailsOpen = true
			}
		}
	})
}

func (a *App) duplicateResultsEmpty(c *ui.Context, result *libraryDuplicateResult) {
	title := locales.LibraryDuplicateNone()
	if result.version != a.version {
		title = locales.LibraryDuplicateStale()
	} else if len(result.report.Unreadable) > 0 {
		title = locales.LibraryDuplicateNoneReadable()
	}
	ui.Column(c).Grow(1).MinHeight(0).Center().Gap(theme.Space8).Padding(theme.Space24).Children(func() {
		ui.Icon(c, icons.Copy).FontSize(22).TextColor(pal().IconMuted)
		ui.Text(c, title).FontSize(15).FontWeight(500).TextAlign(ui.Center)
	})
}
