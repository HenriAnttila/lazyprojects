// Package nav moves the user's shell to a directory after the TUI exits.
//
// A program cannot change the working directory of the shell that started it,
// so one of three hand-offs is used, depending on how lazyprojects was launched:
//
//   - from a tmux popup, the cd is typed into the pane underneath (--pane);
//   - from the shell function printed by `lazyprojects init`, the path is written to a
//     file the function reads and cds to (--cd-file);
//   - with neither, the path is printed, which is the best a bare binary can do.
//
// Session is the other way to get there: a tmux session in the directory,
// which moves no shell at all.
package nav

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Session opens dir in the tmux session named after it, creating the session
// when there is none by that name yet. Inside tmux the client switches to it;
// attaching there would nest. Outside, tmux takes over this terminal.
func Session(dir string) error {
	name := SessionName(dir)
	if os.Getenv("TMUX") == "" {
		cmd := exec.Command("tmux", "new-session", "-A", "-s", name, "-c", dir)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	// = makes the match exact; a bare name also matches by prefix.
	if exec.Command("tmux", "has-session", "-t", "="+name).Run() != nil {
		if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", dir).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux new-session: %s", strings.TrimSpace(string(out)))
		}
	}
	if out, err := exec.Command("tmux", "switch-client", "-t", "="+name).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux switch-client: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// SessionName is the directory's own name, with the two characters tmux
// rewrites in a session name replaced the way tmux would.
func SessionName(dir string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(filepath.Base(dir))
}

type Target struct {
	Pane   string // tmux pane id to send the cd to
	CdFile string // file the shell wrapper reads the path from
}

// shells are the pane commands that will read a typed `cd` as a command. In
// anything else (nvim, claude, lazygit) the keystrokes would land as input.
var shells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true,
}

// Go hands dir to whichever mechanism is configured.
func (t Target) Go(dir string) error {
	switch {
	case t.Pane != "":
		return t.tmux(dir)
	case t.CdFile != "":
		return os.WriteFile(t.CdFile, []byte(dir), 0o600)
	default:
		_, err := fmt.Println(dir)
		return err
	}
}

func (t Target) tmux(dir string) error {
	cmd, err := tmuxOut("display", "-p", "-t", t.Pane, "#{pane_current_command}")
	if err != nil {
		return fmt.Errorf("tmux pane %s: %w", t.Pane, err)
	}
	if !shells[cmd] {
		return fmt.Errorf("pane is running %s, not a shell; cd there yourself:\n  %s", cmd, dir)
	}
	if mode, _ := tmuxOut("display", "-p", "-t", t.Pane, "#{pane_in_mode}"); mode != "0" {
		return fmt.Errorf("pane is in copy mode; press q, then cd there yourself:\n  %s", dir)
	}
	// C-u first, so a half-typed command is discarded rather than having the
	// cd appended to it.
	if err := exec.Command("tmux", "send-keys", "-t", t.Pane, "C-u").Run(); err != nil {
		return err
	}
	return exec.Command("tmux", "send-keys", "-t", t.Pane, "cd -- "+Quote(dir), "Enter").Run()
}

func tmuxOut(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// Quote single-quotes s for a POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// InitScript is the shell function that makes `lazyprojects` change directory. It shadows
// the binary, runs it with a temp file to write the chosen path to, and cds
// there if one was written.
func InitScript(shell string) (string, error) {
	switch shell {
	case "zsh", "bash", "sh":
		return `lazyprojects() {
  local f d
  f=$(mktemp "${TMPDIR:-/tmp}/lazyprojects-cd.XXXXXX") || return
  command lazyprojects --cd-file "$f" "$@"
  d=$(cat "$f" 2>/dev/null)
  rm -f "$f"
  [ -n "$d" ] && cd -- "$d"
}
`, nil
	case "fish":
		return `function lazyprojects
  set -l f (mktemp -t lazyprojects-cd.XXXXXX); or return
  command lazyprojects --cd-file $f $argv
  set -l d (cat $f 2>/dev/null)
  rm -f $f
  test -n "$d"; and cd -- $d
end
`, nil
	}
	return "", fmt.Errorf("unsupported shell %q (zsh, bash, sh, fish)", shell)
}
