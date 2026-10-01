// Package tui is the interface: pickers sharing one layout, in three layers.
//
//	Projects  what is on disk under the root      enter: open the project
//	Add       GitHub repos not cloned yet         enter: clone, then go there
//
//	Project   what you can do with one project:   enter: do it
//	          go there, see its pull requests
//
//	PRs       that project's open pull requests   enter: check out
//
// Each layer is entered with enter and left with the left arrow; esc closes pj
// from anywhere. Launched inside a repo, pj opens on that project rather than
// on the list.
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
	viewProject
	viewPRs
)

// The project view's rows: the things you can do with a project.
const (
	goKey  = "go"
	prsKey = "prs"
)

type Options struct {
	Root     string
	Cwd      string
	Projects []projects.Project
	Client   github.Client
	// CachePath holds the repo list between runs; empty disables the cache.
	CachePath string

	// Here is the repo the shell is standing in, or nil. When set, pj opens on
	// that project's view rather than the list.
	Here *projects.Project
	// StartAdd opens on the Add view instead. StartPR opens on Here's pull
	// requests, for `pj pr`.
	StartAdd, StartPR bool
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
	pick [4]picker

	projects []projects.Project
	dirty    map[string]int // by path; absent until git has answered

	repos        []github.Repo
	reposLoading bool
	reposErr     error

	proj      projects.Project // the project the project view is showing
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
	case opts.StartAdd:
		m.view = viewAdd
	case opts.Here != nil:
		m.open(*opts.Here)
		if opts.StartPR && opts.Here.IsGitHub() {
			m.openPRs()
		}
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
		cmds = append(cmds, m.prsCmd())
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
			break // an answer for a project the user has already left
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
	switch k {
	case "ctrl+c", "esc", "alt+esc": // two quick escapes arrive as alt+esc
		// Escape always closes pj, from any layer and with a filter typed.
		// Going back a layer is the left arrow.
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
	switch k {
	case "left":
		switch m.view {
		case viewPRs:
			m.view = viewProject
		case viewProject:
			m.view = viewProjects
			m.cur().selectKey(m.proj.Path)
		default:
			return nil // already at the first layer
		}
		return m.hover()
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
		m.cycle()
		return m.hover()
	case "enter":
		return m.enter()
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
	if m.cur().selectedKey() != before {
		return m.hover()
	}
	return nil
}

// cycle switches between the two lists. The project view is not part of the
// cycle: it is entered with enter and left with the left arrow.
func (m *Model) cycle() {
	switch m.view {
	case viewProjects:
		m.view = viewAdd
	case viewAdd:
		m.view = viewProjects
	}
}

// open shows the project view for p.
func (m *Model) open(p projects.Project) {
	m.view, m.proj = viewProject, p
	m.prs, m.prErr, m.prLoading = nil, nil, false
	m.prSeq++ // an answer still on its way for the previous project is stale
	m.pick[viewProject], m.pick[viewPRs] = picker{}, picker{}
	m.rebuild()
}

// openPRs shows the open project's pull requests. They are fetched each time
// the view is opened, never before: most visits to a project are not for them.
func (m *Model) openPRs() {
	m.view = viewPRs
	m.prs, m.prErr, m.prLoading = nil, nil, true
	m.prSeq++
	m.pick[viewPRs] = picker{}
	m.rebuild()
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
	path := ""
	switch {
	case m.view == viewProject:
		path = m.proj.Path // the project's own state, whatever row is selected
	case !ok:
		return nil
	}
	switch m.view {
	case viewProjects:
		path = r.key
	case viewAdd:
		if _, ok := m.readmes[r.key]; !ok {
			return m.readmeCmd(r.key)
		}
	}
	if _, ok := m.previews[path]; path != "" && !ok {
		return previewCmd(path)
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
		if p, ok := m.project(r.key); ok {
			m.open(p)
			return m.hover()
		}
	case viewAdd:
		m.openPrompt(r.key)
	case viewProject:
		switch r.key {
		case goKey:
			m.Result = m.proj.Path
			return tea.Quit
		case prsKey:
			m.openPRs()
			return tea.Batch(m.prsCmd(), m.hover())
		}
	case viewPRs:
		if pr, ok := m.pr(r.key); ok {
			return m.checkout(m.proj.Path, pr.Number)
		}
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
	m.Message = fmt.Sprintf("cloned %s into %s", p.slug, p.target)
	m.Result = p.target
	return tea.Quit
}

func (m *Model) browse() tea.Cmd {
	r, ok := m.cur().selected()
	if !ok {
		return nil
	}
	switch m.view {
	case viewAdd:
		return m.browseCmd("repo", "view", r.key, "--web")
	case viewPRs:
		return m.browseCmd("pr", "view", r.key, "-R", m.proj.Slug(), "--web")
	}
	p := m.proj
	if m.view == viewProjects {
		p, _ = m.project(r.key)
	}
	if !p.IsGitHub() {
		m.fail(fmt.Errorf("%s has no GitHub remote", p.Rel))
		return nil
	}
	return m.browseCmd("repo", "view", p.Slug(), "--web")
}

func (m *Model) selectedURL() string {
	r, ok := m.cur().selected()
	if !ok {
		return ""
	}
	switch m.view {
	case viewAdd:
		return "https://github.com/" + r.key
	case viewPRs:
		pr, _ := m.pr(r.key)
		return pr.URL
	}
	p := m.proj
	if m.view == viewProjects {
		p, _ = m.project(r.key)
	}
	if !p.IsGitHub() {
		return ""
	}
	return "https://github.com/" + p.Slug()
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

	rows = nil
	if !within(m.opts.Cwd, m.proj.Path) { // no point offering to go where you are
		rows = append(rows, row{key: goKey, name: "Go to project"})
	}
	if m.proj.IsGitHub() {
		rows = append(rows, row{key: prsKey, name: "Pull requests"})
	}
	m.pick[viewProject].setRows(rows)

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
