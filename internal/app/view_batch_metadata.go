package app

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/batch"
	"dalibre/internal/plugin"
)

func (a *App) batchMetadataView(c *ui.Context) {
	b := &a.batch
	batchHeading(c, locales.BatchStartMetadata(), locales.BatchMetadataChoose())
	sources := a.metadataSources()
	var source *plugin.Plugin
	for _, p := range sources {
		if p.Manifest.ID == b.source || source == nil {
			source = p
		}
	}
	ids := a.batchSelection()
	disabled := len(ids) == 0 || b.work || b.loading
	ui.Row(c).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
		batchCard(c.Key("common"), func() {
			batchHeading(c, locales.BatchCommonTitle(), locales.BatchCommonHint())
			if widgets.Button(c, widgets.Secondary, nil, locales.BatchCommon()).Disabled(disabled).Clicked() {
				b.mode = batchCommon
			}
		})
		batchCard(c.Key("lookup"), func() {
			batchHeading(c, locales.BatchLookupTitle(), locales.BatchLookupHint())
			if source == nil {
				ui.Text(c, locales.FetchMetaNoPluginBody()).FontSize(13).TextColor(pal().TextSecondary)
				if widgets.Button(c, widgets.Secondary, icons.Settings, locales.FetchMetaOpenSettings()).Clicked() {
					a.openPlugins("")
				}
				return
			}
			button := widgets.Button(c, widgets.Secondary, icons.ChevronDown, pluginDisplayName(source.Manifest.ID, source.Manifest.Name)).Disabled(b.work)
			button.PopoverMenuButton(func(m *ui.Menu) {
				for _, p := range sources {
					if m.Item(pluginDisplayName(p.Manifest.ID, p.Manifest.Name)).ID(p.Manifest.ID).Radio(p == source).Chosen() {
						b.source = p.Manifest.ID
					}
				}
			})
			if widgets.Button(c, widgets.Primary, icons.Search, locales.BatchLookup()).Disabled(disabled).Clicked() {
				a.queryBatchMetadata(ids, source, false)
			}
		})
	})
	batchCard(c, func() {
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.DetailBooksSelected(len(ids))).Grow(1).FontSize(15).FontWeight(600)
			options := []string{locales.BatchNeedsMetadata(), locales.CommonShowAll(), locales.BatchMissingCover(), locales.BatchMissingISBN(), locales.BatchMissingDescription()}
			keys := []string{"", "all", "cover", "isbn", "description"}
			current := options[0]
			for i, k := range keys {
				if b.filter == k {
					current = options[i]
				}
			}
			if ui.Select(c, &current, options).Width(210).Label(locales.BatchFilter()).Disabled(b.work).Changed() {
				for i, label := range options {
					if current == label {
						b.filter = keys[i]
						break
					}
				}
			}
		})
		var visible []*batch.Item
		for i := range b.active.Items {
			item := &b.active.Items[i]
			if item.State == batch.Added && a.showBatchMetadataItem(item) {
				visible = append(visible, item)
			}
		}
		if len(visible) == 0 {
			ui.Text(c, locales.BatchNoPending()).FontSize(13).TextColor(pal().TextSecondary)
			return
		}
		all := true
		for _, item := range visible {
			all = all && b.selected[item.ID]
		}
		if ui.Checkbox(c.Key("all"), &all, locales.BatchSelectVisible()).Disabled(b.work).Changed() {
			for _, item := range visible {
				b.selected[item.ID] = all
			}
		}
		for _, item := range visible {
			key := c.Key(item.ID)
			ui.Row(key).MinHeight(38).Gap(theme.Space10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(pal().BorderSubtle).Children(func() {
				title := item.Metadata.Title
				if book := a.byID[item.Book.ID]; book != nil {
					title = book.Title
				}
				selected := b.selected[item.ID]
				if ui.Checkbox(key, &selected, "").Label(title).Disabled(b.work).Changed() {
					b.selected[item.ID] = selected
				}
				ui.Text(key, title).Grow(1).MinWidth(0).FontSize(13).SingleLine()
				var missing []string
				for _, entry := range []struct {
					needed bool
					key    string
				}{
					{item.NeedTitle, "title"}, {item.NeedAuthor, "authors"}, {item.NeedPublisher, "publisher"}, {item.NeedLanguage, "language"},
				} {
					if entry.needed && !item.Reviewed {
						missing = append(missing, batchFieldName(entry.key))
					}
				}
				ui.Text(key, strings.Join(missing, " · ")).FontSize(12).TextColor(pal().TextSecondary).SingleLine()
			})
		}
	})
	ui.Row(c).Gap(theme.Space8).Children(func() {
		if widgets.Button(c, widgets.Secondary, nil, locales.BatchLater()).Disabled(b.work).Clicked() {
			b.mode = batchOverview
		}
		if widgets.Button(c, widgets.Secondary, icons.Check, locales.BatchMarkReviewed()).Disabled(disabled).Clicked() {
			a.markBatchReviewed()
		}
	})
}

