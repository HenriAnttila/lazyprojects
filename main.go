// lazyprojects is a project manager for a root directory of git repos: go to a project,
// clone one from GitHub into its owner's container, check out a pull request.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/HenriAnttila/lazyprojects/internal/config"
	"github.com/HenriAnttila/lazyprojects/internal/github"
	"github.com/HenriAnttila/lazyprojects/internal/nav"
	"github.com/HenriAnttila/lazyprojects/internal/projects"
	"github.com/HenriAnttila/lazyprojects/internal/tui"
)

const usage = `usage: lazyprojects [flags] [command]

commands:
  (none)        the project you are in, or the list of projects under the root
  projects      the list of projects, even from inside one
  add           pick a GitHub repo to clone into the root
  pr            the pull requests of the project you are in
  init <shell>  print the shell function that lets lazyprojects change directory
                (zsh, bash, sh, fish); add  eval "$(lazyprojects init zsh)"  to your rc

flags:
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyprojects:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("lazyprojects", flag.ExitOnError)
	root := fs.String("root", "", "directory projects live under (default $LAZYPROJECTS_ROOT, the config file, then ~/Code)")
	pane := fs.String("pane", "", "tmux pane to cd when run from a popup, e.g. '#{pane_id}'")
	cdFile := fs.String("cd-file", "", "write the chosen directory here; used by the `lazyprojects init` shell function")
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
		return fmt.Errorf("root %s: %w (set it with --root, $LAZYPROJECTS_ROOT, or `root = ...` in the config file)", cfg.Root, err)
	}
	cwd, _ := os.Getwd()
	opts := tui.Options{
		Root:      cfg.Root,
		Cwd:       cwd,
		Projects:  found,
		Client:    github.New(),
		CachePath: github.CachePath(),
	}

	// The repo you are standing in, wherever it is: it need not be under the
	// root to be the project you want to look at.
	top, topErr := projects.Toplevel(cwd)
	if topErr == nil {
		here := projects.Load(cfg.Root, top)
		opts.Here = &here
	}

	switch command {
	case "":
	case "projects":
		opts.Here = nil // the list, even from inside a project
	case "add":
		opts.StartAdd = true
	case "pr":
		switch {
		case topErr != nil:
			return fmt.Errorf("pr: %s is not inside a git repo", cwd)
		case !opts.Here.IsGitHub():
			return fmt.Errorf("pr: %s has no GitHub remote", top)
		}
		opts.StartPR = true
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
