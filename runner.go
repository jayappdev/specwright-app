package main

// `SpecWright runner --workspace DIR --flow ID [--agent NAME] [--model M]`:
// a window of its own for one flow (core/runner.go). The main window opens
// these with "Open in its own window".

import (
	"flag"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"codegen/internal/core"
	"codegen/internal/flow"
)

func runnerMain(args []string) error {
	fs := flag.NewFlagSet("runner", flag.ContinueOnError)
	ws := fs.String("workspace", "", "the project folder")
	id := fs.String("flow", "", "the flow to run")
	agentName := fs.String("agent", "", "claude, copilot or mock")
	model := fs.String("model", "", "the model")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ws == "" || *id == "" {
		return fmt.Errorf("give --workspace and --flow")
	}
	f, err := flow.Load(*ws, *id)
	if err != nil {
		return fmt.Errorf("flow %s: %v", *id, err)
	}
	app, err := newAppWith(core.Options{Runner: *id})
	if err != nil {
		return err
	}
	if *agentName != "" {
		token := os.Getenv(core.RunnerTokenEnv)
		os.Unsetenv(core.RunnerTokenEnv) // not for the tools it starts
		if err := app.hub.SetAgent(*agentName, token, *model); err != nil {
			fmt.Fprintln(os.Stderr, "runner: agent:", err)
		}
	}
	if _, err := app.hub.OpenWorkspace(*ws); err != nil {
		return err
	}
	return wails.Run(&options.App{
		Title:            f.Name + " · SpecWright",
		Width:            1040,
		Height:           820,
		MinWidth:         720,
		MinHeight:        560,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		Mac:              &mac.Options{TitleBar: mac.TitleBarDefault()},
	})
}
