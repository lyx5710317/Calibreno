package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/batch"
	"dalibre/internal/calibre"
)

func (a *App) batchToolbar(c *ui.Context) {
	b := &a.batch
	ui.Row(c).Height(theme.ToolbarHeight).Padding(0, theme.Space24+c.TitleBar().Right, 0, theme.Space32).
		Gap(theme.Space8).AlignItems(ui.Center).DragWindow().BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle).Children(func() {
		ui.Text(c, locales.BatchInbox()).FontSize(22).FontWeight(600).Grow(1).SingleLine()
		if b.active != nil && b.mode != batchOverview {
			if widgets.Button(c, widgets.Secondary, nil, locales.BatchBack()).Disabled(b.work).Clicked() {
				b.mode = batchOverview
			}
		}
		if b.mode == batchOverview && b.active != nil {
			if widgets.Button(c, widgets.Secondary, nil, locales.BatchResults()).Disabled(b.work || b.loading).Clicked() {
				b.mode = batchResult
			}
		}
		if b.mode == batchOverview {
			if widgets.Button(c, widgets.Secondary, icons.Folder, locales.BatchAddFolders()).Clicked() {
				a.chooseImport(true)
			}
		}
		if widgets.Button(c, widgets.Primary, icons.Plus, locales.BatchAddFiles()).Clicked() {
			a.chooseBooks()
		}
	})
}

func (a *App) batchContent(c *ui.Context) {
	b := &a.batch
	if b.mode == batchPreview && b.active != nil {
		a.batchPreviewPage(c)
		return
	}
	ui.Scroll(c.Key(b.mode)).Grow(1).Padding(theme.Space24, theme.Space32).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
		if b.work || b.loading {
			label := a.busy
			if label == "" {
				label = locales.CommonLoading()
			}
			ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(16, 16).Label(label)
				ui.Text(c, label).FontSize(13)
				if a.busyTotal > 0 {
					ui.Text(c, fmt.Sprintf("%d / %d", a.busyDone, a.busyTotal)).FontSize(13).TextColor(pal().TextTertiary)
				}
			})
		}
		if b.active == nil {
			if !b.work && !b.loading {
				batchCard(c, func() { batchHeading(c, locales.BatchEmpty(), locales.BatchDropHint()) })
			}
			return
		}
		switch b.mode {
		case batchCompare:
			a.batchCompareView(c)
		case batchMetadata:
			a.batchMetadataView(c)
		case batchCommon:
			a.batchCommonView(c)
		case batchResult:
			a.batchResultView(c)
		default:
			a.batchOverviewView(c)
		}
	})
}

func batchCard(c *ui.Context, body func()) {
	ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Padding(theme.Space20).Gap(theme.Space12).
		Background(pal().Surface).Border(1, pal().BorderSubtle).Radius(theme.RadiusLG).Children(body)
}

func batchHeading(c *ui.Context, title, help string) {
	ui.Column(c).MinWidth(0).Gap(theme.Space4).Children(func() {
		ui.Text(c, title).FontSize(19).FontWeight(600)
		if help != "" {
			ui.Text(c, help).FontSize(13).TextColor(pal().TextSecondary)
		}
	})
}

func batchNotice(c *ui.Context, icon *ui.SVG, message string, tone theme.Pair) {
	ui.Row(c).Gap(theme.Space8).Padding(theme.Space12, theme.Space16).AlignItems(ui.Center).
		Radius(theme.RadiusSM).Background(tone.BG).Children(func() {
		ui.Icon(c, icon).FontSize(16).TextColor(tone.FG)
		ui.Text(c, message).Grow(1).MinWidth(0).FontSize(13).TextColor(tone.FG)
	})
}

func batchSource(source string) string {
	if source == "" {
		return locales.BatchSelectedFiles()
	}
	return source
}

