package ui

import "github.com/charmbracelet/lipgloss"

// Colours are adaptive so the app is legible on light and dark terminals
// without asking the user to configure anything. A terminal app that assumes a
// dark background is unreadable for the people who do not have one.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0b6e5f", Dark: "#5fd7c0"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b93a1"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#a16207", Dark: "#e3b341"}
	colErr    = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	colBorder = lipgloss.AdaptiveColor{Light: "#d4d4d8", Dark: "#3f3f46"}
	colOK     = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"}
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(colAccent).
			Bold(true)

	detailPane = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colBorder).
			PaddingLeft(2)

	keyStyle = lipgloss.NewStyle().Foreground(colMuted)

	valStyle = lipgloss.NewStyle()

	hintStyle = lipgloss.NewStyle().Foreground(colMuted)

	warnStyle = lipgloss.NewStyle().Foreground(colWarn)

	errStyle = lipgloss.NewStyle().Foreground(colErr)

	okStyle = lipgloss.NewStyle().Foreground(colOK)
)
