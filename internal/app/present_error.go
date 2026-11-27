package app

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net"
	"strings"
	"unicode"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/epub"
	"dalibre/internal/opds"
	"dalibre/internal/plugin"
)

func simplifiedChinese() bool { return i18n.Locale() == "zh-Hans" }

func presentError(err error) string {
	if err == nil {
		return ""
	}
	raw := err.Error()
	if !simplifiedChinese() {
		return raw
	}
	switch {
	case errors.Is(err, calibre.ErrBookChanged):
		return locales.BatchChanged()
	case errors.Is(err, calibre.ErrNotLibrary):
		return locales.ErrorNotLibrary()
	case errors.Is(err, epub.ErrDRM):
		return locales.ErrorDRM()
	case errors.Is(err, epub.ErrNotEPUB) || errors.Is(err, epub.ErrNotKF8):
		return locales.ErrorUnreadable()
	case errors.Is(err, opds.ErrNotOPDS):
		return locales.ErrorNotOPDS()
	case errors.Is(err, fs.ErrPermission):
		return locales.ErrorPermission()
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, sql.ErrNoRows):
		return locales.ErrorMissing()
	case errors.Is(err, context.Canceled):
		return locales.ErrorCancelled()
	case errors.Is(err, context.DeadlineExceeded):
		return locales.ErrorTimeout()
	}
	var pe *plugin.Error
	if errors.As(err, &pe) {
		if translated := pluginDisplayMessage(pe.Plugin, pe.Msg); translated != pe.Msg {
			return translated
		}
	}
	switch errKind(err) {
	case plugin.KindPermission:
		return locales.ErrorPermission()
	case plugin.KindTimeout:
		return locales.ErrorTimeout()
	case plugin.KindRateLimited:
		return locales.ErrorRateLimit()
	case plugin.KindNotFound:
		return locales.ErrorMissing()
	case plugin.KindAuth, plugin.KindSettings:
		return locales.BatchLookupSettings()
	case plugin.KindUnavailable:
		return locales.ErrorUnavailable()
	case plugin.KindInvalid:
		return locales.ErrorInvalid()
	case plugin.KindScript:
		return locales.ErrorPlugin()
	case plugin.KindCancelled:
		return locales.ErrorCancelled()
	}
	if startsWithHan(raw) {
		return raw
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return locales.ErrorTimeout()
		}
		return locales.ErrorUnavailable()
	}
	value := strings.ToLower(raw)
	switch {
	case strings.Contains(value, "sharing violation"), strings.Contains(value, "being used by another process"), strings.Contains(value, "database is locked"):
		return locales.ErrorBusy()
	case strings.Contains(value, "readonly database"), strings.Contains(value, "disk is full"):
		return locales.ErrorWrite()
	case strings.Contains(value, "timeout"), strings.Contains(value, "timed out"):
		return locales.ErrorTimeout()
	case strings.Contains(value, "epub:"), strings.Contains(value, "zip:"), strings.Contains(value, "invalid image"):
		return locales.ErrorUnreadable()
	}
	return locales.ErrorUnexpected()
}

func errorDetailsText(err error) string {
	raw := err.Error()
	var pe *plugin.Error
	if errors.As(err, &pe) && pe.Stack != "" {
		raw += "\n\n" + pe.Stack
	}
	return raw
}

func startsWithHan(s string) bool {
	for i, r := range s {
		if i > 48 || r == '/' || r == '\\' || r == ':' {
			return false
		}
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func (a *App) errorDetailsDialog(c *ui.Context) {
	if !a.errorDetailsOpen {
		return
	}
	widgets.DialogFrame(c, &a.errorDetailsOpen, 580, func() {
		ui.Text(c, locales.ErrorDetails()).Grow(1).FontSize(16).FontWeight(600)
	}, func() {
		ui.Scroll(c).MaxHeight(300).Padding(theme.Space24).AlignItems(ui.Stretch).Children(func() {
			ui.Text(c, a.errorDetails).FontSize(13).Selectable()
		})
	}, func() {
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.TechnicalCopy()).Clicked() {
			copyText(a.errorDetails)
		}
		if widgets.Button(c, widgets.Primary, nil, locales.CommonDone()).Clicked() {
			a.errorDetailsOpen = false
		}
	})
}
