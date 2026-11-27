package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
)

type booxSettingsState struct {
	address, savedAddress string
	status                string
	testing, failed       bool
}

type booxSettingsActions struct {
	test, save, openUpload func(address string)
	remove                 func()
}

func booxPane(c *ui.Context, s *booxSettingsState, actions booxSettingsActions) {
	pane(c, func() { booxSettingsGroup(c, s, actions) })
}

func booxSettingsGroup(c *ui.Context, s *booxSettingsState, actions booxSettingsActions) {
	ui.Column(c.Key("boox")).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		heading(c, locales.BooxName(), locales.BooxSetupBody())
		labeled(c, locales.BooxAddress(), func() {
			if ui.TextInput(c, &s.address).Label(locales.BooxAddress()).Placeholder("http://192.168.1.20:8085").Disabled(s.testing).Changed() {
				s.status, s.failed = "", false
			}
		})
		address := strings.TrimSpace(s.address)
		ready := address != "" && !s.testing
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Wrap().Children(func() {
			if widgets.ButtonSized(c, widgets.Primary, nil, locales.SettingsApply(), theme.ControlS).
				Disabled(!ready || address == s.savedAddress || actions.save == nil).Clicked() {
				actions.save(address)
			}
			if widgets.ButtonSized(c, widgets.Secondary, icons.Wifi, locales.BooxTestConnection(), theme.ControlS).
				Disabled(!ready || actions.test == nil).Clicked() {
				actions.test(address)
			}
			if s.savedAddress != "" && widgets.ButtonSized(c, widgets.Ghost, icons.Trash, locales.CommonRemove(), theme.ControlS).
				Disabled(s.testing || actions.remove == nil).Clicked() {
				actions.remove()
			}
		})
		if s.testing || s.status != "" {
			ui.Row(c).Padding(theme.Space8, theme.Space12).Gap(theme.Space8).Radius(theme.RadiusSM).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
				message, tint := s.status, pal().Success.FG
				switch {
				case s.testing:
					message, tint = locales.BooxTesting(), pal().TextSecondary
					ui.Spinner(c).Size(14, 14).Label(message)
				case s.failed:
					tint = pal().Danger.FG
					ui.Icon(c, icons.Alert).FontSize(14).TextColor(tint)
				default:
					ui.Icon(c, icons.CheckCircle).FontSize(14).TextColor(tint)
				}
				ui.Text(c, message).Grow(1).MinWidth(0).FontSize(12).TextColor(tint)
			})
		}
		widgets.Hairline(c)
		ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.BooxUploadHelp()).Grow(1).MinWidth(0).FontSize(12).TextColor(pal().TextSecondary)
			if widgets.ButtonSized(c, widgets.Secondary, icons.External, locales.BooxOpenUpload(), theme.ControlS).
				Disabled(!ready || actions.openUpload == nil).Clicked() {
				actions.openUpload(address)
			}
		})
	})
}
