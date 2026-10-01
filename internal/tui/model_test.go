package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/HenriAnttila/pj/internal/github"
	"github.com/HenriAnttila/pj/internal/projects"
)

func init() { hoverDelay = 0 }

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	*Model
	t        *testing.T
	root     string
	quit     bool
	cloned   []string // "slug -> target"
	checked  []string // "dir #n"
	cloneErr error
	checkErr error
	ghCalls  []string
}

// newFixture builds a model over a temp root holding Kompell/kompose (cloned)
// and a plain local project, with three repos on "GitHub".
func newFixture(t *testing.T, tweak func(*Options)) *fixture {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Kompell", "kompose"), 0o755)
	os.MkdirAll(filepath.Join(root, "demos"), 0o755)
	f := &fixture{t: t, root: root}
	opts := Options{
		Root: root,
		Cwd:  "/elsewhere",
		Projects: []projects.Project{
			{Path: filepath.Join(root, "Kompell", "kompose"), Rel: "Kompell/kompose", Host: "github.com", Owner: "Kompell", Name: "kompose", Branch: "main"},
			{Path: filepath.Join(root, "local"), Rel: "local", Branch: "main"},
		},
		Client: github.Client{Run: func(_ context.Context, args ...string) ([]byte, error) {
			f.ghCalls = append(f.ghCalls, strings.Join(args, " "))
			switch args[0] {
			case "pr":
				return []byte(`[{"number":7,"title":"Add thing","author":{"login":"henri"},"headRefName":"thing","baseRefName":"main","url":"https://github.com/Kompell/claude/pull/7","updatedAt":"2026-09-30T12:00:00Z"}]`), nil
			case "api":
				if args[1] == "graphql" {
					return []byte(`{"data":{"viewer":{"repositories":{"nodes":[
						{"nameWithOwner":"Kompell/claude","pushedAt":"2026-09-30T12:00:00Z"},
						{"nameWithOwner":"kompell/Kompose","pushedAt":"2026-09-29T12:00:00Z"},
						{"nameWithOwner":"HenriAnttila/compiler","pushedAt":"2026-09-01T12:00:00Z"},
						{"nameWithOwner":"ikiuscompany/kompass","pushedAt":"2025-01-01T12:00:00Z"}
					]}}}}`), nil
				}
				return []byte("# readme"), nil
			}
			return nil, nil
		}},
		Clone: func(_ context.Context, slug, target string, progress func(string)) error {
			f.cloned = append(f.cloned, slug+" -> "+target)
			progress("Receiving objects: 50%")
			if f.cloneErr == nil {
				os.MkdirAll(target, 0o755)
			}
			return f.cloneErr
		},
		Checkout: func(_ context.Context, dir string, number int) error {
			f.checked = append(f.checked, dir+" #"+string(rune('0'+number)))
			return f.checkErr
		},
		Now: func() time.Time { return now },
	}
	if tweak != nil {
		tweak(&opts)
	}
	f.Model = New(opts)
	f.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	f.run(f.Init())
	return f
}

// run executes a command and everything it leads to, feeding each message
// back through Update, the way the Bubble Tea runtime would.
func (f *fixture) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.QuitMsg:
		f.quit = true
	case tea.BatchMsg:
		for _, c := range msg {
			f.run(c)
		}
	default:
		f.send(msg)
	}
}

func (f *fixture) send(msg tea.Msg) {
	_, cmd := f.Update(msg)
	f.run(cmd)
}

