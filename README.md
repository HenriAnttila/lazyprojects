# pj

A project manager for a directory of git repos. The views share one layout
(filter on top, list left, preview right):

| View | Shows | Enter |
|---|---|---|
| **Projects** | every git repo under the root | go there |
| **Add** | your GitHub repos that are not cloned yet, last pushed first | clone, then go there |
| **PRs** | open pull requests of the repo you are in | check out |

Projects and Add are about which project. PRs is about which branch of the one
you are already in, so it only exists when pj is run inside a GitHub repo.

Typing always filters. Rows containing what you typed come first in their
original order; looser fuzzy matches follow.

## Layout on disk

Projects live under one root, default `~/Code`, grouped into containers named
after the GitHub owner:

    ~/Code/Kompell/kompose
    ~/Code/HenriAnttila/pj

A clone goes into its owner's container. The clone prompt lets you pick another
existing container, type a new one, or clear it to clone straight into the root.

Set the root with `--root`, `$PJ_ROOT`, or `root = ~/somewhere` in
`~/.config/pj/config`.

## Install

    go build -o ~/.local/bin/pj .

Needs `gh` (authenticated) and `git`.

## Changing directory

A program cannot change its parent shell's directory, so pj hands the path over
in one of two ways:

- **Typed in a shell:** add `eval "$(pj init zsh)"` (or `bash`, `fish`) to your
  rc file. It defines a `pj` function that runs the binary and then `cd`s.
- **From a tmux popup:** pass the pane underneath, and pj types the `cd` into it:

      bind -n M-p run-shell "tmux display-popup -E -w 80% -h 70% -d '#{pane_current_path}' 'pj --pane #{pane_id}'"

  This refuses when the pane is running something other than a shell.

Without either, pj prints the path.

## Commands

    pj            projects
    pj add        start on the Add view
    pj pr         start on the PR view; errors outside a GitHub repo

## Keys

| Key | Action |
|---|---|
| type | filter |
| `enter` | go / clone / check out |
| `tab` `shift-tab` | next / previous view |
| `ctrl-o` | open in the browser |
| `ctrl-y` | copy the URL |
| `pgup` `pgdn` | scroll the preview |
| `ctrl-u` | clear the filter |
| `esc` | clear the filter, or quit |
| `ctrl-c` | cancel a running clone or checkout; otherwise quit |

## Code

    internal/config    where the root is
    internal/projects  what is on disk; no UI, no network
    internal/github    repos, PRs and READMEs through gh; no UI
    internal/nav       the cd hand-off
    internal/tui       Bubble Tea model: one picker component, one per view