func (a *App) batchOverviewView(c *ui.Context) {
	b := &a.batch
	s := b.active.Summary()
	batchCard(c, func() {
		ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Folder).FontSize(22).TextColor(pal().IconMuted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space4).Children(func() {
				ui.Text(c, batchSource(s.Source)).FontWeight(600).FontSize(20).SingleLine()
				ui.Text(c, s.Created.Local().Format("2006-01-02 15:04")+" · "+locales.BatchFileCount(s.Files)).FontSize(13).TextColor(pal().TextSecondary)
			})
			ui.Text(c, locales.BatchFileProgress(groupDigits(s.Processed), groupDigits(s.Files))).FontSize(13).TextColor(pal().TextSecondary)
			selector := widgets.Button(c, widgets.Secondary, icons.ChevronDown, locales.BatchHistory()).Disabled(b.work || b.loading)
			selector.PopoverMenuButton(func(m *ui.Menu) {
				for _, summary := range b.history {
					title := batchSource(summary.Source) + " · " + summary.Created.Local().Format("01-02 15:04")
					if m.Item(title).ID(summary.ID).Radio(s.ID == summary.ID).Chosen() {
						a.selectBatch(summary.ID)
					}
				}
			})
		})
	})
	ui.Row(c).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
		a.batchTask(c.Key("review"), icons.Layers, locales.BatchReview(), locales.BatchReviewHint(), locales.BatchStartReview(), s.Review, a.firstReview)
		a.batchTask(c.Key("metadata"), icons.Library, locales.BatchNeedsMetadata(), locales.BatchMetadataHint(), locales.BatchStartMetadata(), s.Metadata, a.openBatchMetadata)
	})
	if s.Waiting > 0 {
		if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.BatchResumeImport()).Disabled(b.work || b.loading).Clicked() {
			a.retryImport("")
		}
	}
	batchCard(c, func() {
		batchHeading(c, locales.BatchTasks(), "")
		ui.Column(c).AlignItems(ui.Stretch).Children(func() {
			a.batchItems(c, false)
		})
	})
}

func (a *App) batchTask(c *ui.Context, icon *ui.SVG, title, hint, action string, count int, open func()) {
	batchCard(c, func() {
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).FontSize(18).TextColor(pal().IconMuted)
			ui.Text(c, title).Grow(1).FontSize(16).FontWeight(600)
			ui.Text(c, groupDigits(count)).FontSize(20).FontWeight(600)
		})
		ui.Text(c, hint).FontSize(13).TextColor(pal().TextSecondary)
		ui.Row(c).Children(func() {
			if widgets.Button(c, widgets.Secondary, nil, action).Disabled(a.batch.work || a.batch.loading || count == 0 && title == locales.BatchReview()).Clicked() {
				open()
			}
		})
	})
}

func pendingBatchItem(item *batch.Item) bool {
	return item.State == batch.Review || item.State == batch.Failed || item.State == batch.Waiting || item.NeedsMetadata()
}

func batchItemStatus(item *batch.Item) string {
	switch item.State {
	case batch.Review:
		return locales.BatchReview()
	case batch.Supplement:
		return locales.BatchFormats()
	case batch.Replaced:
		return locales.BatchReplaced()
	case batch.Duplicate:
		return locales.BatchExact()
	case batch.Skipped:
		return locales.BatchSkipped()
	case batch.Unsupported:
		return locales.BatchUnsupported()
	case batch.Failed:
		return locales.BatchFailed()
	case batch.Added:
		switch {
		case item.NeedsMetadata():
			return locales.BatchNeedsMetadata()
		case item.Enriched:
			return locales.BatchEnriched()
		default:
			return locales.BatchAdded()
		}
	default:
		return locales.BatchWaiting()
	}
}

func (a *App) batchItems(c *ui.Context, all bool) {
	pending := 0
	for i := range a.batch.active.Items {
		item := &a.batch.active.Items[i]
		if !all && !pendingBatchItem(item) {
			continue
		}
		pending++
		key := c.Key(item.ID)
		ui.Row(key).MinHeight(42).Padding(theme.Space6, 0).Gap(theme.Space12).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(pal().BorderSubtle).Children(func() {
			ui.Column(key).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				name := filepath.Base(item.Path)
				if item.Metadata.Title != "" {
					name = item.Metadata.Title
				}
				if book := a.byID[item.Book.ID]; book != nil && book.UUID == item.Book.UUID {
					name = book.Title
				}
				if item.FilenameEdition != "" {
					name += " · " + item.FilenameEdition
				}
				ui.Text(key, name).FontSize(13).FontWeight(500).SingleLine()
				if item.Problem != "" {
					ui.Text(key, batchProblem(item)).FontSize(12).TextColor(pal().Danger.FG)
					if item.Detail != "" {
						if widgets.ButtonSized(key, widgets.Ghost, nil, locales.ErrorDetails(), theme.ControlS).Clicked() {
							a.errorDetails, a.errorDetailsOpen = item.Detail, true
						}
					}
				} else if item.Book.ID != 0 && item.State != batch.Added {
					ui.Text(key, locales.BatchBookReference(groupDigits(int(item.Book.ID)))).FontSize(12).TextColor(pal().TextTertiary)
				}
			})
			if !all {
				author := strings.Join(item.Metadata.Authors, ", ")
				if item.NeedAuthor {
					author = locales.BatchUnknownAuthor()
				}
				ui.Text(key, batchText(author)).Width(155).FontSize(13).TextColor(pal().TextSecondary).SingleLine()
			}
			ui.Text(key, item.Format).Width(55).FontSize(12).TextColor(pal().TextTertiary)
			ui.Text(key, batchItemStatus(item)).Width(112).FontSize(13)
			disabled := a.batch.work || a.batch.loading
			switch {
			case item.State == batch.Review:
				if widgets.Button(key, widgets.Secondary, nil, locales.BatchCompare()).Disabled(disabled).Clicked() {
					a.openBatchCompare(item.ID)
				}
			case item.State == batch.Failed:
				if widgets.Button(key, widgets.Secondary, nil, locales.CommonRetry()).Disabled(disabled).Clicked() {
					a.retryImport(item.ID)
				}
			case item.NeedsMetadata():
				if widgets.Button(key, widgets.Secondary, nil, locales.BatchStartMetadata()).Disabled(disabled).Clicked() {
					a.batch.selected = map[string]bool{item.ID: true}
					a.openBatchMetadata()
				}
			}
		})
	}
	if pending == 0 {
		ui.Text(c, locales.BatchNoPending()).FontSize(13).TextColor(pal().TextSecondary)
	}
}

