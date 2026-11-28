package app

import (
	_ "embed"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/app/theme"
)

//go:embed assets/sidebar-wordmark.png
var sidebarWordmarkPNG []byte

//go:embed assets/sidebar-wordmark-dark.png
var sidebarWordmarkDarkPNG []byte

var (
	sidebarWordmark     = decodeSidebarWordmark(sidebarWordmarkPNG)
	sidebarWordmarkDark = decodeSidebarWordmark(sidebarWordmarkDarkPNG)
)

func decodeSidebarWordmark(data []byte) *ui.Bitmap {
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		panic(err)
	}
	return bitmap
}

func sidebarBrand(c *ui.Context) {
	wordmark := sidebarWordmark
	if pal().Dark {
		wordmark = sidebarWordmarkDark
	}
	ui.Box(c).Padding(0, theme.Space16, theme.Space12, theme.Space16).DragWindow().Children(func() {
		ui.Image(c, wordmark).Size(152, 32).Fit(ui.Contain).Label(Name)
	})
}
