// Package projects knows about what is on disk under the root: which
// directories are git repos, where their remotes point, and where a new clone
// should land. It has no UI and no GitHub API calls.
package projects

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// maxDepth reaches root/container/repo with one level to spare, so a repo
// nested a directory deeper than its container is still found.
const maxDepth = 3

type Project struct {
	Path string // absolute
	Rel  string // relative to the root, e.g. Kompell/kompose

	// Remote identity, parsed from origin. All empty when there is no origin.
	Host, Owner, Name string

	Branch string
}

func (p Project) IsGitHub() bool { return p.Host == "github.com" && p.Owner != "" }

// Slug is owner/name for a GitHub project and empty otherwise.
func (p Project) Slug() string {
	if !p.IsGitHub() {
		return ""
	}
	return p.Owner + "/" + p.Name
}

// Scan lists every git repo under root, alphabetically by relative path so a
// project keeps its place in the list between runs.
func Scan(root string) ([]Project, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	var dirs []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			sub := filepath.Join(dir, name)
			// Lstat, not IsDir: linked worktrees and submodules carry a .git file.
			if _, err := os.Lstat(filepath.Join(sub, ".git")); err == nil {
				dirs = append(dirs, sub)
				continue // a repo is a leaf; never list what is inside it
			}
			if depth < maxDepth {
				walk(sub, depth+1)
			}
		}
	}
	walk(root, 1)

	out := make([]Project, len(dirs))
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = Load(root, dir)
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Rel) < strings.ToLower(out[j].Rel)
	})
	return out, nil
}

// Load reads one repo's remote and branch.
func Load(root, dir string) Project {
	p := Project{Path: dir}
	if rel, err := filepath.Rel(root, dir); err == nil {
		p.Rel = rel
	} else {
		p.Rel = dir
	}
	if url, err := git(dir, "config", "--get", "remote.origin.url"); err == nil {
		p.Host, p.Owner, p.Name = ParseRemote(url)
	}
	if b, err := git(dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		p.Branch = b
	} else if sha, err := git(dir, "rev-parse", "--short", "HEAD"); err == nil {
		p.Branch = "@" + sha // detached
	}
	return p
}

// ParseRemote splits a remote URL into host, owner and repo name. It accepts
// scp-style (git@host:owner/name.git), ssh:// and https:// forms. For hosts
// with deeper paths, owner is everything before the last segment.
func ParseRemote(url string) (host, owner, name string) {
	url = strings.TrimSpace(url)
	var path string
	switch {
	case strings.Contains(url, "://"):
		rest := url[strings.Index(url, "://")+3:]
		if at := strings.LastIndex(strings.SplitN(rest, "/", 2)[0], "@"); at >= 0 {
			rest = rest[at+1:]
		}
		host, path, _ = strings.Cut(rest, "/")
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h // drop a port
		}
	case strings.Contains(url, ":"):
		host, path, _ = strings.Cut(url, ":")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	default:
		return "", "", ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return host, "", path
	}
	return host, path[:i], path[i+1:]
}

// Containers lists the directories directly under root that group projects:
// anything that is a directory, not hidden, and not itself a repo.
func Containers(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, name, ".git")); err == nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

// Target is where a clone of name lands. An empty container means directly in
// the root. It refuses a container that would escape the root or nest, and a
// destination that already exists: a collision there is usually the repo you
// wanted, already cloned.
func Target(root, container, name string) (string, error) {
	container = strings.TrimSpace(container)
	if strings.ContainsAny(container, `/\`) || container == "." || container == ".." {
		return "", fmt.Errorf("container must be a single directory name, got %q", container)
	}
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid repo name %q", name)
	}
	dir := filepath.Join(root, container, name)
	if _, err := os.Lstat(dir); err == nil {
		return "", fmt.Errorf("%s already exists", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return dir, nil
}

// Dirty counts changed and untracked paths in a working tree.
func Dirty(dir string) (int, error) {
	out, err := git(dir, "--no-optional-locks", "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	if out == "" {
		return 0, nil
	}
	return strings.Count(out, "\n") + 1, nil
}

// Preview is a short status and recent history for the preview pane, coloured
// by git itself so it follows the terminal's palette.
func Preview(dir string) string {
	// --no-optional-locks: a picker has no business taking git's index lock on
	// a repo that may be open elsewhere.
	status, _ := git(dir, "--no-optional-locks", "-c", "color.status=always", "status", "-sb")
	log, _ := git(dir, "--no-optional-locks", "log", "--color=always", "--oneline", "-10")
	lines := strings.Split(status, "\n")
	if len(lines) > 14 {
		lines = append(lines[:14], fmt.Sprintf("… %d more", len(lines)-14))
	}
	return strings.Join(lines, "\n") + "\n\n" + log
}

// Toplevel returns the repo root containing dir, or an error outside a repo.
func Toplevel(dir string) (string, error) {
	return git(dir, "rev-parse", "--show-toplevel")
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
