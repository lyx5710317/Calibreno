package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/calibre"
)

// readingTransferMenu keeps all four destinations visible before setup.
func (a *App) readingTransferMenu(m *ui.Menu, ids []int64) {
	m.Submenu(locales.TransferMenu(), func(m *ui.Menu) {
		busy := a.busy != "" || a.loading
		m.Submenu(locales.TransferKindle(), func(m *ui.Menu) {
			hasUSB := false
			for _, d := range a.devices() {
				if d.Info().Kind != "kindle" {
					continue
				}
				hasUSB = true
				if m.Item(locales.TransferKindleUSBTo(d.Info().Name)).Disabled(busy || a.dev.busy != "").Chosen() {
					a.sendToReader(d, ids)
				}
			}
			if !hasUSB && m.Item(locales.TransferKindleUSB()).Chosen() {
				a.openTransferSettings(transferKindle)
			}
			if !a.emailMenuItems(m, ids) && m.Item(locales.TransferKindleEmail()).Chosen() {
				a.openTransferSettings(transferKindle)
			}
		})
		weread := m.Item(locales.TransferWeRead()).Disabled(busy || len(ids) != 1 || a.readingTransfers.preparing)
		if len(ids) != 1 {
			weread.Trailing(locales.TransferSingleBook())
		}
		if len(ids) == 1 && weread.Chosen() {
			if b := a.byID[ids[0]]; b != nil {
				a.prepareWeRead(b)
			}
		}
		if m.Item(locales.TransferBOOX()).Disabled(busy && a.settings.BOOXAddress != "").Chosen() {
			a.sendBooksToBOOX(ids, "")
		}
		dev := a.connectedDevice()
		connected := dev != nil && isKOReader(dev.Info().App)
		if m.Item(locales.TransferKOReader()).Disabled(connected && (busy || a.device.sending > 0)).Chosen() {
			if connected {
				a.sendToDevice(dev, ids)
			} else {
				a.openTransferSettings(transferKOReader)
			}
		}
	}).Icon(icons.Menu(icons.Send))
}

func isKOReader(app string) bool {
	return strings.EqualFold(app, "KOReader")
}

// The format menu only offers actions that use the file selected in that row.
func (a *App) readingFormatTransferMenu(m *ui.Menu, b *calibre.Book, format string) {
	boox := formatForBOOX(b.Formats, format) != ""
	weread := format == "EPUB" && slices.Contains(b.Formats, format)
	if !boox && !weread {
		return
	}
	m.Submenu(locales.TransferMenu(), func(m *ui.Menu) {
		busy := a.busy != "" || a.loading
		if boox && m.Item(locales.TransferBOOX()).Disabled(busy && a.settings.BOOXAddress != "").Chosen() {
			a.sendBooksToBOOX([]int64{b.ID}, format)
		}
		if weread && m.Item(locales.TransferWeRead()).Disabled(busy || a.readingTransfers.preparing).Chosen() {
			a.prepareWeRead(b)
		}
	}).Icon(icons.Menu(icons.Send))
}
