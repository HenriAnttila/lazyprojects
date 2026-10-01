// Package tui is the interface: three pickers sharing one layout.
//
//	Projects  what is on disk under the root      enter: go there
//	Add       GitHub repos not cloned yet         enter: clone, then go there
//	PRs       open pull requests of one repo      enter: check out
//
// Typing always filters, as in fzf, so every action is Enter, Tab or a ctrl
// chord. The model follows Bubble Tea's Elm loop: Update handles one message
// and returns commands; anything slow lives in commands.go.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/HenriAnttila/pj/internal/github"
	"github.com/HenriAnttila/pj/internal/projects"
)

type viewID int

const (
	viewProjects viewID = iota
	viewAdd
	viewPRs
)

type Options struct {
	Root     string
	Cwd      string
	Projects []projects.Project
	Client   github.Client
	// CachePath holds the repo list between runs; empty disables the cache.
	CachePath string

	// StartAdd opens on the Add view. StartPR opens on that project's PRs.
	StartAdd bool
	StartPR  *projects.Project
	// Notice is shown in the status line at launch.
	Notice string

	Clone    func(ctx context.Context, slug, target string, progress func(string)) error
	Checkout func(ctx context.Context, dir string, number int) error
	Now      func() time.Time
}

type Model struct {
	opts          Options
	width, height int

	view viewID
	back viewID // where esc returns to from the PR view
	pick [3]picker

	projects []projects.Project
	dirty    map[string]int // by path; absent until git has answered

	repos        []github.Repo
	reposLoading bool
	reposErr     error

	prRepo    string // owner/name the PR view is showing
	prDir     string // its clone on disk, or "" when it is not cloned
	prs       []github.PR
	prLoading bool
	prErr     error
	prSeq     int

	previews map[string]string // git status and log, by project path
	readmes  map[string]string // README markdown, by slug; "" means none
	md       markdown
	scroll   int // preview scroll offset
	hoverSeq int

	prompt *prompt

	busy     string // what is running; keys are ignored while set
	progress string
	cancel   context.CancelFunc

	status    string
	statusErr bool

	// Result is the directory to move the shell to once the program exits, and
	// Message a line to print after it. Both may be empty.
	Result  string
	Message string
}

func New(opts Options) *Model {
	if opts.Clone == nil {
		opts.Clone = Clone
	}
	if opts.Checkout == nil {
		opts.Checkout = Checkout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	m := &Model{
		opts:     opts,
		projects: opts.Projects,
		dirty:    map[string]int{},
		previews: map[string]string{},
		readmes:  map[string]string{},
		status:   opts.Notice,
	}
	if opts.CachePath != "" {
		m.repos = github.LoadCache(opts.CachePath)
	}
	m.rebuild()
	switch {
	case opts.StartPR != nil:
		m.openPRs(opts.StartPR.Slug(), opts.StartPR.Path)
	case opts.StartAdd:
		m.view = viewAdd
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.reposCmd(), m.hover()}
	m.reposLoading = true
	for _, p := range m.projects {
		cmds = append(cmds, dirtyCmd(p.Path))
	}
	if m.view == viewPRs {
		cmds = append(cmds, m.prsCmd(m.prSeq, m.prRepo))
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyPressMsg:
		return m, m.key(msg)

	case tea.PasteMsg:
		if m.busy == "" && m.prompt == nil {
			line, _, _ := strings.Cut(strings.TrimSpace(msg.Content), "\n")
			return m, m.setQuery(m.cur().query + line)
		}

	case dirtyMsg:
		m.dirty[msg.path] = msg.n
		m.rebuild()

	case reposMsg:
		m.reposLoading = false
		m.reposErr = msg.err
		if msg.err == nil {
			m.repos = msg.repos
			m.rebuild()
			return m, m.hover()
		}

	case prsMsg:
		if msg.seq != m.prSeq {
			break // an answer for a repo the user has already left
		}
		m.prLoading, m.prErr, m.prs = false, msg.err, msg.prs
		m.rebuild()
		return m, m.hover()

	case hoverMsg:
		if msg.seq == m.hoverSeq {
			return m, m.loadPreview()
		}

	case previewMsg:
		m.previews[msg.path] = msg.text

	case readmeMsg:
		if msg.err != nil {
			m.readmes[msg.slug] = "*could not load the README: " + msg.err.Error() + "*"
		} else {
			m.readmes[msg.slug] = msg.text
		}

	case cloneMsg:
		return m, m.cloned(msg)

	case checkoutMsg:
		return m, m.checkedOut(msg)

	case noticeMsg:
		if msg.err != nil {
			m.fail(msg.err)
		} else {
			m.status, m.statusErr = msg.text, false
		}
	}
	return m, nil
}

func (m *Model) cur() *picker { return &m.pick[m.view] }

func (m *Model) fail(err error) {
	m.status, m.statusErr = err.Error(), true
}

// key handles one keypress. While something is running only ctrl+c is
// honoured, and it cancels; with the clone prompt open the keys go to it.
func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if m.busy != "" {
		// Cancel rather than quit: the clone or checkout then ends through its
		// usual message, after git has had the chance to clean up after itself.
		if k == "ctrl+c" && m.cancel != nil {
			m.cancel()
			m.progress = "cancelling"
		}
		return nil
	}
	if k == "ctrl+c" {
		return tea.Quit
	}
	if m.errorDetail() {
		m.status, m.statusErr = "", false
		return nil // the keypress only dismisses the error
	}
	m.status, m.statusErr = "", false
	if m.prompt != nil {
		return m.promptKey(msg)
	}

	before := m.cur().selectedKey()
	var cmd tea.Cmd
	switch k {
	case "esc":
		switch {
		case m.cur().query != "":
			m.cur().setQuery("")
		case m.view == viewPRs:
			m.view = m.back
		default:
			return tea.Quit
		}
	case "up", "ctrl+p":
		m.cur().move(-1)
	case "down", "ctrl+n":
		m.cur().move(1)
	case "pgup":
		m.scroll = max(m.scroll-m.bodyHeight()/2, 0)
		return nil
	case "pgdown":
		m.scroll += m.bodyHeight() / 2
		return nil
	case "tab", "shift+tab":
		switch m.view {
		case viewProjects:
			m.view = viewAdd
		case viewAdd:
			m.view = viewProjects
		case viewPRs:
			m.view = m.back
		}
		return m.hover()
	case "enter":
		return m.enter()
	case "ctrl+r":
		cmd = m.pullRequests()
	case "ctrl+o":
		return m.browse()
	case "ctrl+y":
		if url := m.selectedURL(); url != "" {
			return copyCmd(url)
		}
		m.fail(fmt.Errorf("nothing on GitHub to copy a URL for"))
		return nil
	case "backspace":
		if q := []rune(m.cur().query); len(q) > 0 {
			m.cur().setQuery(string(q[:len(q)-1]))
		}
	case "ctrl+u":
		m.cur().setQuery("")
	case "ctrl+w":
		q := strings.TrimRight(m.cur().query, " ")
		m.cur().setQuery(q[:strings.LastIndex(q, " ")+1])
	default:
		if text := typed(msg); text != "" {
			m.cur().setQuery(m.cur().query + text)
		}
	}
	if cmd != nil {
		return cmd
	}
	if m.cur().selectedKey() != before {
		return m.hover()
	}
	return nil
}

