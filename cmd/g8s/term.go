package main

import (
	"os"

	"github.com/pterm/pterm"
	"golang.org/x/term"
)

var noColor = os.Getenv("NO_COLOR") != "" ||
	!term.IsTerminal(int(os.Stdout.Fd()))

func init() {
	if noColor {
		pterm.DisableColor()
		pterm.DisableStyling()
	}
}
