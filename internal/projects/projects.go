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

// Risk is what deleting a project's directory would lose: everything in it
// that exists nowhere else.
type Risk struct {
	NoRemote bool     // no origin at all: the directory is the only copy
	Commits  int      // with NoRemote, how many commits that is
	Dirty    int      // uncommitted or untracked paths
	Unpushed []string // "branch (2 commits)" for commits on no remote
	Stashes  int
	Ignored  []string // ignored files, e.g. .env; ignored directories are build output
}

// Safe reports whether nothing would be lost.
func (r Risk) Safe() bool {
	return !r.NoRemote && r.Dirty == 0 && len(r.Unpushed) == 0 && r.Stashes == 0 && len(r.Ignored) == 0
}

// Assess inspects a project before it is deleted. It reads what the clone
// already knows and does not fetch, so "pushed" means pushed as of the last
// time this clone talked to its remote.
func Assess(dir string) (Risk, error) {
	var r Risk
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return r, fmt.Errorf("%s is not a git repo", dir)
	}
	if n, err := Dirty(dir); err == nil {
		r.Dirty = n
	}
	if out, _ := git(dir, "stash", "list"); out != "" {
		r.Stashes = strings.Count(out, "\n") + 1
	}
	// Ignored files (not directories, which are node_modules and build
	// output) are typically local config and secrets that are in no commit.
	if out, _ := git(dir, "--no-optional-locks", "status", "--porcelain", "--ignored"); out != "" {
		for _, line := range strings.Split(out, "\n") {
			if name, ok := strings.CutPrefix(line, "!! "); ok && !strings.HasSuffix(name, "/") {
				r.Ignored = append(r.Ignored, name)
			}
		}
	}
	if _, err := git(dir, "config", "--get", "remote.origin.url"); err != nil {
		r.NoRemote = true
		if out, err := git(dir, "rev-list", "--count", "--all"); err == nil {
			fmt.Sscan(out, &r.Commits)
		}
		return r, nil
	}
	branches, _ := git(dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	for _, b := range strings.Fields(branches) {
		out, err := git(dir, "rev-list", "--count", b, "--not", "--remotes")
		var n int
		if err != nil {
			continue
		}
		if fmt.Sscan(out, &n); n == 1 {
			r.Unpushed = append(r.Unpushed, b+" (1 commit)")
		} else if n > 1 {
			r.Unpushed = append(r.Unpushed, fmt.Sprintf("%s (%d commits)", b, n))
		}
	}
	return r, nil
}

// Delete removes a project's directory for good. It refuses anything that is
// not a git repo strictly inside the root, so a wrong path cannot take the
// root, a container, or something outside it.
func Delete(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to delete %s: not inside %s", dir, root)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Errorf("refusing to delete %s: not a git repo", dir)
	}
	return os.RemoveAll(dir)
}
