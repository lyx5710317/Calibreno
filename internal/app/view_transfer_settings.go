package app

import (
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
)

type transferTarget int

const (
	transferKindle transferTarget = iota
	transferWeRead
	transferBOOX
	transferKOReader
)

func (a *App) openTransferSettings(target transferTarget) {
	a.transferTab = target
	a.openSettings(sectionTransfer)
}

func (a *App) transferSettingsPane(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Padding(theme.Space12, theme.Space20).Children(func() {
			tab := int(a.transferTab)
			ui.Tabs(c, &tab, locales.TransferKindle(), locales.TransferWeRead(), locales.TransferBOOX(), locales.TransferKOReader())
			a.transferTab = transferTarget(tab)
		})
		widgets.Hairline(c)
		switch a.transferTab {
		case transferKindle:
			a.emailPane(c.Key(a.transferTab))
		case transferWeRead:
			a.weReadSettingsPane(c.Key(a.transferTab))
		case transferBOOX:
			a.booxSettingsPane(c.Key(a.transferTab))
		case transferKOReader:
			a.wirelessPane(c.Key(a.transferTab))
		}
	})
}

func (a *App) kindleUSBSettings(c *ui.Context) {
	ui.Column(c.Key("kindle-usb")).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
		heading(c, locales.TransferKindleUSB(), locales.TransferKindleUSBHelp())
		hasKindle := false
		for _, d := range a.devices() {
			info := d.Info()
			if info.Kind == "kindle" {
				hasKindle = true
				ui.Text(c.Key(d.Storage().ID()), locales.TransferKindleUSBConnected(info.Name)).FontSize(12).TextColor(pal().Success.FG)
			}
		}
		if !hasKindle {
			ui.Text(c, locales.TransferKindleUSBNotConnected()).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
}
