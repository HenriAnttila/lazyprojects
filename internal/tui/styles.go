package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Only the terminal's 16 ANSI colours are used, and no background is ever set,
// so the UI follows whatever palette and transparency the terminal has.
var (
	sAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	sActive = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	sMatch  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	sDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sBold   = lipgloss.NewStyle().Bold(true)
	sErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	sWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	sCursor = lipgloss.NewStyle().Reverse(true)
)

// fit truncates or pads s to exactly w cells, ANSI sequences aside.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// age renders how long ago t was, in one short unit.
func age(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d.Minutes()), 0))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// size renders GitHub's diskUsage, which is in KB.
func size(kb int) string {
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(kb)/1024/1024)
	case kb >= 1024:
		return fmt.Sprintf("%.1f MB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}
