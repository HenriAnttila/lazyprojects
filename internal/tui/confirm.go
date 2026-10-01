package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/HenriAnttila/lazyprojects/internal/projects"
)

// confirm is the screen between choosing "Delete project" and the directory
// being removed. It says what would be lost, and when something would be, it
// takes the project's name typed out rather than a single keypress.
type confirm struct {
	proj  projects.Project
	risk  *projects.Risk // nil until git has answered
	panes int            // tmux panes sitting inside the project
	input string
	err   string
}

// word is what has to be typed to delete a project that has something to lose.
func (c *confirm) word() string { return filepath.Base(c.proj.Path) }

func (m *Model) openConfirm() tea.Cmd {
	m.confirm = &confirm{proj: m.proj}
	return m.riskCmd(m.proj.Path)
}

func (m *Model) confirmKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.confirm
	c.err = ""
	switch msg.String() {
	case "left":
		m.confirm = nil
	case "backspace":
		if in := []rune(c.input); len(in) > 0 {
			c.input = string(in[:len(in)-1])
		}
	case "ctrl+u":
		c.input = ""
	case "enter":
		switch {
		case c.risk == nil:
			return nil // still checking; never delete on a guess
		case !c.risk.Safe() && c.input != c.word():
			c.err = fmt.Sprintf("type %s to confirm", c.word())
			return nil
		}
		m.busy, m.progress = "deleting "+m.projName(), ""
		return m.deleteCmd(c.proj.Path)
	default:
		c.input += typed(msg)
	}
	return nil
}

func (m *Model) deleted(msg deletedMsg) {
	m.busy = ""
	if msg.err != nil {
		m.confirm = nil
		m.fail(fmt.Errorf("could not delete %s: %w", tilde(msg.path), msg.err))
		return
	}
	name := m.projName()
	kept := m.projects[:0:0]
	for _, p := range m.projects {
		if p.Path != msg.path {
			kept = append(kept, p)
		}
	}
	m.projects, m.confirm = kept, nil
	delete(m.dirty, msg.path)
	delete(m.previews, msg.path)
	m.view = viewProjects
	m.cur().setQuery("") // it matched the project that is now gone
	m.rebuild()          // the repo is back in Add, if it is on GitHub
	m.status, m.statusErr = "deleted "+name, false
}

func (m *Model) confirmLines() []string {
	c := m.confirm
	lines := []string{
		"",
		"  Delete " + sBold.Render(m.projName()),
		"  " + sDim.Render(tilde(c.proj.Path)),
		"",
	}
	if c.risk == nil {
		return append(lines, "  "+sDim.Render("checking what would be lost…"))
	}
	r := *c.risk
	var lost []string
	if r.NoRemote {
		lost = append(lost, fmt.Sprintf("it has no remote: this is the only copy of its %s", plural(r.Commits, "commit")))
	}
	if r.Dirty > 0 {
		lost = append(lost, plural(r.Dirty, "uncommitted or untracked file"))
	}
	for _, b := range r.Unpushed {
		lost = append(lost, "not pushed: "+b)
	}
	if r.Stashes > 0 {
		lost = append(lost, plural(r.Stashes, "stash"))
	}
	if n := len(r.Ignored); n > 0 {
		shown := r.Ignored
		more := ""
		if n > 4 {
			shown, more = shown[:4], fmt.Sprintf(" and %d more", n-4)
		}
		lost = append(lost, "ignored files that are in no commit: "+strings.Join(shown, ", ")+more)
	}
	if len(lost) == 0 {
		lines = append(lines, "  Nothing here would be lost: it is clean and pushed, as of the last fetch.")
	} else {
		lines = append(lines, "  "+sErr.Render("This would lose work that exists nowhere else:"))
		for _, l := range lost {
			lines = append(lines, "    "+sWarn.Render("•")+" "+l)
		}
	}
	if c.panes > 0 {
		lines = append(lines, "", "  "+sWarn.Render(plural(c.panes, "tmux pane")+" open inside it would be left in a deleted directory"))
	}
	lines = append(lines, "")
	if r.Safe() {
		lines = append(lines, "  The directory is removed for good, not moved to a trash.")
	} else {
		lines = append(lines,
			"  The directory is removed for good, not moved to a trash.",
			"",
			"  type "+sBold.Render(c.word())+" to delete: "+c.input+sCursor.Render(" "))
	}
	if c.err != "" {
		lines = append(lines, "", "  "+sErr.Render(c.err))
	}
	return lines
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "sh") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
