package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/bookconvert"
)

type conversionDialogState struct {
	open            bool
	title           string
	sources         []string
	formats         []string
	source, target  int
	layout          int
	pdfLayout       int
	pdfLanguage     int
	ocr             bool
	pages           string
	isBook          bool
	saveCopy        bool
	reviewAvailable bool
	err             string
}

type conversionDialogCallbacks struct {
	busy       bool
	progress   string
	targets    func(source string) []string
	submit     func()
	preview    func()
	review     func()
	cancelWork func()
}

func (s *conversionDialogState) selectedSource() string {
	return conversionChoice(s.sources, s.source)
}

func (s *conversionDialogState) selectedTarget(targets func(string) []string) string {
	return conversionChoice(targets(s.selectedSource()), s.target)
}

func (s *conversionDialogState) writesToLibrary(target string) bool {
	return s.isBook && !s.saveCopy && target != "" && !s.hasFormat(target)
}

func (s *conversionDialogState) hasFormat(format string) bool {
	return slices.Contains(s.formats, format) || slices.Contains(s.sources, format)
}

func (s *conversionDialogState) selectedPages() ([]int, error) {
	return bookconvert.ParsePages(strings.TrimSpace(s.pages))
}

func (s *conversionDialogState) requestOptions(target string) bookconvert.Request {
	readingDirections := []string{"horizontal", "vertical-rtl"}
	switch s.selectedSource() {
	case "PDF":
		return bookconvert.Request{
			SourceLayout: conversionChoice(readingDirections, s.pdfLayout),
			PDFLanguage:  conversionChoice([]string{"auto", "zh-Hant", "ja"}, s.pdfLanguage),
			OCR:          s.ocr,
		}
	case "EPUB":
		if target == "EPUB" {
			return bookconvert.Request{Layout: conversionChoice(readingDirections, s.layout)}
		}
	}
	return bookconvert.Request{}
}

func conversionDialog(c *ui.Context, s *conversionDialogState, cb conversionDialogCallbacks) {
	if !s.open {
		return
	}
	height := float32(250)
	source, target := s.selectedSource(), s.selectedTarget(cb.targets)
	switch {
	case source == "PDF" && target != "":
		height += 134
		if _, err := s.selectedPages(); err != nil {
			height += 24
		}
	case source == "EPUB" && target == "EPUB":
		height += 66
	}
	if s.isBook && target != "" {
		if s.hasFormat(target) {
			height += 32
		} else {
			height += 66
		}
	}
	if cb.busy {
		height += 36
	}
	if s.err != "" {
		height += 48
	}
	widgets.TallDialogFrame(c, &s.open, 600, height, func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, locales.ConversionTitle()).FontSize(16).FontWeight(600).SingleLine()
			ui.Text(c, s.title).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		})
	}, func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			conversionOptions(c, s, cb)
		})
	}, func() {
		target := s.selectedTarget(cb.targets)
		if s.selectedSource() == "PDF" && target != "" {
			pages, err := s.selectedPages()
			if widgets.Button(c, widgets.Secondary, nil, locales.ConversionPreviewPages()).
				Disabled(cb.busy || cb.preview == nil || err != nil || len(pages) == 0).Clicked() {
				cb.preview()
			}
			if s.reviewAvailable && cb.review != nil &&
				widgets.Button(c, widgets.Secondary, nil, locales.ConversionReviewPages()).Disabled(cb.busy).Clicked() {
				cb.review()
			}
		}
		ui.Spacer(c)
		if cb.busy {
			if widgets.Button(c, widgets.Secondary, nil, locales.ToolsStop()).Disabled(cb.cancelWork == nil).Clicked() {
				cb.cancelWork()
			}
		} else if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			s.open = false
		}
		if widgets.Button(c, widgets.Primary, icons.Replace, locales.ConversionSubmit()).
			Disabled(cb.busy || cb.submit == nil || target == "").Clicked() {
			cb.submit()
		}
	})
	if cb.busy {
		// Keep the cancellation control reachable until the job reports its result.
		s.open = true
	}
}