func (f *fixture) press(keys ...string) {
	special := map[string]tea.KeyPressMsg{
		"enter":     {Code: tea.KeyEnter},
		"esc":       {Code: tea.KeyEscape},
		"tab":       {Code: tea.KeyTab},
		"down":      {Code: tea.KeyDown},
		"up":        {Code: tea.KeyUp},
		"backspace": {Code: tea.KeyBackspace},
		"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
		"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
	}
	for _, k := range keys {
		if msg, ok := special[k]; ok {
			f.send(msg)
			continue
		}
		for _, r := range k {
			f.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

func (f *fixture) names() []string {
	p := f.cur()
	var out []string
	for _, m := range p.matches {
		out = append(out, p.rows[m.idx].name)
	}
	return out
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestAddHidesWhatIsAlreadyCloned(t *testing.T) {
	f := newFixture(t, nil)
	f.press("tab")
	// kompell/Kompose is on disk as Kompell/kompose; owners and names on
	// GitHub are case-insensitive.
	equal(t, "Add rows", f.names(), []string{"Kompell/claude", "HenriAnttila/compiler", "ikiuscompany/kompass"})
}

func TestFilterKeepsPushOrderForSubstringMatches(t *testing.T) {
	f := newFixture(t, nil)
	f.press("tab", "komp")
	// Both contain "komp"; the more recently pushed one stays first even
	// though the other is the tighter fuzzy match. "compiler" is not a match.
	equal(t, "filtered rows", f.names(), []string{"Kompell/claude", "ikiuscompany/kompass"})

	f.press("ctrl+u", "kcl") // not a substring of anything: fuzzy only
	equal(t, "fuzzy rows", f.names(), []string{"Kompell/claude"})
}

func TestEnterOnProjectGoesThere(t *testing.T) {
	f := newFixture(t, nil)
	f.press("local", "enter")
	if !f.quit || f.Result != filepath.Join(f.root, "local") {
		t.Fatalf("quit=%v Result=%q", f.quit, f.Result)
	}
}

func TestEscClearsFilterBeforeQuitting(t *testing.T) {
	f := newFixture(t, nil)
	f.press("x", "esc")
	if f.quit || f.cur().query != "" {
		t.Fatalf("first esc: quit=%v query=%q", f.quit, f.cur().query)
	}
	f.press("esc")
	if !f.quit || f.Result != "" {
		t.Fatalf("second esc: quit=%v Result=%q", f.quit, f.Result)
	}
}

func TestCloneDefaultsToOwnerContainer(t *testing.T) {
	f := newFixture(t, nil)
	f.press("tab", "enter")
	if f.prompt == nil || f.prompt.input != "Kompell" {
		t.Fatalf("prompt = %+v", f.prompt)
	}
	equal(t, "containers offered", f.prompt.options, []string{"Kompell", "demos", ""})

	f.press("enter")
	target := filepath.Join(f.root, "Kompell", "claude")
	equal(t, "clones", f.cloned, []string{"Kompell/claude -> " + target})
	if !f.quit || f.Result != target {
		t.Fatalf("quit=%v Result=%q", f.quit, f.Result)
	}
}

func TestCloneIntoChosenContainer(t *testing.T) {
	f := newFixture(t, nil)
	f.press("tab", "enter", "tab") // owner -> demos
	if f.prompt.input != "demos" {
		t.Fatalf("after tab: input = %q", f.prompt.input)
	}
	f.press("ctrl+u", "work", "enter") // or type a new one
	equal(t, "clones", f.cloned, []string{"Kompell/claude -> " + filepath.Join(f.root, "work", "claude")})
}

func TestCloneRefusesExistingTarget(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Projects = nil }) // nothing known as cloned
	os.MkdirAll(filepath.Join(f.root, "Kompell", "claude"), 0o755)
	f.press("tab", "enter", "enter")
	if len(f.cloned) != 0 || f.quit || f.prompt == nil || !strings.Contains(f.prompt.err, "already exists") {
		t.Fatalf("cloned=%v quit=%v prompt=%+v", f.cloned, f.quit, f.prompt)
	}
}

func TestCloneFailureStaysOpen(t *testing.T) {
	f := newFixture(t, nil)
	f.cloneErr = errors.New("fatal: repository not found")
	f.press("tab", "enter", "enter")
	if f.quit || f.Result != "" || !f.statusErr || !strings.Contains(f.status, "repository not found") {
		t.Fatalf("quit=%v Result=%q status=%q", f.quit, f.Result, f.status)
	}
	if f.busy != "" || f.prompt != nil {
		t.Fatalf("still busy=%q prompt=%v", f.busy, f.prompt)
	}
}

// inRepo makes the fixture behave as if launched inside Kompell/kompose.
func inRepo(startPR bool) func(*Options) {
	return func(o *Options) {
		o.Here = &o.Projects[0]
		o.Cwd = filepath.Join(o.Projects[0].Path, "src")
		o.StartPR = startPR
	}
}

func TestPRViewExistsOnlyInsideARepo(t *testing.T) {
	f := newFixture(t, nil)
	for range 4 {
		f.press("tab")
		if f.view == viewPRs {
			t.Fatal("tab reached the PR view outside a repo")
		}
	}
	if strings.Contains(f.render(), "PRs") {
		t.Fatalf("PR tab drawn outside a repo:\n%s", f.render())
	}
	for _, call := range f.ghCalls {
		if strings.HasPrefix(call, "pr ") {
			t.Fatalf("fetched pull requests outside a repo: %s", call)
		}
	}

	f = newFixture(t, inRepo(false))
	var seen []viewID
	for range 3 {
		f.press("tab")
		seen = append(seen, f.view)
	}
	if seen[0] != viewAdd || seen[1] != viewPRs || seen[2] != viewProjects {
		t.Fatalf("tab order inside a repo = %v", seen)
	}
	if !strings.Contains(f.render(), "PRs · Kompell/kompose") {
		t.Fatalf("PR tab not drawn inside a repo:\n%s", f.render())
	}
}

func TestCheckoutFromInsideTheRepoDoesNotMoveTheShell(t *testing.T) {
	f := newFixture(t, inRepo(true))
	if f.view != viewPRs {
		t.Fatalf("view = %v, want the PR view", f.view)
	}
	equal(t, "PR rows", f.names(), []string{"#7 Add thing"})

	f.press("enter")
	equal(t, "checkouts", f.checked, []string{filepath.Join(f.root, "Kompell", "kompose") + " #7"})
	if !f.quit || f.Result != "" || !strings.Contains(f.Message, "checked out #7") {
		t.Fatalf("quit=%v Result=%q Message=%q", f.quit, f.Result, f.Message)
	}
}

func TestCheckoutFailureLeavesYouInThePicker(t *testing.T) {
	f := newFixture(t, inRepo(true))
	f.checkErr = errors.New("error: Your local changes would be overwritten\nAborting")
	f.press("enter")
	if f.quit || f.Result != "" || !f.errorDetail() {
		t.Fatalf("quit=%v Result=%q status=%q", f.quit, f.Result, f.status)
	}
	if out := f.render(); !strings.Contains(out, "Your local changes would be overwritten") {
		t.Fatalf("error not shown in full:\n%s", out)
	}
	f.press("enter") // dismisses; must not retry the checkout
	if len(f.checked) != 1 || f.statusErr {
		t.Fatalf("checked=%v statusErr=%v", f.checked, f.statusErr)
	}
}

func TestCloneNeverChecksAnythingOut(t *testing.T) {
	f := newFixture(t, inRepo(false))
	f.press("tab", "enter", "enter")
	if len(f.cloned) != 1 || len(f.checked) != 0 || !f.quit {
		t.Fatalf("cloned=%v checked=%v quit=%v", f.cloned, f.checked, f.quit)
	}
}

func TestKeysAreIgnoredWhileBusyAndCtrlCCancels(t *testing.T) {
	f := newFixture(t, nil)
	cancelled := false
	f.busy, f.cancel = "cloning x", func() { cancelled = true }
	f.press("esc", "enter", "x")
	if f.quit || f.cur().query != "" {
		t.Fatalf("keys got through while busy: quit=%v query=%q", f.quit, f.cur().query)
	}
	f.press("ctrl+c")
	if !cancelled || f.quit {
		t.Fatalf("cancelled=%v quit=%v", cancelled, f.quit)
	}
}

func TestRenderFitsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {60, 12}, {20, 5}, {200, 60}} {
		f := newFixture(t, inRepo(false))
		f.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, keys := range [][]string{nil, {"tab"}, {"tab"}, {"tab", "tab", "enter"}} {
			f.press(keys...)
			lines := strings.Split(f.render(), "\n")
			if len(lines) != max(size[1], chrome+1) {
				t.Fatalf("%v after %v: %d lines", size, keys, len(lines))
			}
			for _, l := range lines {
				if w := lipglossWidth(l); w > size[0] {
					t.Fatalf("%v after %v: line is %d wide: %q", size, keys, w, l)
				}
			}
		}
	}
}

func lipglossWidth(s string) int { return ansi.StringWidth(s) }
