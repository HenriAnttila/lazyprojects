// pj is a project manager for a root directory of git repos: go to a project,
// clone one from GitHub into its owner's container, check out a pull request.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/HenriAnttila/pj/internal/config"
	"github.com/HenriAnttila/pj/internal/github"
	"github.com/HenriAnttila/pj/internal/nav"
	"github.com/HenriAnttila/pj/internal/projects"
	"github.com/HenriAnttila/pj/internal/tui"
)

const usage = `usage: pj [flags] [command]

commands:
  (none)        pick a project under the root and go to it
  add           pick a GitHub repo to clone into the root
  pr            pick a pull request of the repo you are in and check it out
  init <shell>  print the shell function that lets pj change directory
                (zsh, bash, sh, fish); add  eval "$(pj init zsh)"  to your rc

flags:
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pj:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("pj", flag.ExitOnError)
	root := fs.String("root", "", "directory projects live under (default $PJ_ROOT, the config file, then ~/Code)")
	pane := fs.String("pane", "", "tmux pane to cd when run from a popup, e.g. '#{pane_id}'")
	cdFile := fs.String("cd-file", "", "write the chosen directory here; used by the `pj init` shell function")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])

	command := fs.Arg(0)
	if command == "init" {
		script, err := nav.InitScript(fs.Arg(1))
		if err != nil {
			return err
		}
		fmt.Print(script)
		return nil
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	found, err := projects.Scan(cfg.Root)
	if err != nil {
		return fmt.Errorf("root %s: %w (set it with --root, $PJ_ROOT, or `root = ...` in the config file)", cfg.Root, err)
	}
	cwd, _ := os.Getwd()
	opts := tui.Options{
		Root:      cfg.Root,
		Cwd:       cwd,
		Projects:  found,
		Client:    github.New(),
		CachePath: github.CachePath(),
	}

	switch command {
	case "", "projects":
	case "add":
		opts.StartAdd = true
	case "pr":
		// The repo you are standing in, wherever it is: it need not be under
		// the root for its pull requests to be worth checking out.
		top, err := projects.Toplevel(cwd)
		if err != nil {
			return fmt.Errorf("pr: %s is not inside a git repo", cwd)
		}
		here := projects.Load(cfg.Root, top)
		if !here.IsGitHub() {
			return fmt.Errorf("pr: %s has no GitHub remote", top)
		}
		opts.StartPR = &here
	default:
		fs.Usage()
		return fmt.Errorf("unknown command %q", command)
	}

	final, err := tea.NewProgram(tui.New(opts)).Run()
	if err != nil {
		return err
	}
	m := final.(*tui.Model)
	if m.Message != "" {
		fmt.Fprintln(os.Stderr, m.Message)
	}
	if m.Result == "" {
		return nil
	}
	return nav.Target{Pane: *pane, CdFile: *cdFile}.Go(m.Result)
}