func (a *App) showBatchMetadataItem(item *batch.Item) bool {
	b := a.byID[item.Book.ID]
	switch a.batch.filter {
	case "all":
		return true
	case "cover":
		return b != nil && !b.HasCover
	case "isbn", "description":
		if b == nil {
			return false
		}
		d := a.detailsOf(b)
		return d != nil && (a.batch.filter == "isbn" && d.Identifiers["isbn"] == "" || a.batch.filter == "description" && d.Comments == "")
	default:
		return item.NeedsMetadata()
	}
}

func (a *App) batchCommonView(c *ui.Context) {
	b := &a.batch
	batchHeading(c, locales.BatchCommon(), locales.BatchCommonHint())
	batchCard(c, func() {
		widgets.Field(c, locales.CommonLanguage(), "", func() {
			codes := []string{"", "zho", "eng", "jpn", "fra", "deu", "kor", "spa", "ita", "por", "rus", "ara", "nld", "swe", "pol", "tur", "lat"}
			choices := []string{locales.BatchLeaveEmpty()}
			for _, code := range codes[1:] {
				choices = append(choices, languageName(code))
			}
			current := choices[0]
			if b.language != "" {
				current = languageName(b.language)
			}
			if ui.Select(c, &current, choices).Label(locales.CommonLanguage()).Disabled(b.work).Changed() {
				for i, name := range choices {
					if name == current {
						b.language = codes[i]
					}
				}
			}
		})
		widgets.Field(c, locales.CommonPublisher(), "", func() {
			ui.TextInput(c, &b.publisher).Label(locales.CommonPublisher()).Placeholder(locales.BatchLeaveEmpty()).Disabled(b.work)
		})
		widgets.Field(c, locales.CommonTags(), locales.BatchTagHint(), func() {
			ui.TextInput(c, &b.tags).Label(locales.CommonTags()).Placeholder(locales.BatchLeaveEmpty()).Disabled(b.work)
		})
	})
	ui.Row(c).Gap(theme.Space8).Children(func() {
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Disabled(b.work).Clicked() {
			b.mode = batchMetadata
		}
		empty := b.language == "" && strings.TrimSpace(b.publisher) == "" && strings.TrimSpace(b.tags) == ""
		if widgets.Button(c, widgets.Primary, nil, locales.BatchPreview()).Disabled(b.work || empty).Clicked() {
			a.previewBatchCommon()
		}
	})
}

func (a *App) batchPreviewPage(c *ui.Context) {
	ui.Column(c.Key(batchPreview)).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(theme.Space24, theme.Space32).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
			a.batchPreviewView(c)
		})
		a.batchPreviewActions(c)
	})
}

