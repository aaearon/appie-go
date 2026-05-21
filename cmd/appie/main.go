package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	appie "github.com/gwillem/appie-go"
	"github.com/jessevdk/go-flags"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

var globalOpts struct {
	Config  string `short:"c" long:"config" description:"Path to config file"`
	Verbose bool   `short:"v" long:"verbose" description:"Verbose output"`
	JSON    bool   `short:"j" long:"json" description:"Emit machine-readable JSON envelopes on stdout"`

	Login   loginCommand        `command:"login" description:"Login to Albert Heijn"`
	Search  searchCommand       `command:"search" description:"Search for products"`
	Product productCommand      `command:"product" description:"Look up product(s) by webshop ID"`
	Receipt receiptCommand      `command:"receipt" subcommands-optional:"true" description:"List recent receipts"`
	Order   orderCommand        `command:"order" subcommands-optional:"true" description:"List open orders"`
	List    shoppingListCommand `command:"list" subcommands-optional:"true" description:"Show shopping lists"`
	Koopjes koopjesCommand      `command:"koopjes" description:"Show last-chance bargains at a store"`
	Update  updateCommand       `command:"update" description:"Update appie to the latest version"`
}

func clientOpts() []appie.Option {
	opts := []appie.Option{appie.WithConfigPath(globalOpts.Config)}
	if globalOpts.Verbose {
		opts = append(opts, appie.WithLogger(log.New(os.Stderr, "", log.Ltime)))
	}
	return opts
}

func defaultConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ".appie.json"
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "appie", "config.json")
}

// handleParseError classifies and emits a command error, then exits.
func handleParseError(err error) {
	if flags.WroteHelp(err) {
		os.Exit(0)
	}

	var fe *flags.Error
	var code string
	var exit int

	if errors.As(err, &fe) {
		// Argument / parse problem.
		code, exit = "bad_args", exitUserError
	} else {
		code, exit = errorCode(err)
	}

	if globalOpts.JSON {
		var amb *ambiguousError
		if errors.As(err, &amb) {
			_ = emitErrorDetails(err, "ambiguous", map[string]any{"candidates": amb.candidates})
		} else {
			_ = emitError(err, code)
		}
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(exit)
}

// wantsJSON pre-scans os.Args for -j / --json so parse errors (which fire
// before flags assigns globalOpts.JSON) still produce JSON envelopes.
func wantsJSON() bool {
	for _, a := range os.Args[1:] {
		if a == "-j" || a == "--json" {
			return true
		}
	}
	return false
}

func main() {
	if globalOpts.Config == "" {
		globalOpts.Config = defaultConfigPath()
	}

	// Set JSON mode from a raw-args scan before flags parsing, so an
	// unknown-flag error reported by the parser still gets emitted as an
	// envelope rather than plain stderr.
	if wantsJSON() {
		globalOpts.JSON = true
	}

	// Drop flags.PrintErrors so we can format errors ourselves (JSON envelope
	// vs. plain stderr) based on --json.
	p := flags.NewParser(&globalOpts, flags.HelpFlag|flags.PassDoubleDash)
	if _, err := p.Parse(); err != nil {
		handleParseError(err)
	}
}

func init() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-V" {
			if wantsJSON() {
				_ = writeJSON(os.Stdout, envelope{
					OK:   true,
					Data: map[string]any{"version": version},
				})
			} else {
				fmt.Println("appie", version)
			}
			os.Exit(0)
		}
	}
}
