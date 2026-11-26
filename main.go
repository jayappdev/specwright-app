// Command SpecWright is the desktop app: a Wails shell around core.Hub.
//
//	SpecWright             start the desktop app
//	SpecWright agentbridge run the agentbridge MCP server on stdio (started by the agent CLIs)
//	SpecWright aishim <cli> run claude/copilot for a tool and record the AI it uses
//	SpecWright devserver   serve the real backend to the browser preview (development)
//	SpecWright copilot-hook permission   Copilot CLI permission hook (asks in the app)
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
	if len(os.Args) > 2 && os.Args[1] == "copilot-hook" && os.Args[2] == "permission" {
		if err := extension.PermissionHook(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "copilot-hook:", err)
		}
		return // always exit 0: no output means Copilot's own default
	}
	if len(os.Args) > 1 && os.Args[1] == "devserver" {
		if err := devServer(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "devserver:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "tool" { // SpecWright's tools without MCP (agents' shell)
		os.Exit(extension.RunToolCLI(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "agentbridge" {
		if err := extension.ServeMCP(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "agentbridge:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "runner" { // a window for one flow (runner.go)
		if err := runnerMain(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "runner:", err)
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