func (a *App) batchPreviewView(c *ui.Context) {
	b := &a.batch
	batchHeading(c, locales.BatchPreview(), locales.BatchPreviewHint())
	batchNotice(c, icons.ShieldCheck, locales.BatchFillEmpty(), theme.Pair{FG: pal().TextAccent, BG: pal().AccentSubtle})
	for _, id := range a.batchSelection() {
		if r := b.lookup[id]; r != nil {
			a.batchLookupRow(c.Key(id), id, r)
		}
	}
	batchCard(c, func() {
		ui.Row(c).Gap(theme.Space12).Children(func() {
			ui.Text(c, locales.BatchField()).Width(116).FontSize(13).TextColor(pal().TextSecondary)
			ui.Text(c, locales.BatchCurrentValue()).Grow(1).FontSize(13).TextColor(pal().TextSecondary)
			ui.Text(c, locales.BatchProposedValue()).Grow(1).FontSize(13).TextColor(pal().TextSecondary)
			ui.Text(c, locales.FetchMetaSource()).Width(120).FontSize(13).TextColor(pal().TextSecondary)
		})
		hasFields := false
		for i := range b.preview {
			e := &b.preview[i]
			if len(e.fields) == 0 {
				continue
			}
			hasFields = true
			key := c.Key(e.item)
			source := locales.BatchCommon()
			if e.source != nil {
				source = pluginDisplayName(e.source.Manifest.ID, e.source.Manifest.Name)
			}
			ui.Text(key, e.title).FontSize(14).FontWeight(600)
			for j := range e.fields {
				f := &e.fields[j]
				row := c.Key(e.item + ":" + f.key)
				ui.Row(row).MinHeight(38).Gap(theme.Space12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(pal().BorderSubtle).Children(func() {
					ui.Row(row).Width(116).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
						name := batchFieldName(f.key)
						ui.Checkbox(row, &f.selected, "").Label(name).Disabled(b.work)
						ui.Text(row, name).Grow(1).FontSize(13).SingleLine()
					})
					ui.Text(row, batchDisplayValue(f.key, f.before)).Grow(1).Basis(0).MinWidth(0).FontSize(13).Selectable()
					ui.Text(row, batchDisplayValue(f.key, f.after)).Grow(1).Basis(0).MinWidth(0).FontSize(13).Selectable()
					ui.Text(row, source).Width(120).FontSize(12).TextColor(pal().TextSecondary).SingleLine()
				})
			}
		}
		if !hasFields {
			ui.Text(c, locales.BatchNothingToApply()).FontSize(13).TextColor(pal().TextSecondary)
		}
	})
}

func (a *App) batchPreviewActions(c *ui.Context) {
	b := &a.batch
	count := 0
	for _, edit := range b.preview {
		for _, field := range edit.fields {
			if field.selected {
				count++
			}
		}
	}
	ui.Row(c).Padding(theme.Space12, theme.Space32).Gap(theme.Space8).AlignItems(ui.Center).
		Background(pal().Surface).BorderWidth(1, 0, 0, 0).BorderColor(pal().BorderSubtle).Children(func() {
		if widgets.Button(c, widgets.Secondary, nil, locales.BatchStartMetadata()).Disabled(b.work).Clicked() {
			b.mode = batchMetadata
		}
		if widgets.Button(c, widgets.Secondary, nil, locales.BatchLater()).Disabled(b.work).Clicked() {
			b.mode = batchOverview
		}
		ui.Spacer(c)
		if b.work {
			ui.Spinner(c).Size(14, 14).Label(a.busy)
		} else {
			ui.Text(c, locales.BatchSelectedFields(count)).FontSize(13).TextColor(pal().TextSecondary)
		}
		if widgets.Button(c, widgets.Primary, icons.Check, locales.BatchApplyMetadata()).Disabled(b.work || count == 0).Clicked() {
			a.applyBatchMetadata()
		}
	})
}