func batchProblem(item *batch.Item) string {
	switch item.Problem {
	case "read":
		return locales.BatchReadError()
	case "compare":
		return locales.BatchCompareError()
	case "changed":
		return locales.BatchChanged()
	default:
		return locales.BatchWriteError()
	}
}

func (a *App) batchCompareView(c *ui.Context) {
	b := &a.batch
	i := b.active.Item(b.compare)
	if i == nil {
		return
	}
	batchHeading(c, locales.BatchCompareTitle(), locales.BatchCompareHint())
	if len(i.Candidates) > 1 {
		options := make([]string, len(i.Candidates)+1)
		options[0] = locales.BatchPickCandidate()
		for k, ref := range i.Candidates {
			label := locales.BatchNameMatch()
			if ref.ByISBN {
				label = locales.BatchISBNMatch()
			}
			parts := []string{locales.BatchBookReference(groupDigits(int(ref.Book.ID))), ref.Title}
			if ref.Publisher != "" {
				parts = append(parts, ref.Publisher)
			}
			if !ref.Pubdate.IsZero() {
				parts = append(parts, ref.Pubdate.Format("2006"))
			}
			options[k+1] = strings.Join(append(parts, label), " · ")
		}
		cur := options[max(0, b.choice+1)]
		if ui.Select(c, &cur, options).Width(360).Label(locales.BatchPickCandidate()).Disabled(b.loading || b.work).Changed() {
			for k, option := range options {
				if option == cur {
					a.loadBatchComparison(k - 1)
				}
			}
		}
	}
	d := b.comparison
	ready := d != nil && d.err == nil
	edition := ready && (i.FilenameEdition != "" || likelyOtherEdition(i.Metadata, d.book.Metadata(d.details)))
	if ready && len(i.Translators) > 0 && len(d.translators) > 0 && strings.Join(i.Translators, "\x00") != strings.Join(d.translators, "\x00") {
		edition = true
	}
	ui.Row(c).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
		batchCard(c.Key("incoming"), func() {
			batchHeading(c, locales.BatchNewFile(), filepath.Base(i.Path))
			batchComparisonFields(c, i.Metadata, i.Format, i.Size)
			if len(i.Translators) != 0 {
				batchComparisonRow(c, locales.BatchTranslator(), strings.Join(i.Translators, ", "))
			} else if i.FilenameEdition != "" {
				batchComparisonRow(c, locales.BatchFilenameEdition(), i.FilenameEdition)
			}
		})
		batchCard(c.Key("existing"), func() {
			batchHeading(c, locales.BatchExistingBook(), "")
			if ready {
				size := d.book.Size
				for _, f := range d.details.Files {
					if f.Format == i.Format {
						size = f.Size
					}
				}
				batchComparisonFields(c, d.book.Metadata(d.details), strings.Join(d.book.Formats, ", "), size)
				if len(d.translators) != 0 {
					batchComparisonRow(c, locales.BatchTranslator(), strings.Join(d.translators, ", "))
				}
			} else if d != nil {
				ui.Text(c, locales.BatchChanged()).FontSize(13).TextColor(pal().Danger.FG)
			} else {
				ui.Text(c, locales.BatchPickCandidate()).FontSize(13).TextColor(pal().TextSecondary)
			}
		})
	})
	switch {
	case b.confirmReplace:
		batchNotice(c, icons.Alert, locales.BatchReplaceConfirm(), pal().Warning)
	case edition:
		batchNotice(c, icons.Library, locales.BatchSuggestEdition(), theme.Pair{FG: pal().TextAccent, BG: pal().AccentSubtle})
	case ready && !d.hasFormat:
		batchNotice(c, icons.CheckCircle, locales.BatchSuggestFormat(), pal().Success)
	case ready:
		batchNotice(c, icons.Alert, locales.BatchSuggestReview(), pal().Warning)
	}
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		disabled := b.work || b.loading
		if b.confirmReplace {
			if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Disabled(disabled).Clicked() {
				b.confirmReplace = false
			}
			if widgets.Button(c, widgets.Primary, nil, locales.BatchConfirmReplace()).Disabled(disabled || !ready).Clicked() {
				a.applyBatchDecision(batch.Replace)
			}
			return
		}
		if widgets.Button(c, widgets.Secondary, nil, locales.BatchLater()).Disabled(b.work).Clicked() {
			b.mode = batchOverview
		}
		if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.BatchRecheck()).Disabled(disabled).Clicked() {
			a.recheckBatchItem()
		}
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.BatchSkip()).Disabled(disabled).Clicked() {
			a.applyBatchDecision(batch.Skip)
		}
		var newStyle = widgets.Primary
		if ready && !d.hasFormat && !edition {
			newStyle = widgets.Secondary
		}
		if widgets.Button(c, newStyle, nil, locales.BatchKeepNew()).Disabled(disabled).Clicked() {
			a.applyBatchDecision(batch.AddBook)
		}
		if ready && d.hasFormat {
			if widgets.Button(c, widgets.Secondary, nil, locales.BatchReplace()).Disabled(disabled).Clicked() {
				b.confirmReplace = true
			}
		} else if ready {
			style := widgets.Primary
			if edition {
				style = widgets.Secondary
			}
			if widgets.Button(c, style, nil, locales.BatchFormats()).Disabled(disabled).Clicked() {
				a.applyBatchDecision(batch.AddFormat)
			}
		}
	})
}

