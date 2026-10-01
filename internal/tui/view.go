package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/HenriAnttila/pj/internal/github"
	"github.com/HenriAnttila/pj/internal/projects"
)

// Fixed lines around the body: tabs, filter, a rule above and below, key
// hints, status.
const chrome = 6

// minPreviewWidth is the terminal width below which the preview is dropped
// and the list takes the whole screen.
const minPreviewWidth = 80

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *Model) bodyHeight() int { return max(m.height-chrome, 1) }

// listWidth is the list pane's width, and previewWidth what is left after the
// " │ " divider; previewWidth is 0 when there is no room for a preview.
func (m *Model) widths() (list, preview int) {
	if m.width < minPreviewWidth {
		return m.width, 0
	}
	list = max(m.width*42/100, 34)
	return list, m.width - list - 3
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	listW, prevW := m.widths()
	h := m.bodyHeight()

	var body []string
	full := m.prompt != nil || m.errorDetail()
	switch {
	case m.errorDetail():
		// git's own output, in full: one status line cannot hold it, and which
		// line matters differs between a failed clone and a failed checkout.
		body = append(body, "")
		for _, l := range strings.Split(strings.TrimSpace(m.status), "\n") {
			body = append(body, "  "+sErr.Render(strings.ReplaceAll(l, "\t", "    ")))
		}
	case m.prompt != nil:
		body = m.promptLines()
	default:
		list := m.listLines(listW, h)
		if prevW == 0 {
			body = list
		} else {
			prev := m.previewLines(prevW, h)
			for i := range h {
				body = append(body, fit(at(list, i), listW)+sDim.Render(" │ ")+at(prev, i))
			}
		}
	}
	for len(body) < h {
		body = append(body, "")
	}

	joint := prevW > 0 && !full
	lines := []string{m.tabs(), m.filterLine(), rule(m.width, listW, joint, "┬")}
	lines = append(lines, body[:h]...)
	lines = append(lines, rule(m.width, listW, joint, "┴"), m.hints(), m.statusLine())
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n")
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func rule(width, listW int, joint bool, char string) string {
	if !joint {
		return sDim.Render(strings.Repeat("─", width))
	}
	return sDim.Render(strings.Repeat("─", listW+1) + char + strings.Repeat("─", max(width-listW-2, 0)))
}

func (m *Model) tabs() string {
	tab := func(id viewID, label string) string {
		if m.view == id {
			return sActive.Render(label)
		}
		return sDim.Render(label)
	}
	left := " " + tab(viewProjects, "Projects") + "   " + tab(viewAdd, "Add")
	// Below the first layer it is a breadcrumb, not tabs.
	switch m.view {
	case viewProject:
		left = " " + sDim.Render("Projects ›") + " " + sActive.Render(m.projName())
	case viewPRs:
		left = " " + sDim.Render("Projects › "+m.projName()+" ›") + " " + sActive.Render("Pull requests")
	}
	right := sDim.Render(tilde(m.opts.Root)) + " "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) filterLine() string {
	p := m.cur()
	left := " " + sAccent.Render(">") + " " + p.query
	if m.prompt == nil && m.busy == "" {
		left += sCursor.Render(" ")
	}
	count := fmt.Sprintf("%d/%d", len(p.matches), len(p.rows))
	if m.view == viewPRs && len(m.prs) >= github.PRLimit {
		count += fmt.Sprintf(" (first %d)", github.PRLimit) // say so rather than silently truncate
	}
	right := sDim.Render(count) + " "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) listLines(width, height int) []string {
	p := m.cur()
	if len(p.matches) > 0 {
		return p.view(width, height)
	}
	return []string{"", "  " + m.emptyText()}
}

// emptyText explains an empty list: still loading, failed, filtered to
// nothing, or genuinely empty.
func (m *Model) emptyText() string {
	if len(m.cur().rows) > 0 {
		return sDim.Render("no matches")
	}
	switch m.view {
	case viewProjects:
		return sDim.Render("no git repos under " + tilde(m.opts.Root))
	case viewAdd:
		switch {
		case m.reposErr != nil && len(m.repos) == 0:
			return sErr.Render("could not list repos: " + firstLine(m.reposErr.Error()))
		case m.reposLoading && len(m.repos) == 0:
			return sDim.Render("loading your repos from GitHub…")
		}
		return sDim.Render("every repo you can access is already cloned")
	case viewProject:
		// Reached only from inside a project that is not on GitHub: nowhere
		// to go, and no pull requests to list.
		return sDim.Render("not on GitHub: nothing to do here")
	}
	switch {
	case m.prLoading:
		return sDim.Render("loading pull requests…")
	case m.prErr != nil:
		return sErr.Render("could not list pull requests: " + firstLine(m.prErr.Error()))
	}
	return sDim.Render("no open pull requests")
}

// projName is how the open project is titled: its path under the root, or its
// full path when it lives outside it.
func (m *Model) projName() string {
	if strings.HasPrefix(m.proj.Rel, "..") || filepath.IsAbs(m.proj.Rel) {
		return tilde(m.proj.Path)
	}
	return m.proj.Rel
}

// projectInfo is where a project is and what state its working tree is in.
func (m *Model) projectInfo(p projects.Project, title string) string {
	remote := "no remote"
	if p.Host != "" {
		remote = p.Host + ":" + p.Owner + "/" + p.Name
	}
	out := sBold.Render(title) + "\n" + sDim.Render(tilde(p.Path)+"\n"+remote) + "\n\n"
	if text, ok := m.previews[p.Path]; ok {
		out += text
	}
	return out
}

func (m *Model) previewLines(width, height int) []string {
	lines := strings.Split(m.previewText(width), "\n")
	m.scroll = max(min(m.scroll, len(lines)-height), 0)
	lines = lines[m.scroll:]
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return lines
}

func (m *Model) previewText(width int) string {
	if m.view == viewProject {
		// The same for every row, and for none: this screen is about the project.
		return m.projectInfo(m.proj, m.projName())
	}
	r, ok := m.cur().selected()
	if !ok {
		return ""
	}
	now := m.opts.Now()
	sep := sDim.Render(strings.Repeat("─", min(width, 40)))
	var b strings.Builder
	switch m.view {
	case viewProjects:
		p, _ := m.project(r.key)
		b.WriteString(m.projectInfo(p, p.Rel))

	case viewAdd:
		repo, _ := m.repo(r.key)
		b.WriteString(sBold.Render(repo.NameWithOwner) + "\n")
		facts := []string{"public"}
		if repo.IsPrivate {
			facts[0] = "private"
		}
		if repo.IsFork {
			facts = append(facts, "fork")
		}
		if repo.IsArchived {
			facts = append(facts, "archived")
		}
		if repo.Language != "" {
			facts = append(facts, repo.Language)
		}
		facts = append(facts, "pushed "+ago(age(repo.PushedAt, now)), size(repo.DiskUsage))
		b.WriteString(sDim.Render(strings.Join(facts, " · ")) + "\n")
		if repo.Description != "" {
			b.WriteString("\n" + ansi.Wordwrap(repo.Description, width, "") + "\n")
		}
		b.WriteString("\n" + sep + "\n\n")
		switch readme, ok := m.readmes[repo.NameWithOwner]; {
		case !ok:
			b.WriteString(sDim.Render("loading README…"))
		case strings.TrimSpace(readme) == "":
			b.WriteString(sDim.Render("(no README)"))
		default:
			b.WriteString(m.md.render(readme, width))
		}

	case viewPRs:
		pr, _ := m.pr(r.key)
		b.WriteString(sBold.Render(ansi.Wordwrap(fmt.Sprintf("#%d %s", pr.Number, pr.Title), width, "")) + "\n")
		facts := []string{pr.Author, pr.Head + " → " + pr.Base}
		if pr.IsDraft {
			facts = append(facts, "draft")
		}
		facts = append(facts, "updated "+ago(age(pr.UpdatedAt, now)))
		b.WriteString(sDim.Render(strings.Join(facts, " · ")) + "\n")
		b.WriteString("\n" + sep + "\n\n")
		if strings.TrimSpace(pr.Body) == "" {
			b.WriteString(sDim.Render("(no description)"))
		} else {
			b.WriteString(m.md.render(pr.Body, width))
		}
	}
	return b.String()
}

func (m *Model) promptLines() []string {
	p := m.prompt
	what := "Clone " + sBold.Render(p.slug)
	shown := make([]string, len(p.options))
	for i, o := range p.options {
		if o == "" {
			o = "(root)"
		}
		if p.options[i] == p.input {
			shown[i] = sActive.Render(o)
		} else {
			shown[i] = sDim.Render(o)
		}
	}
	dest := tilde(m.opts.Root) + "/"
	if p.input != "" {
		dest += p.input + "/"
	}
	lines := []string{
		"",
		"  " + what,
		"",
		"  container  " + p.input + sCursor.Render(" "),
		"  tab cycles " + strings.Join(shown, "  "),
		"",
		"  " + sDim.Render("→ ") + dest + sBold.Render(p.name),
	}
	if p.err != "" {
		lines = append(lines, "", "  "+sErr.Render(p.err))
	}
	return lines
}

func (m *Model) hints() string {
	var keys []string
	switch {
	case m.busy != "":
		keys = []string{"ctrl-c cancel"}
	case m.prompt != nil:
		keys = []string{"enter clone", "tab next container", "← cancel", "esc quit"}
	case m.view == viewProjects:
		keys = []string{"enter open", "tab add", "ctrl-o browser", "ctrl-y copy URL", "pgup/pgdn scroll", "esc quit"}
	case m.view == viewAdd:
		keys = []string{"enter clone", "tab projects", "ctrl-o browser", "ctrl-y copy URL", "pgup/pgdn scroll", "esc quit"}
	case m.view == viewProject:
		switch m.cur().selectedKey() {
		case goKey:
			keys = []string{"enter go there"}
		case prsKey:
			keys = []string{"enter open"}
		}
		keys = append(keys, "← projects", "ctrl-o browser", "ctrl-y copy URL", "pgup/pgdn scroll", "esc quit")
	default:
		if m.cur().selectedKey() != "" {
			keys = []string{"enter check out"}
		}
		keys = append(keys, "← back", "ctrl-o browser", "ctrl-y copy URL", "pgup/pgdn scroll", "esc quit")
	}
	return " " + sDim.Render(strings.Join(keys, "   "))
}

func (m *Model) statusLine() string {
	switch {
	case m.busy != "":
		return " " + sAccent.Render(m.busy+"…") + " " + sDim.Render(m.progress)
	case m.errorDetail():
		return " " + sDim.Render("press any key to dismiss")
	case m.status != "" && m.statusErr:
		return " " + sErr.Render(m.status)
	case m.status != "":
		return " " + m.status
	case m.view == viewAdd && m.reposErr != nil:
		return " " + sErr.Render("refresh failed, showing the cached list: "+firstLine(m.reposErr.Error()))
	case m.view == viewAdd && m.reposLoading:
		return " " + sDim.Render("refreshing…")
	}
	return ""
}

func ago(a string) string {
	if a == "never" {
		return a
	}
	return a + " ago"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// errorDetail reports whether the current error is too long for the status
// line and takes over the body instead.
func (m *Model) errorDetail() bool {
	return m.statusErr && m.busy == "" && strings.Contains(strings.TrimSpace(m.status), "\n")
}

// tilde abbreviates the home directory.
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rest
	}
	return path
}
