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
		var book *calibre.Book
		if len(ids) == 1 {
			book = a.byID[ids[0]]
		}
		a.weReadTransferMenu(m, book, busy)
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
		if weread {
			a.weReadTransferMenu(m, b, busy)
		}
	}).Icon(icons.Menu(icons.Send))
}

func (a *App) weReadTransferMenu(m *ui.Menu, book *calibre.Book, busy bool) {
	m.Submenu(locales.TransferWeRead(), func(m *ui.Menu) {
		disabled := busy || book == nil || a.weRead.busy
		send := m.Item(locales.WeReadSendAction()).Disabled(disabled)
		if book == nil {
			send.Trailing(locales.TransferSingleBook())
		}
		if send.Chosen() {
			a.prepareWeRead(book)
		}
		if m.Item(locales.WeReadManualAction()).Disabled(disabled).Chosen() {
			a.manualWeRead(book)
		}
		if m.Item(locales.WeReadSettings()).Chosen() {
			a.openTransferSettings(transferWeRead)
		}
	})
}
