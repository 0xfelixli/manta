package cli

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Oceanic Mineral CLI styles (lipgloss automatically strips ANSI when stdout is not a TTY).
var (
	cliBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}).
			Background(lipgloss.AdaptiveColor{Light: "#CCFBF1", Dark: "#134E4A"}).
			Padding(0, 1)

	cliOnline = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0D9488", Dark: "#2DD4BF"})

	cliOffline = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#E11D48", Dark: "#FB7185"})

	cliKey = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#A1A1AA"})

	cliVal = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#F4F4F5"})

	cliAccent = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"})

	cliMuted = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#71717A"})

	cliWarn = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"})
)

func renderKV(k, v string) string {
	return fmt.Sprintf("  %s %s", cliKey.Render(fmt.Sprintf("%-11s", k+":")), v)
}

func renderDelayCLI(ms int) string {
	switch {
	case ms < 0:
		return cliOffline.Render("▫▫▫ timeout")
	case ms == 0:
		return cliMuted.Render("          -")
	case ms < 150:
		return cliOnline.Render(fmt.Sprintf("▪▪▪ %5dms", ms))
	case ms < 400:
		return cliWarn.Render(fmt.Sprintf("▪▪▫ %5dms", ms))
	default:
		return cliOffline.Render(fmt.Sprintf("▪▫▫ %5dms", ms))
	}
}