// typed is the text a keypress inserts, or "" for a key that is not text.
func typed(msg tea.KeyPressMsg) string {
	k := msg.Key()
	if k.Mod&^tea.ModShift != 0 {
		return ""
	}
	return k.Text
}

func (m *Model) setQuery(q string) tea.Cmd {
	m.cur().setQuery(q)
	return m.hover()
}

// hover restarts the preview debounce; call it whenever the selection changes.
func (m *Model) hover() tea.Cmd {
	m.hoverSeq++
	m.scroll = 0
	return hoverCmd(m.hoverSeq)
}

// loadPreview fetches what the selected row's preview needs, once.
func (m *Model) loadPreview() tea.Cmd {
	r, ok := m.cur().selected()
	if !ok {
		return nil
	}
	switch m.view {
	case viewProjects:
		if _, ok := m.previews[r.key]; !ok {
			return previewCmd(r.key)
		}
	case viewAdd:
		if _, ok := m.readmes[r.key]; !ok {
			return m.readmeCmd(r.key)
		}
	}
	return nil
}

func (m *Model) enter() tea.Cmd {
	r, ok := m.cur().selected()
	if !ok {
		return nil
	}
	switch m.view {
	case viewProjects:
		m.Result = r.key
		return tea.Quit
	case viewAdd:
		m.openPrompt(r.key, 0)
	case viewPRs:
		pr, ok := m.pr(r.key)
		if !ok {
			return nil
		}
		if m.prDir == "" {
			// Not on disk yet: clone first, then check the PR out in the clone.
			m.openPrompt(m.prRepo, pr.Number)
			return nil
		}
		return m.checkout(m.prDir, pr.Number)
	}
	return nil
}

func (m *Model) checkout(dir string, number int) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.busy, m.progress = fmt.Sprintf("checking out #%d", number), ""
	return m.checkoutCmd(ctx, dir, number)
}

func (m *Model) checkedOut(msg checkoutMsg) tea.Cmd {
	m.busy, m.cancel = "", nil
	if msg.err != nil {
		// Typically a dirty working tree. git has left it untouched; so do we.
		m.fail(fmt.Errorf("checkout of #%d failed: %w", msg.number, msg.err))
		return nil
	}
	m.Message = fmt.Sprintf("checked out #%d in %s", msg.number, msg.dir)
	// Only move the shell if it is not already somewhere inside the repo.
	if !within(m.opts.Cwd, msg.dir) {
		m.Result = msg.dir
	}
	return tea.Quit
}

