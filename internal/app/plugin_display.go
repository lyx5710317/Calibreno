package app

import (
	"regexp"
	"slices"
	"strings"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/plugin"
)

// pluginDisplayField translates the source's field label and fixed formats.
func pluginDisplayField(id string, field plugin.Field) plugin.Field {
	label := field.Label
	field.Label = pluginDisplayText(id, label)
	switch label {
	case "Rating":
		field.Value = pluginDisplayMessage(id, field.Value)
	case "Rights", "Binding":
		field.Value = pluginDisplayText(id, field.Value)
	}
	return field
}

func pluginDisplayName(id, name string) string {
	if value := chinesePluginInfo[id].name; simplifiedChinese() && value != "" {
		return value
	}
	return name
}

func pluginDisplayDescription(id, description string) string {
	if value := chinesePluginInfo[id].description; simplifiedChinese() && value != "" {
		return value
	}
	return description
}

func pluginDisplayText(id, value string) string {
	if _, ok := chinesePluginInfo[id]; !simplifiedChinese() || !ok || value == "" {
		return value
	}
	if translated := chinesePluginStrings[value]; translated != "" {
		return translated
	}
	if prefix, tail, ok := strings.Cut(value, " ("); ok && strings.HasSuffix(tail, ")") {
		if translated := chinesePluginStrings[prefix]; translated != "" {
			return translated + "（" + strings.TrimSuffix(tail, ")") + "）"
		}
	}
	return value
}

func pluginDisplayManifest(m *plugin.Manifest) *plugin.Manifest {
	if _, ok := chinesePluginInfo[m.ID]; !simplifiedChinese() || !ok {
		return m
	}
	view := *m
	view.Name = pluginDisplayName(m.ID, m.Name)
	view.Description = pluginDisplayDescription(m.ID, m.Description)
	view.Capabilities.Actions = slices.Clone(m.Capabilities.Actions)
	for i := range view.Capabilities.Actions {
		act := &view.Capabilities.Actions[i]
		act.Title = pluginDisplayText(m.ID, act.Title)
	}
	view.Settings = slices.Clone(m.Settings)
	for i := range view.Settings {
		s := &view.Settings[i]
		s.Title = pluginDisplayText(m.ID, s.Title)
		s.Description = pluginDisplayText(m.ID, s.Description)
		s.Options = pluginDisplayOptions(m.ID, s.Options)
	}
	return &view
}

func pluginDisplayActionTitle(m *plugin.Manifest) string {
	title := m.ActionTitle()
	if title == m.Name {
		return pluginDisplayName(m.ID, title)
	}
	return pluginDisplayText(m.ID, title)
}

func pluginDisplayOptions(id string, values []plugin.Option) []plugin.Option {
	options := slices.Clone(values)
	for i := range options {
		o := &options[i]
		o.Title = pluginDisplayText(id, o.Title)
		o.Group = pluginDisplayText(id, o.Group)
		o.Badge = pluginDisplayMessage(id, o.Badge)
	}
	return options
}

func pluginDisplayForm(id string, f plugin.Form) plugin.Form {
	if _, ok := chinesePluginInfo[id]; !simplifiedChinese() || !ok {
		return f
	}
	f.Submit = pluginDisplayText(id, f.Submit)
	f.Note = pluginDisplayText(id, f.Note)
	f.Fields = slices.Clone(f.Fields)
	for i := range f.Fields {
		fd := &f.Fields[i]
		fd.Title = pluginDisplayText(id, fd.Title)
		fd.Options = pluginDisplayOptions(id, fd.Options)
	}
	return f
}

func pluginDisplayMissing(p *plugin.Plugin) []string {
	values := p.MissingSettings()
	for i, value := range values {
		values[i] = pluginDisplayText(p.Manifest.ID, value)
	}
	return values
}

func pluginCategoryTitle(cat plugin.Category) string {
	switch cat.ID {
	case "library":
		return locales.CommonLibrary()
	case "metadata":
		return locales.MarketMetadata()
	case "epub":
		return locales.MarketEpub()
	case "language":
		return locales.CommonLanguage()
	case "sources":
		return locales.MarketSources()
	default:
		return cat.Title
	}
}

