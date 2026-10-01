package projects

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := []struct{ url, host, owner, name string }{
		{"git@github.com:Kompell/kompose.git", "github.com", "Kompell", "kompose"},
		{"git@github.com:Kompell/kompose", "github.com", "Kompell", "kompose"},
		{"https://github.com/HenriAnttila/dotfiles.git", "github.com", "HenriAnttila", "dotfiles"},
		{"https://token@github.com/HenriAnttila/dotfiles", "github.com", "HenriAnttila", "dotfiles"},
		{"ssh://git@github.com/Kompell/claude.git", "github.com", "Kompell", "claude"},
		{"ssh://git@github.com:22/Kompell/claude.git", "github.com", "Kompell", "claude"},
		{"git@ssh.dev.azure.com:v3/sisuauto/Verkkosivut/Verkkosivut", "ssh.dev.azure.com", "v3/sisuauto/Verkkosivut", "Verkkosivut"},
		{"/srv/git/thing.git", "", "", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		host, owner, name := ParseRemote(c.url)
		if host != c.host || owner != c.owner || name != c.name {
			t.Errorf("ParseRemote(%q) = %q %q %q, want %q %q %q", c.url, host, owner, name, c.host, c.owner, c.name)
		}
	}
}

func initRepo(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "Kompell", "kompose"), "git@github.com:Kompell/kompose.git")
	initRepo(t, filepath.Join(root, "flat"), "")
	initRepo(t, filepath.Join(root, "Azure", "site"), "git@ssh.dev.azure.com:v3/org/proj/site")
	// Never descended into or listed.
	initRepo(t, filepath.Join(root, "flat", "nested"), "")
	initRepo(t, filepath.Join(root, "node_modules", "dep"), "")
	initRepo(t, filepath.Join(root, ".hidden", "x"), "")
	initRepo(t, filepath.Join(root, "a", "b", "c", "too-deep"), "")

	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, p := range got {
		rels = append(rels, p.Rel)
	}
	want := []string{"Azure/site", "flat", "Kompell/kompose"}
	if !reflect.DeepEqual(rels, want) {
		t.Fatalf("Scan rels = %v, want %v", rels, want)
	}
	if got[2].Slug() != "Kompell/kompose" || got[2].Branch != "main" {
		t.Errorf("kompose = %+v", got[2])
	}
	if got[0].IsGitHub() || got[1].IsGitHub() {
		t.Errorf("non-GitHub projects reported as GitHub: %+v %+v", got[0], got[1])
	}
}

func TestContainers(t *testing.T) {
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "Kompell", "kompose"), "")
	initRepo(t, filepath.Join(root, "flat"), "")
	os.MkdirAll(filepath.Join(root, "demos"), 0o755)
	os.MkdirAll(filepath.Join(root, ".cache"), 0o755)
	os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644)

	got := Containers(root)
	want := []string{"Kompell", "demos"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Containers = %v, want %v", got, want)
	}
}

func TestTarget(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Kompell", "kompose"), 0o755)

	if got, err := Target(root, "Kompell", "claude"); err != nil || got != filepath.Join(root, "Kompell", "claude") {
		t.Errorf("Target = %q, %v", got, err)
	}
	if got, err := Target(root, "", "claude"); err != nil || got != filepath.Join(root, "claude") {
		t.Errorf("Target with no container = %q, %v", got, err)
	}
	for _, c := range []struct{ container, name string }{
		{"Kompell", "kompose"}, // exists
		{"..", "x"},
		{"a/b", "x"},
		{"Kompell", "../x"},
		{"Kompell", ""},
	} {
		if got, err := Target(root, c.container, c.name); err == nil {
			t.Errorf("Target(%q, %q) = %q, want an error", c.container, c.name, got)
		}
	}
}

func TestDirty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "r")
	initRepo(t, dir, "")
	if n, err := Dirty(dir); err != nil || n != 0 {
		t.Fatalf("clean repo: Dirty = %d, %v", n, err)
	}
	os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b"), []byte("x"), 0o644)
	if n, err := Dirty(dir); err != nil || n != 2 {
		t.Fatalf("two untracked files: Dirty = %d, %v", n, err)
	}
}
