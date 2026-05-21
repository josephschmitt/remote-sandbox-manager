package ui

import "charm.land/lipgloss/v2"

type styles struct {
	title       lipgloss.Style
	sandbox     lipgloss.Style
	cursor      lipgloss.Style
	selectedRow lipgloss.Style
	divider     lipgloss.Style
	status      lipgloss.Style
	attached    lipgloss.Style

	working   lipgloss.Style
	needs     lipgloss.Style
	idle      lipgloss.Style
	completed lipgloss.Style
	failed    lipgloss.Style
	stopped   lipgloss.Style
}

func newStyles() styles {
	return styles{
		title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		sandbox:     lipgloss.NewStyle().Faint(true).Italic(true).Foreground(lipgloss.Color("8")),
		cursor:      lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true),
		selectedRow: lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true),
		divider:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		status:      lipgloss.NewStyle().Reverse(true).Foreground(lipgloss.Color("15")),
		attached:    lipgloss.NewStyle().Foreground(lipgloss.Color("13")),

		working:   lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		needs:     lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		idle:      lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		completed: lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
		failed:    lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		stopped:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
}