// Published plugins include these summaries as English prose.
var pluginMessagePatterns = []struct {
	pattern *regexp.Regexp
	chinese string
}{
	{regexp.MustCompile("^Converted to KePub\\. (\\d+) already KePub\\.$"), "已转换为 KePub，另有 ${1} 本已有 KePub 格式。"},
	{regexp.MustCompile("^Converted (\\d+) books to KePub\\. (\\d+) already KePub\\.$"), "已将 ${1} 本书转换为 KePub，另有 ${2} 本已有 KePub 格式。"},
	{regexp.MustCompile("^Converted (\\d+) books to KePub\\.$"), "已将 ${1} 本书转换为 KePub。"},
	{regexp.MustCompile("^ePub updated\\. Skipped most options for DRM-encrypted books\\.$"), "已更新 EPUB。受 DRM 保护的书已跳过大部分选项。"},
	{regexp.MustCompile("^(\\d+) of (\\d+) ePubs updated\\. Skipped most options for DRM-encrypted books\\.$"), "已更新 ${2} 个 EPUB 中的 ${1} 个。受 DRM 保护的书已跳过大部分选项。"},
	{regexp.MustCompile("^(\\d+) of (\\d+) ePubs updated\\.$"), "已更新 ${2} 个 EPUB 中的 ${1} 个。"},
	{regexp.MustCompile("^#([^ ]+) · no date$"), "#${1} · 无出版日期"},
	{regexp.MustCompile("^That makes (\\d+) books; Dalibre adds at most 200 at a time\\.$"), "这次会生成 ${1} 本书，Calibreno 一次最多添加 200 本。"},
	{regexp.MustCompile("^Google Books answered (\\d{3})(?: [^.]*)?\\.$"), "Google 图书返回了 HTTP ${1}。"},
	{regexp.MustCompile("^Apple's search answered (\\d{3})\\.$"), "Apple Books 返回了 HTTP ${1}。"},
	{regexp.MustCompile("^([\\d.]+) / ([\\d.]+) \\(([\\d,]+) ratings?\\)$"), "${1} / ${2}（${3} 人评分）"},
	{regexp.MustCompile("^(NeoDB|Google Books) answered (\\d{3})(?: [^.]*)?\\.$"), "${1} 返回了 HTTP ${2}。"},
	{regexp.MustCompile("^Project Gutenberg answered (\\d{3})(?: [^.]*)?\\.$"), "古登堡计划返回了 HTTP ${1}。"},
	{regexp.MustCompile("^There is no list called (.+)\\.$"), "找不到名为“${1}”的书单。"},
	{regexp.MustCompile("^(.+) is no longer in the catalog\\.$"), "目录已不再提供《${1}》。"},
	{regexp.MustCompile("^([\\d,.]+) pages? · ([\\d,.]+) words?$"), "${1} 页 · ${2} 字"},
	{regexp.MustCompile("^(\\d+) of (\\d+) books have problems$"), "${2} 本书中有 ${1} 本需要检查"},
	{regexp.MustCompile("^(\\d+) groups?, (\\d+) books$"), "${1} 组 · ${2} 本书"},
	{regexp.MustCompile("^(\\d+) book\\(s\\) \\(out of (\\d+)\\) to update$"), "${2} 本书中有 ${1} 本待更新"},
	{regexp.MustCompile("^Forgot (\\d+) not-duplicate marks?$"), "已清除 ${1} 个不同书标记"},
	{regexp.MustCompile("^Marked (\\d+) books not duplicates$"), "已将 ${1} 本书标记为不同书"},
	{regexp.MustCompile("^Marked (\\d+) authors not duplicates$"), "已将 ${1} 位作者标记为不同作者"},
	{regexp.MustCompile("^Merged (\\d+) books into “(.+)”\\.$"), "已将 ${1} 本书合并为《${2}》。"},
	{regexp.MustCompile("^New book “(.+)” created\\.$"), "已创建《${1}》。"},
	{regexp.MustCompile("^(\\d+) New Books Created\\.$"), "已创建 ${1} 本书。"},
	{regexp.MustCompile("^(\\d+) books? out of their series$"), "已将 ${1} 本书移出原系列"},
	{regexp.MustCompile("^(\\d+) books? in (.+)$"), "已将 ${1} 本书加入系列 ${2}"},
	{regexp.MustCompile("^(\\d+) searches$"), "${1} 项搜索"},
}

func pluginDisplayMessage(id, value string) string {
	if _, ok := chinesePluginInfo[id]; !simplifiedChinese() || !ok || value == "" {
		return value
	}
	if translated := pluginDisplayText(id, value); translated != value {
		return translated
	}
	for _, entry := range pluginMessagePatterns {
		if entry.pattern.MatchString(value) {
			return entry.pattern.ReplaceAllString(value, entry.chinese)
		}
	}
	return value
}
