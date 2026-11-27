package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/bookconvert"
)

type pdfReviewState struct {
	open         bool
	title        string
	summary      bookconvert.PDFWorkspaceSummary
	page         int
	record       bookconvert.PDFPage
	preview      *ui.Bitmap
	loading      bool
	loadError    string
	previewError string
	zoom         int
	jump         string
	rerunPages   string
	rerunError   string
}

func (s *pdfReviewState) nextReviewPage() int {
	for _, page := range s.summary.ReviewPages {
		if page > s.page {
			return page
		}
	}
	if pages := s.summary.ReviewPages; len(pages) > 0 && pages[0] != s.page {
		return pages[0]
	}
	return 0
}

func pdfReviewDialog(c *ui.Context, s *pdfReviewState, busy bool, progress string, load func(int), rerun func([]int), back, stop func()) {
	if !s.open {
		return
	}
	widgets.TallDialogFrame(c, &s.open, 940, 660, func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, locales.ConversionReviewPages()).FontSize(16).FontWeight(600).SingleLine()
			if s.title != "" {
				ui.Text(c, s.title).FontSize(12).SingleLine().TextColor(pal().TextSecondary)
			}
		})
	}, func() {
		ui.Column(c).Grow(1).MinHeight(0).Padding(theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			pdfReviewNavigation(c, s, busy, load)
			ui.Row(c).Grow(1).MinHeight(0).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
				pdfReviewOriginal(c.Key("original"), s)
				pdfReviewText(c.Key("text"), s)
			})
			if s.rerunError != "" {
				ui.Text(c, s.rerunError).FontSize(12).TextColor(pal().Danger.FG)
			}
		})
	}, func() {
		if widgets.Button(c, widgets.Secondary, icons.ChevronLeft, locales.ConversionPDFBack()).Disabled(busy).Clicked() {
			s.open = false
		}
		ui.Spacer(c)
		if busy {
			ui.Spinner(c).Size(16, 16).Label(locales.CommonLoading())
			if progress != "" {
				ui.Text(c, progress).Shrink(1).MinWidth(0).FontSize(12).SingleLine().TextColor(pal().TextSecondary)
			}
			if widgets.Button(c, widgets.Secondary, nil, locales.ActionStop(locales.ConversionPDFRerun())).Clicked() {
				stop()
			}
			return
		}
		startRerun := func() {
			if s.page < 1 {
				return
			}
			pages := []int{s.page}
			if spec := strings.TrimSpace(s.rerunPages); spec != "" {
				var err error
				pages, err = bookconvert.ParsePages(spec)
				if err != nil || len(pages) == 0 || pages[len(pages)-1] > s.summary.Total {
					s.rerunError = locales.ConversionInvalidPageRange()
					return
				}
			}
			s.rerunError = ""
			rerun(pages)
		}
		ui.Text(c, locales.ConversionPDFRerunRange()).FontSize(12).TextColor(pal().TextSecondary)
		ui.TextInput(c, &s.rerunPages).Width(140).Label(locales.ConversionPDFRerunRange()).Placeholder(strconv.Itoa(s.page)).OnSubmit(startRerun)
		if widgets.Button(c, widgets.Primary, icons.Refresh, locales.ConversionPDFRerun()).Disabled(s.page < 1).Clicked() {
			startRerun()
		}
	})
	if !s.open {
		// Keep the dialog visible with its stop button while a job is running.
		if busy {
			s.open = true
		} else {
			back()
		}
	}
}

func pdfReviewNavigation(c *ui.Context, s *pdfReviewState, busy bool, load func(int)) {
	navigate := func(page int) {
		s.jump, s.rerunError = "", ""
		load(page)
	}
	ui.Row(c).AlignItems(ui.Center).Gap(theme.Space8).Children(func() {
		if widgets.IconButton(c, icons.ChevronLeft, locales.ConversionPDFPreviousPage(), false).Disabled(busy || s.page <= 1).Clicked() {
			navigate(s.page - 1)
		}
		ui.Text(c, locales.ConversionPDFPagePosition(s.page, s.summary.Total)).FontSize(13).FontWeight(600).NoWrap()
		if widgets.IconButton(c, icons.ChevronRight, locales.ConversionPDFNextPage(), false).Disabled(busy || s.page >= s.summary.Total).Clicked() {
			navigate(s.page + 1)
		}
		msg, fg, bg := locales.ConversionPDFRecognized(), pal().TextSecondary, pal().Subtle
		if _, saved := slices.BinarySearch(s.summary.Saved, s.page); !saved {
			msg, fg, bg = locales.ConversionPDFNotProcessed(), pal().Warning.FG, pal().Warning.BG
		} else if _, review := slices.BinarySearch(s.summary.ReviewPages, s.page); review {
			msg, fg, bg = locales.ConversionPDFNeedsReview(), pal().Warning.FG, pal().Warning.BG
		}
		ui.Box(c).Padding(theme.Space2, theme.Space6).Radius(theme.RadiusXS).Background(bg).Children(func() {
			ui.Text(c, msg).FontSize(12).TextColor(fg).NoWrap()
		})
		ui.Spacer(c)
		next := s.nextReviewPage()
		if next != 0 && widgets.Button(c, widgets.Secondary, nil, locales.ConversionPDFNextReview()).Disabled(busy).Clicked() {
			navigate(next)
		}
		jump := func() {
			pages, err := bookconvert.ParsePages(strings.TrimSpace(s.jump))
			if !busy && err == nil && len(pages) == 1 && pages[0] <= s.summary.Total && pages[0] != s.page {
				navigate(pages[0])
			}
		}
		pageNumber := locales.ConversionPDFPageNumber()
		ui.TextInput(c, &s.jump).Width(112).Label(pageNumber).Placeholder(pageNumber).Disabled(busy).OnSubmit(jump)
		if widgets.Button(c, widgets.Secondary, nil, locales.ConversionPDFGoToPage()).Disabled(busy || s.jump == "").Clicked() {
			jump()
		}
	})
}