func conversionOptions(c *ui.Context, s *conversionDialogState, cb conversionDialogCallbacks) {
	ui.Column(c).Padding(theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
			widgets.Field(c, locales.ConversionSourceFormat(), "", func() {
				if conversionFormatSelect(c, &s.source, s.sources, locales.ConversionSourceFormat(), cb.busy) {
					s.target = 0
					s.err = ""
				}
			})
			targets := cb.targets(s.selectedSource())
			if len(targets) > 0 {
				widgets.Field(c, locales.ConversionTargetFormat(), "", func() {
					if conversionFormatSelect(c, &s.target, targets, locales.ConversionTargetFormat(), cb.busy) {
						s.err = ""
					}
				})
			}
		})
		target := s.selectedTarget(cb.targets)
		if target == "" {
			ui.Text(c, locales.ConversionUnsupported()).FontSize(12).TextColor(pal().TextSecondary)
		} else {
			switch s.selectedSource() {
			case "PDF":
				conversionPDFOptions(c, s, cb.busy)
			case "EPUB":
				if target == "EPUB" {
					widgets.Field(c, locales.ConversionLayout(), "", func() {
						conversionSelect(c, &s.layout, conversionDirections(), locales.ConversionLayout(), cb.busy)
					})
				}
			}
			conversionDestination(c, s, target, cb.busy)
		}
		if s.err != "" {
			ui.Row(c).Gap(theme.Space8).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Danger.FG)
				ui.Text(c, s.err).Grow(1).MinWidth(0).FontSize(12).TextColor(pal().Danger.FG)
			})
		}
		if cb.busy {
			ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				if cb.progress != "" {
					ui.Text(c, cb.progress).FontSize(12).TextColor(pal().TextSecondary).Grow(1).MinWidth(0)
				}
			})
		}
	})
}

func conversionPDFOptions(c *ui.Context, s *conversionDialogState, busy bool) {
	ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
		widgets.Field(c, locales.ConversionPDFLayout(), "", func() {
			conversionSelect(c, &s.pdfLayout, conversionDirections(), locales.ConversionPDFLayout(), busy)
		})
		widgets.Field(c, locales.ConversionPDFLanguage(), "", func() {
			languages := []string{locales.ConversionLanguageAuto(), locales.ConversionLanguageTraditional(), locales.ConversionLanguageJapanese()}
			conversionSelect(c, &s.pdfLanguage, languages, locales.ConversionPDFLanguage(), busy)
		})
	})
	ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
		widgets.Field(c, locales.ConversionPages(), "", func() {
			ui.TextInput(c, &s.pages).Label(locales.ConversionPages()).
				Placeholder(locales.ConversionPageRangeHint()).Disabled(busy)
			if _, err := s.selectedPages(); err != nil {
				ui.Text(c, locales.ConversionInvalidPageRange()).FontSize(12).TextColor(pal().Danger.FG)
			}
		})
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).Padding(theme.Space24, 0, 0, 0).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, &s.ocr, locales.ConversionOCR()).Disabled(busy)
		})
	})
}

func conversionDestination(c *ui.Context, s *conversionDialogState, target string, busy bool) {
	existing := s.isBook && s.hasFormat(target)
	if s.isBook && !existing {
		widgets.Field(c, locales.ConversionDestination(), "", func() {
			destinations := []string{locales.ConversionToLibrary(), locales.ConversionToCopy()}
			selected := 0
			if s.saveCopy {
				selected = 1
			}
			if conversionSelect(c, &selected, destinations, locales.ConversionDestination(), busy) {
				s.saveCopy = selected == 1
			}
		})
	} else if existing {
		ui.Text(c, locales.ConversionExistingFormat()).FontSize(12).TextColor(pal().TextSecondary)
	}
}

func conversionDirections() []string {
	return []string{locales.ConversionHorizontal(), locales.ConversionVerticalRTL()}
}

func conversionFormatSelect(c *ui.Context, index *int, formats []string, label string, busy bool) bool {
	if len(formats) != 1 {
		return conversionSelect(c, index, formats, label, busy)
	}
	ui.Row(c).Height(theme.ControlM).AlignItems(ui.Center).Children(func() {
		widgets.FormatBadge(c, formats[0], false)
	})
	return false
}

func conversionSelect(c *ui.Context, index *int, choices []string, label string, busy bool) bool {
	selected := conversionChoice(choices, *index)
	if ui.Select(c, &selected, choices).Label(label).Disabled(busy || len(choices) < 2).Changed() {
		if pos := slices.Index(choices, selected); pos >= 0 {
			*index = pos
			return true
		}
	}
	return false
}

func conversionChoice(choices []string, index int) string {
	if len(choices) == 0 {
		return ""
	}
	if index < 0 || index >= len(choices) {
		return choices[0]
	}
	return choices[index]
}
