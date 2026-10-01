# pj

A project manager for a directory of git repos, in two layers that share one
layout (filter on top, list left, preview right).

The first layer is about which project:

| View | Shows | Enter |
|---|---|---|
| **Projects** | every git repo under the root | open that project |
| **Add** | your GitHub repos that are not cloned yet, last pushed first | clone, then go there |

The second is one project:

| View | Shows | Enter |
|---|---|---|
| **Project** | a "Go to project" row, then its open pull requests | go there / check the PR out |

Run inside a repo, pj opens straight onto that project; esc backs out to the
list.

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

    pj            the project you are in, or the list when you are not in one
    pj projects   the list, even from inside a project
    pj add        start on the Add view
    pj pr         the project you are in, cursor on its first pull request

## Keys

| Key | Action |
|---|---|
| type | filter |
| `enter` | open / clone / go there / check out |
| `tab` | switch between Projects and Add |
| `ctrl-o` | open in the browser |
| `ctrl-y` | copy the URL |
| `pgup` `pgdn` | scroll the preview |
| `ctrl-u` | clear the filter |
| `esc` | clear the filter, leave the project view, or quit |
| `ctrl-c` | cancel a running clone or checkout; otherwise quit |

## Code

    internal/config    where the root is
    internal/projects  what is on disk; no UI, no network
    internal/github    repos, PRs and READMEs through gh; no UI
    internal/nav       the cd hand-off
    internal/tui       Bubble Tea model: one picker component, used by every view