func pdfReviewOriginal(c *ui.Context, s *pdfReviewState) {
	ui.Column(c).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Gap(theme.Space6).Children(func() {
		ui.Row(c).Height(32).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.ConversionPDFOriginal()).FontSize(12).FontWeight(500).TextColor(pal().TextSecondary).Grow(1)
			choices := []string{locales.ConversionPDFFit(), "150%", "200%"}
			value := choices[s.zoom]
			if ui.Select(c, &value, choices).Width(110).Label(locales.ConversionPDFZoom()).Disabled(s.preview == nil).Changed() {
				if selected := slices.Index(choices, value); selected >= 0 {
					s.zoom = selected
				}
			}
		})
		pane := ui.Box(c).Grow(1).MinHeight(0).Center().Clip().Radius(theme.RadiusMD).Border(1, pal().BorderSubtle).Background(pal().Subtle)
		pane.Children(func() {
			switch {
			case s.preview != nil && s.zoom == 0:
				ui.Image(c, s.preview).Fill().Fit(ui.Contain)
			case s.preview != nil:
				viewport := pane.Bounds()
				if viewport.W <= 0 || viewport.H <= 0 {
					viewport.W, viewport.H = 400, 280
				}
				viewWidth, viewHeight := max(1, viewport.W-2), max(1, viewport.H-2)
				w, h := s.preview.Size()
				fit := min(viewWidth/float32(w), viewHeight/float32(h))
				scale := fit * (1 + float32(s.zoom)*0.5)
				width, height := float32(w)*scale, float32(h)*scale
				ui.ScrollBoth(c.Key(s.page)).Fill().Children(func() {
					ui.Box(c).Width(max(width, viewWidth)).Height(height).AlignItems(ui.Center).Children(func() {
						ui.Image(c, s.preview).Size(width, height).Fit(ui.Contain)
					})
				})
			case s.loading:
				ui.Spinner(c).Size(18, 18).Label(locales.CommonLoading())
			default:
				message := s.previewError
				if message == "" {
					message = locales.ConversionPDFPreviewUnavailable()
				}
				ui.Text(c, message).TextAlign(ui.Center).Padding(theme.Space16).FontSize(13).TextColor(pal().TextTertiary)
			}
		})
	})
}

func pdfReviewText(c *ui.Context, s *pdfReviewState) {
	ui.Column(c).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Gap(theme.Space6).Children(func() {
		ui.Row(c).Height(32).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.ConversionPDFRecognizedText()).FontSize(12).FontWeight(500).TextColor(pal().TextSecondary)
		})
		ui.Scroll(c.Key(s.page)).Grow(1).MinHeight(0).Padding(theme.Space16).Radius(theme.RadiusMD).Border(1, pal().BorderSubtle).Background(pal().Surface).Children(func() {
			switch {
			case s.loadError != "":
				ui.Text(c, s.loadError).FontSize(13).TextColor(pal().Danger.FG).Selectable()
			case s.record.Number == s.page && s.record.Available:
				if s.record.Text == "" {
					ui.Text(c, locales.ConversionPDFNoRecognizedText()).FontSize(13).TextColor(pal().TextTertiary)
				} else {
					ui.Text(c, s.record.Text).FontSize(13).LineHeight(1.55).Selectable()
				}
				if len(s.record.LowConfidence) > 0 {
					ui.Column(c).Padding(theme.Space12, 0, 0).Gap(theme.Space4).Children(func() {
						ui.Text(c, locales.ConversionPDFLowConfidence()).FontSize(12).FontWeight(500).TextColor(pal().Warning.FG)
						ui.Text(c, strings.Join(s.record.LowConfidence, "\n")).FontSize(12).Selectable()
					})
				}
			case s.loading:
				ui.Spinner(c).Size(18, 18).Label(locales.CommonLoading())
			default:
				ui.Text(c, locales.ConversionPDFNotProcessed()).FontSize(13).TextColor(pal().TextTertiary)
			}
		})
	})
}
