package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/daemon"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7DD3FC"))
	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#93C5FD"))
	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))
	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))
	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F8FAFC")).
			Background(lipgloss.Color("#155E75"))
	selectedDetailStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E0F2FE")).
				Background(lipgloss.Color("#155E75"))
	goodStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#34D399"))
	warningStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FBBF24"))
	badStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FB7185"))
	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#38BDF8"))
	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FCD34D"))
	fieldStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0"))
	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#475569"))
	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CBD5E1"))
)

func styledLine(style lipgloss.Style, value string, width int) string {
	return style.Render(clip(value, width))
}

func accountStateStyle(state account.State) lipgloss.Style {
	switch state {
	case account.StateAuthenticated:
		return goodStyle
	case account.StatePortal, account.StateAuthenticating, account.StateBackoff:
		return warningStyle
	case account.StateOffline, account.StateError:
		return badStyle
	case account.StateChecking:
		return infoStyle
	default:
		return mutedStyle
	}
}

func accountRouteStyle(state account.State, selected bool) lipgloss.Style {
	if selected {
		return selectedStyle
	}
	return accountStateStyle(state)
}

func interfaceStateStyle(status daemon.InterfaceStatus) lipgloss.Style {
	if !status.Present || !status.AdminUp {
		return badStyle
	}
	if !status.Carrier || len(status.IPv4Addresses) == 0 {
		return warningStyle
	}
	return goodStyle
}
