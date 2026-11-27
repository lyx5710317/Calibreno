package app

import (
	"dalibre/cmd/dalibre/locales"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
)

// dropOverlay marks the window as a drop target while files are dragged
// over it. The dragged paths aren't known until the drop, so it can't
// count them yet.
func (a *App) dropOverlay(c *ui.Context) {
	ui.Box(c).Absolute().Top(theme.Space12).Left(theme.Space12).Right(theme.Space12).Bottom(theme.Space12).PassThrough().
		Radius(theme.RadiusXL).Border(2, pal().Accent).BorderStyle(ui.BorderDashed).Background(pal().Accent.Alpha(0.06)).Center().Children(func() {
		ui.Column(c).Width(400).Center().Gap(theme.Space12).Padding(theme.Space24, theme.Space32).Radius(theme.RadiusLG).
			Background(pal().SurfaceRaised).Border(1, pal().BorderSubtle).Shadow(0, 2, 6, 0, ui.RGBA(26, 23, 18, 0.08)).Shadow(0, 24, 48, -8, ui.RGBA(26, 23, 18, 0.18)).Children(func() {
			ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().AccentSubtle).Center().Children(func() {
				ui.Icon(c, icons.Upload).FontSize(22).TextColor(pal().TextAccent)
			})
			ui.Text(c, locales.BatchDrop()).FontSize(18).FontWeight(600)
			hint := locales.BatchDropHint()
			if a.busy != "" {
				hint = locales.BatchDropQueue()
			}
			ui.Text(c, hint).FontSize(12).TextColor(pal().TextTertiary).TextAlign(ui.Center)
		})
	})
}
