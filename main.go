// Command SpecWright is the desktop app: a Wails shell around core.Hub.
//
//	SpecWright             start the desktop app
//	SpecWright agentbridge run the agentbridge MCP server on stdio (started by the agent CLIs)
//	SpecWright aishim <cli> run claude/copilot for a tool and record the AI it uses
package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"codegen/internal/aishim"
	"codegen/internal/extension"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if len(os.Args) > 1 && os.Args[1] == "aishim" {
		os.Exit(aishim.Main(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "agentbridge" {
		if err := extension.ServeMCP(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "agentbridge:", err)
			os.Exit(1)
		}
		return
	}
	app, err := NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, "codegen:", err)
		os.Exit(1)
	}
	err = wails.Run(&options.App{
		Title:            "SpecWright",
		Width:            1360,
		Height:           880,
		MinWidth:         900,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarDefault(),
			About:    &mac.AboutInfo{Title: "SpecWright", Message: "Turns specs into working code with Claude or GitHub Copilot."},
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "codegen:", err)
		os.Exit(1)
	}
}
