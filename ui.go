package main

import (
	"fmt"
	"os"
)

// ANSI color escape codes for terminal formatting
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
)

// printBanner displays session metadata (active model and working directory).
func printBanner(provider Provider) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	fmt.Printf("%s%s━━━ Phi Agent Code ━━━%s\n", colorBold, colorRed, colorReset)
	fmt.Printf("%sModel:    %s %s\n", colorGray, colorReset, provider.Model())
	fmt.Printf("%sDirectory:%s %s\n", colorGray, colorReset, cwd)
	fmt.Printf("%sCommands: %s Type your prompt or press Ctrl+C to exit.", colorGray, colorReset)
	fmt.Println()
}