func (m *Model) cloned(msg cloneMsg) tea.Cmd {
	if !msg.done {
		m.progress = msg.line
		return waitClone(msg.ch)
	}
	p := m.prompt
	m.prompt, m.busy, m.cancel = nil, "", nil
	if msg.err != nil {
		m.fail(fmt.Errorf("clone of %s failed: %w", p.slug, msg.err))
		return nil
	}
	m.projects = append(m.projects, projects.Load(m.opts.Root, p.target))
	m.rebuild()
	if p.pr != 0 {
		m.prDir = p.target
		return m.checkout(p.target, p.pr)
	}
	m.Message = fmt.Sprintf("cloned %s into %s", p.slug, p.target)
	m.Result = p.target
	return tea.Quit
}

// pullRequests opens the PR view for the selected repo.
func (m *Model) pullRequests() tea.Cmd {
	r, ok := m.cur().selected()
	if !ok {
		return nil
	}
	switch m.view {
	case viewProjects:
		p, _ := m.project(r.key)
		if !p.IsGitHub() {
			m.fail(fmt.Errorf("%s has no GitHub remote", p.Rel))
			return nil
		}
		m.openPRs(p.Slug(), p.Path)
	case viewAdd:
		m.openPRs(r.key, "")
	default:
		return nil
	}
	return tea.Batch(m.prsCmd(m.prSeq, m.prRepo), m.hover())
}

func (m *Model) openPRs(slug, dir string) {
	if m.view != viewPRs {
		m.back = m.view
	}
	m.view = viewPRs
	m.prRepo, m.prDir = slug, dir
	m.prs, m.prErr, m.prLoading = nil, nil, true
	m.prSeq++
	m.pick[viewPRs] = picker{}
}

func (m *Model) browse() tea.Cmd {
	r, ok := m.cur().selected()
	if !ok {
		return nil
	}
	switch m.view {
	case viewProjects:
		p, _ := m.project(r.key)
		if !p.IsGitHub() {
			m.fail(fmt.Errorf("%s has no GitHub remote", p.Rel))
			return nil
		}
		return m.browseCmd("repo", "view", p.Slug(), "--web")
	case viewAdd:
		return m.browseCmd("repo", "view", r.key, "--web")
	default:
		return m.browseCmd("pr", "view", r.key, "-R", m.prRepo, "--web")
	}
}

func (m *Model) selectedURL() string {
	r, ok := m.cur().selected()
	if !ok {
		return ""
	}
	switch m.view {
	case viewProjects:
		if p, _ := m.project(r.key); p.IsGitHub() {
			return "https://github.com/" + p.Slug()
		}
		return ""
	case viewAdd:
		return "https://github.com/" + r.key
	default:
		pr, _ := m.pr(r.key)
		return pr.URL
	}
}

func (m *Model) project(path string) (projects.Project, bool) {
	for _, p := range m.projects {
		if p.Path == path {
			return p, true
		}
	}
	return projects.Project{}, false
}

func (m *Model) repo(slug string) (github.Repo, bool) {
	for _, r := range m.repos {
		if r.NameWithOwner == slug {
			return r, true
		}
	}
	return github.Repo{}, false
}

func (m *Model) pr(number string) (github.PR, bool) {
	for _, pr := range m.prs {
		if fmt.Sprint(pr.Number) == number {
			return pr, true
		}
	}
	return github.PR{}, false
}

// rebuild turns the model's data into picker rows. It is cheap, and called
// after anything that changes what a list should show.
func (m *Model) rebuild() {
	now := m.opts.Now()

	rows := make([]row, 0, len(m.projects))
	cloned := map[string]bool{}
	for _, p := range m.projects {
		r := row{key: p.Path, name: p.Rel, cols: []string{p.Branch}}
		if n := m.dirty[p.Path]; n > 0 {
			r.badge = sWarn.Render(fmt.Sprintf("●%d", n))
		}
		rows = append(rows, r)
		if p.IsGitHub() {
			cloned[strings.ToLower(p.Slug())] = true
		}
	}
	m.pick[viewProjects].setRows(rows)

	// Add lists only what is not on disk yet, so it never shows a repo twice
	// and shrinks as the root fills up. Order is GitHub's: last push first.
	rows = make([]row, 0, len(m.repos))
	for _, r := range m.repos {
		if cloned[strings.ToLower(r.NameWithOwner)] {
			continue
		}
		rows = append(rows, row{
			key:  r.NameWithOwner,
			name: r.NameWithOwner,
			cols: []string{r.Language, age(r.PushedAt, now)},
			dim:  r.IsArchived,
		})
	}
	m.pick[viewAdd].setRows(rows)

	rows = make([]row, 0, len(m.prs))
	for _, pr := range m.prs {
		r := row{
			key:  fmt.Sprint(pr.Number),
			name: fmt.Sprintf("#%d %s", pr.Number, pr.Title),
			cols: []string{pr.Head, age(pr.UpdatedAt, now)},
		}
		if pr.IsDraft {
			r.badge = sDim.Render("draft")
		}
		rows = append(rows, r)
	}
	m.pick[viewPRs].setRows(rows)
}

// within reports whether dir is inside root (or is root).
func within(dir, root string) bool {
	if dir == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
