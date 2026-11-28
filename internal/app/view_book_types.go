package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
)

var bookTypeLabels = map[string]func() string{
	"fiction":                  locales.BookTypeFiction,
	"scienceFiction":           locales.BookTypeScienceFiction,
	"mystery":                  locales.BookTypeMystery,
	"romance":                  locales.BookTypeRomance,
	"literature":               locales.BookTypeLiterature,
	"women":                    locales.BookTypeWomen,
	"poetry":                   locales.BookTypePoetry,
	"drama":                    locales.BookTypeDrama,
	"socialSciencesHumanities": locales.BookTypeSocialSciencesHumanities,
	"nonfiction":               locales.BookTypeNonfiction,
	"psychology":               locales.BookTypePsychology,
	"history":                  locales.BookTypeHistory,
	"philosophy":               locales.BookTypePhilosophy,
	"biography":                locales.BookTypeBiography,
	"religionSpirituality":     locales.BookTypeReligionSpirituality,
	"scienceTechnology":        locales.BookTypeScienceTechnology,
	"personalGrowth":           locales.BookTypePersonalGrowth,
	"comics":                   locales.BookTypeComics,
}

func bookTypeLabel(t calibre.BookType) string {
	if label := bookTypeLabels[t.ID]; label != nil {
		return label()
	}
	return t.Name
}

func (a *App) bookTypeLabel(id string) string {
	if id == "" {
		return locales.BookTypesUncategorized()
	}
	for _, t := range a.bookTypes {
		if t.ID == id {
			return bookTypeLabel(t)
		}
	}
	return id
}

func (a *App) bookTypeDetails(ids []string) string {
	if len(ids) == 0 {
		return locales.BookTypesUncategorized()
	}
	labels := make([]string, 0, len(ids))
	for _, t := range a.bookTypes {
		if slices.Contains(ids, t.ID) {
			labels = append(labels, bookTypeLabel(t))
		}
	}
	return strings.Join(labels, " · ")
}

func (a *App) bookTypePicker() filterPicker {
	counts := make(map[string]int, len(a.counts.BookTypes))
	for _, count := range a.counts.BookTypes {
		counts[count.Value] = count.N
	}
	items := make([]catalog.Count, 0, len(a.bookTypes)+1)
	items = append(items, catalog.Count{N: a.counts.Uncategorized})
	labels := make(map[string]string, len(a.bookTypes)+1)
	labels[""] = locales.BookTypesUncategorized()
	for _, t := range a.bookTypes {
		items = append(items, catalog.Count{Value: t.ID, N: counts[t.ID]})
		labels[t.ID] = bookTypeLabel(t)
	}
	return filterPicker{
		kind: catalog.ByBookType, name: locales.BookTypesTitle(),
		placeholder: locales.BookTypesSearch(), label: locales.BookTypesSearch(),
		icon: icons.Layers, items: items, display: labels,
	}
}

type bookTypeDraft struct {
	definitions []calibre.BookType
	selected    []string
	created     []calibre.BookType
	addOpen     bool
	name        string
	err         string
}

func (d *bookTypeDraft) add() bool {
	t, err := calibre.NewCustomBookType(d.name)
	if err != nil {
		d.err = presentError(err)
		return false
	}
	key := calibre.BookTypeNameKey(t.Name)
	if key == calibre.BookTypeNameKey(locales.BookTypesUncategorized()) {
		d.err = locales.BookTypesInvalidName()
		return false
	}
	for _, existing := range d.definitions {
		if key == calibre.BookTypeNameKey(existing.Name) || key == calibre.BookTypeNameKey(bookTypeLabel(existing)) {
			d.err = locales.BookTypesDuplicate()
			return false
		}
	}
	d.definitions = append(d.definitions, t)
	d.created = append(d.created, t)
	d.selected = append(d.selected, t.ID)
	d.closeAdd()
	return true
}

func (d *bookTypeDraft) closeAdd() {
	d.addOpen, d.name, d.err = false, "", ""
}

func (d *bookTypeDraft) field(c *ui.Context) {
	widgets.Field(c, locales.BookTypesTitle(), locales.BookTypesHelp(), func() {
		if len(d.definitions) == 0 {
			ui.Text(c, locales.BookTypesUnavailable()).FontSize(12).TextColor(pal().TextTertiary)
			return
		}
		ui.Row(c).Wrap().Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			if widgets.CheckTag(c.Key("uncategorized"), locales.BookTypesUncategorized(), len(d.selected) == 0).Clicked() {
				d.selected = nil
			}
			for _, t := range d.definitions {
				index := slices.Index(d.selected, t.ID)
				if widgets.CheckTag(c.Key(t.ID), bookTypeLabel(t), index >= 0).Clicked() {
					if index >= 0 {
						d.selected = slices.Delete(d.selected, index, index+1)
					} else {
						d.selected = append(d.selected, t.ID)
					}
				}
			}
		})
		if d.addOpen {
			ui.Row(c.Key("create-type")).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
				if ui.TextInput(c, &d.name).Grow(1).MinWidth(0).AutoFocus().Label(locales.BookTypesName()).Placeholder(locales.BookTypesName()).Changed() {
					d.err = ""
				}
				if widgets.ButtonSized(c, widgets.Secondary, nil, locales.BookTypesCreate(), theme.ControlS).Disabled(strings.TrimSpace(d.name) == "").Clicked() {
					d.add()
				}
				if widgets.ButtonSized(c, widgets.Secondary, nil, locales.CommonCancel(), theme.ControlS).Clicked() {
					d.closeAdd()
				}
			})
			if d.err != "" {
				ui.Text(c, d.err).FontSize(12).TextColor(pal().Danger.FG)
			}
		} else if widgets.ButtonSized(c, widgets.Secondary, icons.Plus, locales.BookTypesAddCustom(), theme.ControlS).AlignSelf(ui.Start).Clicked() {
			d.addOpen = true
		}
	})
}
