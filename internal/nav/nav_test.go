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

// The init function is what makes `pj` change directory, so run it for real:
// a stand-in pj binary writes a path to --cd-file and the shell must end up there.
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
			fake := "#!/bin/sh\n[ \"$1\" = --cd-file ] && printf %s \"$PJ_DEST\" > \"$2\"\n"
			os.WriteFile(filepath.Join(bin, "pj"), []byte(fake), 0o755)

			cmd := exec.Command(shell, "-c", script+"\npj\npwd")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "PJ_DEST="+dest)
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
