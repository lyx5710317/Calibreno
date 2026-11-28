package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/bookconvert"
	"dalibre/internal/boox"
	"dalibre/internal/calibre"
)

type readingTransfers struct {
	booxEdit booxSettingsState
	sending  bool

	probe    func(context.Context, string) (string, error)
	send     func(context.Context, string, string, string, string) (boox.Outcome, error)
	openURL  func(string) error
	reveal   func(string)
	copyPath func(string) error
}

func newReadingTransfers() readingTransfers {
	return readingTransfers{
		probe: boox.Probe, send: boox.Send,
		openURL: mygo.Shell.OpenExternal,
		reveal:  mygo.Shell.ShowItemInFolder,
		copyPath: func(path string) error {
			return mygo.Clipboard.Write(transfer.TextData(path))
		},
	}
}

func (a *App) booxSettingsPane(c *ui.Context) {
	state := &a.readingTransfers.booxEdit
	if state.savedAddress != a.settings.BOOXAddress {
		*state = booxSettingsState{address: a.settings.BOOXAddress, savedAddress: a.settings.BOOXAddress}
	}
	booxPane(c, state, booxSettingsActions{
		test:       a.testBOOXAddress,
		save:       a.saveBOOXAddress,
		remove:     func() { a.storeBOOXAddress("") },
		openUpload: func(address string) { a.openBOOXUpload(address, "") },
	})
}

func (a *App) saveBOOXAddress(raw string) {
	address, err := boox.NormalizeAddress(raw)
	if err != nil {
		a.setBOOXStatus(locales.BooxInvalidAddress(), true)
		return
	}
	a.storeBOOXAddress(address)
}

func (a *App) storeBOOXAddress(address string) {
	old := a.settings.BOOXAddress
	a.settings.BOOXAddress = address
	if err := a.settings.Save(); err != nil {
		a.settings.BOOXAddress = old
		a.setBOOXStatus(presentError(err), true)
		return
	}
	state := &a.readingTransfers.booxEdit
	state.address, state.savedAddress = address, address
	if address == "" {
		a.setBOOXStatus(locales.BooxAddressRemoved(), false)
	} else {
		a.setBOOXStatus(locales.BooxAddressSaved(), false)
	}
}

func (a *App) setBOOXStatus(message string, failed bool) {
	state := &a.readingTransfers.booxEdit
	state.status, state.failed = message, failed
}

func (a *App) testBOOXAddress(raw string) {
	state := &a.readingTransfers.booxEdit
	if state.testing {
		return
	}
	address, err := boox.NormalizeAddress(raw)
	if err != nil {
		a.setBOOXStatus(locales.BooxInvalidAddress(), true)
		return
	}
	state.testing = true
	a.setBOOXStatus("", false)
	probe := a.readingTransfers.probe
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		device, err := probe(ctx, address)
		a.post(func() {
			state.testing = false
			current, _ := boox.NormalizeAddress(state.address)
			if current != address {
				return
			}
			if err != nil {
				a.setBOOXStatus(booxMessage(err), true)
			} else if device != "" {
				a.setBOOXStatus(locales.BooxConnected(device), false)
			} else {
				a.setBOOXStatus(locales.BooxReady(), false)
			}
		})
	})
}

var booxFormatOrder = []string{"EPUB", "PDF", "MOBI", "AZW3", "FB2", "DJVU", "TXT"}

func formatForBOOX(formats []string, preferred string) string {
	if preferred != "" {
		if boox.Supports(preferred) && slices.Contains(formats, preferred) {
			return preferred
		}
		return ""
	}
	for _, format := range booxFormatOrder {
		if slices.Contains(formats, format) {
			return format
		}
	}
	return ""
}

