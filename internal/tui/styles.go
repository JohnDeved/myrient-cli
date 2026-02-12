package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary   = lipgloss.Color("#7C3AED") // Purple
	colorSecondary = lipgloss.Color("#06B6D4") // Cyan
	colorSuccess   = lipgloss.Color("#10B981") // Green
	colorWarning   = lipgloss.Color("#F59E0B") // Amber
	colorError     = lipgloss.Color("#EF4444") // Red
	colorMuted     = lipgloss.Color("#6B7280") // Gray
	colorBg        = lipgloss.Color("#1F2937") // Dark bg
	colorHighlight = lipgloss.Color("#374151") // Highlight bg

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	breadcrumbStyle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Background(colorHighlight).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			PaddingLeft(1).
			PaddingRight(1)

	normalStyle = lipgloss.NewStyle().
			PaddingLeft(1).
			PaddingRight(1)

	dirStyle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	fileStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D1D5DB"))

	sizeStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(10).
			Align(lipgloss.Right)

	dateStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(18)

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#111827")).
			Foreground(lipgloss.Color("#9CA3AF")).
			PaddingLeft(1).
			PaddingRight(1)

	tabActiveStyle = lipgloss.NewStyle().
			Background(colorPrimary).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			PaddingLeft(1).
			PaddingRight(1)

	tabInactiveStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#374151")).
				Foreground(lipgloss.Color("#9CA3AF")).
				PaddingLeft(1).
				PaddingRight(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(colorSuccess)

	markedStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true)

	progressBarFilled = lipgloss.NewStyle().
				Foreground(colorSuccess)

	progressBarEmpty = lipgloss.NewStyle().
				Foreground(colorMuted)

	searchPromptStyle = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	collectionBadge = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Background(lipgloss.Color("#1E3A5F")).
			PaddingLeft(1).
			PaddingRight(1)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary)
)
