// Package ui provides terminal styling, banner formatting, animated spinners,
// and raw mode readline input handling for interactive CLI workflows.
package ui

import (
	"fmt"
	"os"
	"strings"
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

	// Palette for the custom Holographic PhiCode Brand (Authentic Mineral Turquoise)
	ColorPhiSolid      = "\033[1;38;2;48;225;195m"  // Opaque vibrant turquoise front face (#30E1C3)
	ColorPhiBevel      = "\033[38;2;24;145;130m"    // Deep teal-turquoise relief contour
	ColorCodeRelief    = "\033[1;38;2;48;225;195m"  // Vibrant turquoise for the 3D relief lines
	ColorCodeFrontDim  = "\033[38;2;24;95;85m"      // Soft translucent non-opaque turquoise front face
	ColorCodeGlow      = "\033[1;38;2;140;255;235m" // Luminous light-turquoise sweep beam
	ColorFlashWhite    = "\033[1;38;2;255;255;255m" // Power surge white
	ColorGlitchCyan    = "\033[1;38;2;40;190;170m"  // Quantum matrix turquoise
	ColorPreLightDim   = "\033[38;2;15;60;55m"      // Ambient dark turquoise before beam
)

var logoPhi = []string{
	"██████╗ ██╗  ██╗██╗",
	"██╔══██╗██║  ██║██║",
	"██████╔╝███████║██║",
	"██╔═══╝ ██╔══██║██║",
	"██║     ██║  ██║██║",
	"╚═╝     ╚═╝  ╚═╝╚═╝",
}

var logoCode = []string{
	"   ██████╗  ██████╗  ██████╗  ███████╗",
	"  ██╔════╝ ██╔═══██╗ ██╔══██╗ ██╔════╝",
	"  ██║      ██║   ██║ ██║  ██║ █████╗  ",
	"  ██║      ██║   ██║ ██║  ██║ ██╔══╝  ",
	"  ╚██████╗ ╚██████╔╝ ██████╔╝ ███████╗",
	"   ╚═════╝  ╚═════╝  ╚═════╝  ╚══════╝",
}

// RenderLogo renders the holographic brand mark.
// frame controls the temporary quantum materialization intro (0..7); -1 for permanent static state.
func RenderLogo(frame int) string {
	var sb strings.Builder

	for row := 0; row < 6; row++ {
		phiStr := logoPhi[row]
		codeStr := logoCode[row]

		// 1. Render PHI (Opaque front face in turquoise, with subtle relief shadow)
		for _, r := range []rune(phiStr) {
			if r == ' ' {
				sb.WriteRune(' ')
				continue
			}

			switch {
			case frame == 0:
				glitchRunes := []rune{'░', '▒', '▓', '▄', '▀', '█'}
				sb.WriteString(ColorGlitchCyan)
				sb.WriteRune(glitchRunes[(row*7)%len(glitchRunes)])
			case frame == 1 || frame == 2:
				sb.WriteString(ColorFlashWhite)
				sb.WriteRune(r)
			default:
				if r == '█' {
					sb.WriteString(ColorPhiSolid)
				} else {
					sb.WriteString(ColorPhiBevel)
				}
				sb.WriteRune(r)
			}
		}

		// 2. Render CODE (Translucent front face, vibrant turquoise on the 3D relief)
		if frame >= 0 && frame < 2 {
			sb.WriteString(ColorReset + "\n")
			continue
		}

		codeRunes := []rune(codeStr)
		for cIdx, r := range codeRunes {
			if r == ' ' {
				sb.WriteRune(' ')
				continue
			}

			if frame >= 2 && frame <= 6 {
				beamCol := (frame - 2) * 8
				dist := cIdx - beamCol
				switch {
				case dist >= 0 && dist <= 6:
					sb.WriteString(ColorCodeGlow)
					sb.WriteRune(r)
				case dist < 0:
					if r == '█' {
						sb.WriteString(ColorCodeFrontDim)
					} else {
						sb.WriteString(ColorCodeRelief)
					}
					sb.WriteRune(r)
				default:
					sb.WriteString(ColorPreLightDim)
					sb.WriteRune(r)
				}
			} else {
				if r == '█' {
					sb.WriteString(ColorCodeFrontDim)
				} else {
					sb.WriteString(ColorCodeRelief)
				}
				sb.WriteRune(r)
			}
		}

		sb.WriteString(ColorReset + "\n")
	}

	return sb.String()
}

// RenderBanner returns the complete session header banner including the logo and session metadata.
func RenderBanner(model string, frame int) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	var sb strings.Builder
	sb.WriteString(RenderLogo(frame))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%sModel:    %s %s\n", ColorGray, ColorReset, model))
	sb.WriteString(fmt.Sprintf("%sDirectory:%s %s\n", ColorGray, ColorReset, cwd))
	sb.WriteString(fmt.Sprintf("%sCommands: %s Type your prompt or press Ctrl+C to exit.\n", ColorGray, ColorReset))
	sb.WriteString("\n")
	return sb.String()
}

// PrintBanner outputs the static banner to standard output.
func PrintBanner(model string) {
	fmt.Print(RenderBanner(model, -1))
}
