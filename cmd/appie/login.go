package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"

	appie "github.com/gwillem/appie-go"
)

type loginCommand struct{}

func (cmd *loginCommand) Execute(args []string) error {
	configPath := globalOpts.Config

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	opts := clientOpts()
	if globalOpts.JSON {
		// Route the URL to stderr (the JSON envelope owns stdout) AND launch
		// the browser ourselves — overriding WithOpenBrowser suppresses the
		// library's default launcher, so we have to replicate it here.
		opts = append(opts, appie.WithOpenBrowser(func(url string) {
			fmt.Fprintln(os.Stderr, url)
			openBrowser(url)
		}))
	}

	client := appie.New(opts...)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if !globalOpts.JSON {
		fmt.Println("Opening browser for AH login. If it doesn't open, visit:")
	}

	if err := client.Login(ctx); err != nil {
		return fmt.Errorf("login failed: %w: %w", err, errUpstream)
	}

	if globalOpts.JSON {
		return emitJSON(map[string]any{
			"authenticated": true,
			"config_path":   configPath,
		}, nil)
	}

	fmt.Printf("Login successful! Tokens saved to %s\n", configPath)
	fmt.Println("After you have been authorized, the access keys will be automatically refreshed.")
	return nil
}

// openBrowser mirrors the library's default browser launcher (which is bypassed
// when WithOpenBrowser is provided).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}
