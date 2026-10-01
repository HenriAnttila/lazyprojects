package nav

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b":       "'/a/b'",
		"/a b/c":     "'/a b/c'",
		"/it's/here": `'/it'\''s/here'`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCdFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cd")
	if err := (Target{CdFile: f}).Go("/some/dir"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); string(got) != "/some/dir" {
		t.Fatalf("cd file = %q", got)
	}
}

func TestSessionName(t *testing.T) {
	for in, want := range map[string]string{
		"/code/acme/website": "website",
		"/code/acme/next.js": "next_js",
		"/code/acme/a:b":     "a_b",
	} {
		if got := SessionName(in); got != want {
			t.Errorf("SessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A stand-in tmux records what it was asked to do, and answers has-session
// with whatever $FAKE_HAS says.
func TestSession(t *testing.T) {
	for name, tc := range map[string]struct {
		tmux, has string
		want      []string
	}{
		"inside tmux, no session yet": {"sock,1,0", "1", []string{
			"has-session -t =next_js",
			"new-session -d -s next_js -c /code/acme/next.js",
			"switch-client -t =next_js",
		}},
		"inside tmux, session exists": {"sock,1,0", "0", []string{
			"has-session -t =next_js",
			"switch-client -t =next_js",
		}},
		"outside tmux": {"", "1", []string{
			"new-session -A -s next_js -c /code/acme/next.js",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			bin, log := t.TempDir(), filepath.Join(t.TempDir(), "log")
			fake := "#!/bin/sh\necho \"$*\" >> \"$FAKE_LOG\"\n[ \"$1\" = has-session ] && exit \"$FAKE_HAS\"\nexit 0\n"
			os.WriteFile(filepath.Join(bin, "tmux"), []byte(fake), 0o755)
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			t.Setenv("FAKE_LOG", log)
			t.Setenv("FAKE_HAS", tc.has)
			t.Setenv("TMUX", tc.tmux)

			if err := Session("/code/acme/next.js"); err != nil {
				t.Fatal(err)
			}
			out, _ := os.ReadFile(log)
			if got := strings.TrimSpace(string(out)); got != strings.Join(tc.want, "\n") {
				t.Fatalf("tmux was asked:\n%s\nwant:\n%s", got, strings.Join(tc.want, "\n"))
			}
		})
	}
}

// The init function is what makes `lazyprojects` change directory, so run it for real:
// a stand-in lazyprojects binary writes a path to --cd-file and the shell must end up there.
func TestInitScriptChangesDirectory(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "sh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(shell, func(t *testing.T) {
			script, err := InitScript(shell)
			if err != nil {
				t.Fatal(err)
			}
			bin, dest := t.TempDir(), filepath.Join(t.TempDir(), "it's here")
			os.MkdirAll(dest, 0o755)
			fake := "#!/bin/sh\n[ \"$1\" = --cd-file ] && printf %s \"$LAZYPROJECTS_DEST\" > \"$2\"\n"
			os.WriteFile(filepath.Join(bin, "lazyprojects"), []byte(fake), 0o755)

			cmd := exec.Command(shell, "-c", script+"\nlazyprojects\npwd")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "LAZYPROJECTS_DEST="+dest)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != dest {
				t.Fatalf("shell ended in %q, want %q", got, dest)
			}
		})
	}
	if _, err := InitScript("powershell"); err == nil {
		t.Fatal("an unsupported shell should be an error")
	}
}