func likelyOtherEdition(a, b calibre.Metadata) bool {
	left, right := isbnOf(a.Identifiers["isbn"]), isbnOf(b.Identifiers["isbn"])
	if left != "" && right != "" {
		return left != right
	}
	return a.Publisher != "" && b.Publisher != "" && !strings.EqualFold(a.Publisher, b.Publisher) ||
		!a.Pubdate.IsZero() && !b.Pubdate.IsZero() && a.Pubdate.Year() != b.Pubdate.Year()
}

func batchComparisonFields(c *ui.Context, m calibre.Metadata, format string, size int64) {
	row := func(label, val string) {
		batchComparisonRow(c, label, val)
	}
	row(locales.CommonTitle(), m.Title)
	row(locales.CommonAuthors(), strings.Join(m.Authors, ", "))
	row("ISBN", m.Identifiers["isbn"])
	row(locales.CommonPublisher(), m.Publisher)
	date := ""
	if !m.Pubdate.IsZero() {
		date = m.Pubdate.Format("2006-01-02")
	}
	row(locales.CommonPublished(), date)
	row(locales.BatchFormatLabel(), format)
	row(locales.BatchFileSize(), humanSize(size))
}

func batchComparisonRow(c *ui.Context, label, value string) {
	ui.Row(c.Key(label)).Gap(theme.Space10).Children(func() {
		ui.Text(c, label).Width(100).FontSize(13).TextColor(pal().TextTertiary)
		ui.Text(c, batchText(value)).Grow(1).MinWidth(0).FontSize(13).Selectable()
	})
}

func batchText(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func (a *App) batchResultView(c *ui.Context) {
	s := a.batch.active.Summary()
	batchHeading(c, locales.BatchResults(), batchSource(s.Source))
	if s.Pending() == 0 {
		batchNotice(c, icons.CheckCircle, locales.BatchComplete(), pal().Success)
	} else {
		batchNotice(c, icons.Clock, locales.BatchPendingCount(s.Pending()), pal().Warning)
	}
	ui.Row(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		for _, m := range []struct {
			label string
			value int
		}{
			{locales.BatchAdded(), s.Added}, {locales.BatchFormats(), s.Formats}, {locales.BatchReplaced(), s.Replaced},
			{locales.BatchSkipped(), s.Duplicates + s.Skipped}, {locales.BatchFailed(), s.Failed}, {locales.BatchEnriched(), s.Enriched},
		} {
			batchCard(c.Key(m.label), func() {
				ui.Text(c, groupDigits(m.value)).FontSize(22).FontWeight(600)
				ui.Text(c, m.label).FontSize(13).TextColor(pal().TextSecondary)
			})
		}
	})
	batchCard(c, func() {
		batchHeading(c, locales.BatchRecords(), "")
		ui.Column(c).AlignItems(ui.Stretch).Children(func() {
			a.batchItems(c, true)
		})
	})
	ui.Row(c).Gap(theme.Space8).Children(func() {
		if widgets.Button(c, widgets.Secondary, nil, locales.BatchBack()).Clicked() {
			a.batch.mode = batchOverview
		}
		if widgets.Button(c, widgets.Primary, icons.Library, locales.CommonLibrary()).Clicked() {
			a.page = pageLibrary
		}
	})
}
