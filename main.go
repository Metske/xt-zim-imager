package main

import (
	"embed"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed font-production/fallback_noto.bin
var embeddedFallbackNoto []byte

func isCLIMode() bool {
	if len(os.Args) <= 1 {
		return false
	}
	// macOS Finder may pass a Process Serial Number (-psn_0_...) when launching a .app bundle
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "-psn_") {
		return false
	}
	return true
}

func init() {
	if !isCLIMode() {
		f, err := os.OpenFile("crash.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err == nil {
			os.Stdout = f
			os.Stderr = f
		}
	}
}

func main() {
	if isCLIMode() {
		runCLI()
		return
	}

	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "xt-zim-imager",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
