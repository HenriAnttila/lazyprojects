package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/HenriAnttila/pj/internal/github"
	"github.com/HenriAnttila/pj/internal/projects"
)

// Everything slow happens in a tea.Cmd and comes back as one of these
// messages; Update itself never blocks.
type (
	dirtyMsg struct {
		path string
		n    int
	}
	reposMsg struct {
		repos []github.Repo
		err   error
	}
	prsMsg struct {
		prs []github.PR
		err error
	}
	hoverMsg   struct{ seq int }
	previewMsg struct{ path, text string }
	readmeMsg  struct {
		slug, text string
		err        error
	}
	cloneMsg struct {
		ch   <-chan cloneEvent
		line string
		done bool
		err  error
	}
	checkoutMsg struct {
		dir    string
		number int
		err    error
	}
	noticeMsg struct {
		text string
		err  error
	}
)

type cloneEvent struct {
	line string
	done bool
	err  error
}

// hoverDelay is how long the cursor has to rest on a row before its preview is
// fetched, so holding an arrow key does not fire a request per row.
var hoverDelay = 150 * time.Millisecond

func hoverCmd(seq int) tea.Cmd {
	return tea.Tick(hoverDelay, func(time.Time) tea.Msg { return hoverMsg{seq} })
}

func dirtyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		n, err := projects.Dirty(path)
		if err != nil {
			return nil
		}
		return dirtyMsg{path, n}
	}
}

func previewCmd(path string) tea.Cmd {
	return func() tea.Msg { return previewMsg{path, projects.Preview(path)} }
}

func (m *Model) reposCmd() tea.Cmd {
	client, cache := m.opts.Client, m.opts.CachePath
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		repos, err := client.Repos(ctx)
		if err == nil && cache != "" {
			_ = github.SaveCache(cache, repos)
		}
		return reposMsg{repos, err}
	}
}

func (m *Model) prsCmd() tea.Cmd {
	client, slug := m.opts.Client, m.opts.Here.Slug()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prs, err := client.PRs(ctx, slug)
		return prsMsg{prs, err}
	}
}

func (m *Model) readmeCmd(slug string) tea.Cmd {
	client := m.opts.Client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text, err := client.Readme(ctx, slug)
		return readmeMsg{slug, text, err}
	}
}

// cloneCmd starts a clone and reports its progress line by line. Each message
// carries the channel, and Update asks for the next one until done.
func (m *Model) cloneCmd(ctx context.Context, slug, target string) tea.Cmd {
	clone := m.opts.Clone
	return func() tea.Msg {
		ch := make(chan cloneEvent, 64)
		go func() {
			err := clone(ctx, slug, target, func(line string) {
				select {
				case ch <- cloneEvent{line: line}:
				default: // never let a slow UI stall git
				}
			})
			ch <- cloneEvent{done: true, err: err}
			close(ch)
		}()
		return waitClone(ch)()
	}
}

func waitClone(ch <-chan cloneEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return cloneMsg{ch: ch, line: ev.line, done: ev.done, err: ev.err}
	}
}

func (m *Model) checkoutCmd(ctx context.Context, dir string, number int) tea.Cmd {
	checkout := m.opts.Checkout
	return func() tea.Msg {
		return checkoutMsg{dir, number, checkout(ctx, dir, number)}
	}
}

func (m *Model) browseCmd(args ...string) tea.Cmd {
	client := m.opts.Client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := client.Run(ctx, args...); err != nil {
			return noticeMsg{err: err}
		}
		return noticeMsg{text: "opened in browser"}
	}
}

// copyCmd puts text on the clipboard with the platform's own tool, falling
// back to the terminal's OSC 52. The fallback alone is not enough: tmux's
// default set-clipboard=external ignores OSC 52 from programs inside it.
func copyCmd(text string) tea.Cmd {
	for _, tool := range [][]string{{"wl-copy"}, {"pbcopy"}, {"xclip", "-selection", "clipboard"}} {
		path, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}
		return func() tea.Msg {
			cmd := exec.Command(path, tool[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err != nil {
				return noticeMsg{err: fmt.Errorf("%s: %w", tool[0], err)}
			}
			return noticeMsg{text: "copied " + text}
		}
	}
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg { return noticeMsg{text: "copied " + text} })
}

// Clone runs `gh repo clone`, which uses the protocol gh is configured for,
// and feeds git's progress to the callback as it arrives.
func Clone(ctx context.Context, slug, target string, progress func(string)) (err error) {
	parent := filepath.Dir(target)
	_, statErr := os.Stat(parent)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if os.IsNotExist(statErr) {
		// A container made for this clone should not outlive its failure.
		// os.Remove only succeeds on an empty directory.
		defer func() {
			if err != nil {
				os.Remove(parent)
			}
		}()
	}
	cmd := exec.CommandContext(ctx, "gh", "repo", "clone", slug, target, "--", "--progress")
	// On cancel, SIGTERM the whole process group instead of the default SIGKILL
	// of gh alone: git is gh's child, and only a signal it can catch lets it
	// remove the half-written clone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var tail []string
	sc := bufio.NewScanner(stderr)
	sc.Split(scanProgress)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		progress(line)
		if strings.Contains(line, "% (") {
			continue // a progress counter says nothing about why a clone failed
		}
		if tail = append(tail, line); len(tail) > 4 {
			tail = tail[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return errors.New("cancelled")
		}
		if len(tail) > 0 {
			return errors.New(strings.Join(tail, "\n"))
		}
		return err
	}
	return nil
}

// scanProgress splits on \r as well as \n: git redraws its progress line with
// a carriage return rather than starting a new one.
func scanProgress(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Checkout checks a PR out by number rather than by branch: `gh pr checkout
// <branch>` fails for PRs from forks, whose head branch is not in this repo.
func Checkout(ctx context.Context, dir string, number int) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "checkout", fmt.Sprint(number))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