func (a *App) sendBooksToBOOX(ids []int64, preferred string) {
	lib, t := a.lib, &a.readingTransfers
	if a.settings.BOOXAddress == "" {
		a.openTransferSettings(transferBOOX)
		return
	}
	if lib == nil || a.busy != "" || a.loading {
		return
	}
	address, err := boox.NormalizeAddress(a.settings.BOOXAddress)
	if err != nil {
		a.openTransferSettings(transferBOOX)
		t.booxEdit.address, t.booxEdit.savedAddress = a.settings.BOOXAddress, a.settings.BOOXAddress
		a.setBOOXStatus(locales.BooxInvalidAddress(), true)
		return
	}
	var books []*calibre.Book
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			books = append(books, b)
		}
	}
	if len(books) == 0 {
		return
	}
	send := t.send
	ctx, cancel := context.WithCancel(context.Background())
	t.sending = true
	a.busy, a.busyDone, a.busyTotal, a.busyStop = locales.BooxSending(), 0, len(books), cancel
	type failure struct {
		title string
		path  string
		err   error
	}
	a.run(func() {
		defer cancel()
		var sent, pending int
		var noFormat, uncertain []string
		var failures []failure
		for i, b := range books {
			if ctx.Err() != nil {
				pending = len(books) - i
				break
			}
			format := formatForBOOX(b.Formats, preferred)
			if format == "" {
				noFormat = append(noFormat, b.Title)
			} else if original, file, err := libraryFormatPaths(lib, b, format); err != nil {
				failures = append(failures, failure{title: b.Title, err: err})
			} else {
				name := b.Title
				if len(b.Authors) > 0 {
					name += " - " + b.Authors[0]
				}
				name = calibre.SafeFileName(name, 100)
				if name == "" {
					name = filepath.Base(original)
				} else {
					name += "." + strings.ToLower(format)
				}
				outcome, err := send(ctx, address, format, name, file)
				switch {
				case errors.Is(err, context.Canceled):
					pending = len(books) - i
				case err != nil:
					failures = append(failures, failure{title: b.Title, path: original, err: err})
					if errors.Is(err, boox.ErrIncompatible) || errors.Is(err, boox.ErrDisconnected) {
						pending = len(books) - i - 1
					}
				case outcome == boox.Sent:
					sent++
				default:
					uncertain = append(uncertain, b.Title)
				}
				if pending > 0 {
					break
				}
			}
			a.post(func() { a.busyDone = i + 1 })
		}
		a.post(func() {
			t.sending = false
			a.busy, a.busyDone, a.busyTotal, a.busyStop = "", 0, 0, nil
			if sent > 0 {
				a.toast("%s", locales.BooxSent(sent))
			}
			if len(noFormat) > 0 {
				a.toasts = append(a.toasts, toastMsg{text: locales.BooxNoFormat(strings.Join(noFormat, ", ")), error: true})
			}
			if len(uncertain) > 0 {
				a.toasts = append(a.toasts, toastMsg{text: locales.BooxUnconfirmed(strings.Join(uncertain, ", ")), error: true})
			}
			if len(failures) > 0 {
				f := failures[0]
				note := locales.BooxFailed(f.title, booxMessage(f.err))
				if len(failures) > 1 {
					note += " " + locales.BooxMoreFailed(len(failures)-1)
				}
				m := toastMsg{text: note, error: true}
				for _, failed := range failures {
					if failed.path != "" && (errors.Is(failed.err, boox.ErrIncompatible) || errors.Is(failed.err, boox.ErrRejected) || errors.Is(failed.err, boox.ErrDisconnected)) {
						m.action = locales.BooxOpenUpload()
						m.onAction = func() { a.openBOOXUpload(address, failed.path) }
						break
					}
				}
				a.toasts = append(a.toasts, m)
			}
			if pending > 0 {
				a.toast("%s", locales.BooxNotSent(pending))
			}
		})
	})
}

func booxMessage(err error) string {
	switch {
	case errors.Is(err, boox.ErrAddress):
		return locales.BooxInvalidAddress()
	case errors.Is(err, boox.ErrDisconnected), errors.Is(err, context.DeadlineExceeded):
		return locales.BooxDisconnected()
	case errors.Is(err, boox.ErrIncompatible):
		return locales.BooxIncompatible()
	case errors.Is(err, boox.ErrRejected):
		return locales.BooxRejected()
	case errors.Is(err, boox.ErrFileChanged), errors.Is(err, boox.ErrBadFileName):
		return locales.BooxInvalidFile()
	default:
		return presentError(err)
	}
}

func (a *App) openBOOXUpload(raw, file string) {
	address, err := boox.NormalizeAddress(raw)
	if err != nil {
		a.toasts = append(a.toasts, toastMsg{text: locales.BooxInvalidAddress(), error: true})
		return
	}
	open, reveal := a.readingTransfers.openURL, a.readingTransfers.reveal
	a.run(func() {
		if err := open(address); err != nil {
			a.post(func() { a.toastErrorSummary(locales.BooxPageFailed(), err) })
		} else if file != "" {
			reveal(file)
		}
	})
}

// libraryFormatPaths returns the book's path as shown to the user and the
// verified target for automatic transfers.
func libraryFormatPaths(lib *calibre.Library, book *calibre.Book, format string) (string, string, error) {
	details, err := lib.Details(book)
	if err != nil {
		return "", "", err
	}
	for _, file := range details.Files {
		if file.Format == format && file.Path != "" {
			original, err := filepath.Abs(file.Path)
			if err != nil {
				return "", "", err
			}
			resolved, err := lib.ResolveLibraryFile(original)
			return original, resolved, err
		}
	}
	return "", "", fmt.Errorf("%s %s: %w", book.Title, format, os.ErrNotExist)
}

func sourceForWeRead(formats []string) string {
	for _, source := range []string{"FB2", "TXT", "AZW3", "MOBI", "PDF"} {
		if slices.Contains(formats, source) && slices.Contains(bookconvert.OutputFormats(source), "EPUB") {
			return source
		}
	}
	return ""
}