func batchDisplayValue(key, value string) string {
	if value == "" {
		return batchText(value)
	}
	if key == "cover" {
		return locales.FetchMetaCover()
	}
	values := strings.Split(value, "\x00")
	if key == "language" {
		for i, code := range values {
			values[i] = languageName(code)
		}
	}
	return strings.Join(values, " · ")
}

func (a *App) batchLookupRow(c *ui.Context, id string, result *batchLookup) {
	name := ""
	if result.book != nil {
		name = result.book.Title
	} else if item := a.batch.active.Item(id); item != nil {
		name = item.Metadata.Title
	}
	ui.Column(c).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
		a.batchCandidatePicker(c, id, name, result)
		if result.chosen >= 0 {
			p := result.candidates[result.chosen]
			ui.Row(c).Gap(theme.Space8).Wrap().AlignItems(ui.Center).Children(func() {
				for _, raw := range p.Fields {
					field := pluginDisplayField(result.source.Manifest.ID, raw)
					ui.Text(c, field.Label+" · "+field.Value).FontSize(12).TextColor(pal().TextSecondary)
				}
				if p.URL != "" {
					ui.Link(c, locales.GetBooksOpenBookPage(), p.URL).FontSize(12).TextColor(pal().TextAccent)
				}
			})
		}
	})
}

func (a *App) batchCandidatePicker(c *ui.Context, id, name string, result *batchLookup) {
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, name).Grow(1).MinWidth(125).FontSize(13).SingleLine()
		switch {
		case result.err != nil:
			if kind := errKind(result.err); kind == plugin.KindSettings || kind == plugin.KindAuth {
				if widgets.Button(c, widgets.Secondary, nil, locales.FetchMetaOpenSettings()).Clicked() {
					a.openPlugins(result.source.Manifest.ID)
				}
			}
			ui.Text(c, errText(result.err)).Grow(1).MinWidth(0).FontSize(13).TextColor(pal().Danger.FG)
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.ErrorDetails(), theme.ControlS).Clicked() {
				a.errorDetails, a.errorDetailsOpen = errorDetailsText(result.err), true
			}
		case len(result.candidates) == 0:
			ui.Text(c, locales.BatchNoMatch()).FontSize(13).TextColor(pal().TextSecondary)
		default:
			label := locales.BatchPickCandidate()
			if result.chosen >= 0 {
				label = batchCandidateLabel(result.candidates[result.chosen])
			}
			ui.Text(c, locales.BatchCandidateCount(len(result.candidates))).FontSize(12).TextColor(pal().TextSecondary)
			picker := widgets.Button(c, widgets.Secondary, icons.ChevronDown, label).MaxWidth(550).Shrink(1).
				Label(locales.BatchCandidateFor(name)).Disabled(a.batch.work)
			picker.PopoverMenuButton(func(m *ui.Menu) {
				for j, r := range result.candidates {
					if m.Item(batchCandidateLabel(r)).ID(r.ID).Radio(j == result.chosen).Chosen() && j != result.chosen {
						result.chosen = j
						a.makeBatchPreview(id)
					}
				}
			})
		}
		if result.err != nil || result.chosen < 0 {
			if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.CommonRetry()).Disabled(a.batch.work).Clicked() {
				a.queryBatchMetadata([]string{id}, result.source, true)
			}
		}
	})
}

func batchCandidateLabel(result plugin.Result) string {
	var parts []string
	if result.Publisher != "" {
		parts = append(parts, result.Publisher)
	}
	if date := plugin.ParseDate(result.Pubdate); !date.IsZero() {
		parts = append(parts, strconv.Itoa(date.Year()))
	}
	if isbn := result.Identifiers["isbn"]; isbn != "" {
		parts = append(parts, isbn)
	}
	if len(result.Authors) > 0 {
		parts = append(parts, strings.Join(result.Authors, ", "))
	}
	// Put the edition evidence first. Online catalog titles can be very long.
	return strings.Join(append(parts, result.Title), " · ")
}
