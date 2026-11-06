// Package ui provides terminal styling, banner formatting, animated spinners,
// and raw mode readline input handling for interactive CLI workflows.
package ui

import (
	"fmt"
	"os"
)

// ANSI color escape codes for terminal formatting
const (
	ColorReset  = "\033[0m"
	ColorBold   = "\033[1m"
	ColorCyan   = "\033[36m"
	ColorGray   = "\033[90m"
	ColorGreen  = "\033[32m"
	ColorRed    = "\033[31m"
	ColorYellow = "\033[33m"
)

const asciiLogo = `
    ____  __    _ ______          __   
   / __ \/ /_  (_) ____/___  ____/ /__ 
  / /_/ / __ \/ / /   / __ \/ __  / _ \
 / ____/ / / / / /___/ /_/ / /_/ /  __/
/_/   /_/ /_/_/\____/\____/\__,_/\___/ `

// PrintBanner displays session metadata (active model and working directory).
func PrintBanner(model string) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	fmt.Printf("%s%s%s%s\n\n", ColorBold, ColorCyan, asciiLogo, ColorReset)
	fmt.Printf("%sModel:    %s %s\n", ColorGray, ColorReset, model)
	fmt.Printf("%sDirectory:%s %s\n", ColorGray, ColorReset, cwd)
	fmt.Printf("%sCommands: %s Type your prompt or press Ctrl+C to exit.\n", ColorGray, ColorReset)
	fmt.Println()
}

