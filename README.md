# lazyprojects

I keep everything in `~/Code`, one folder per GitHub org. lazyprojects is what
I open to get around in there: jump to a project, clone one I don't have yet
into the right folder, or check out a PR.

It's not a replacement for lazygit or gh-dash. lazygit is for what's inside a
repo and gh-dash is for keeping up with GitHub. This is only for managing the
projects themselves, so I don't have to scroll through a hundred repos and
blank on which one I wanted.

## How it works

Everything is the same picker: a filter on top, a list on the left, a preview
on the right. Typing always filters. Enter or `→` goes in, `←` goes back, `esc`
closes it.

| Layer | Shows | Enter |
|---|---|---|
| **Projects** | every git repo under the root | open that project |
| **Add** | your GitHub repos that aren't cloned yet, last pushed first | clone it, then go there |
| **Project** | what you can do with it: go there, open a tmux session, pull requests, open on GitHub, delete | do it |
| **Pull requests** | that project's open PRs | check it out |

Projects and Add are the first layer, switched with `tab`. Run it inside a
repo and it opens on that project instead of the list.

When you filter, rows that contain what you typed come first, in their
original order. Looser fuzzy matches follow.

## Layout on disk

Projects live under one root, `~/Code` by default, grouped into containers
named after the GitHub owner:

    ~/Code/acme/website
    ~/Code/yourname/dotfiles

A clone goes into its owner's container. The clone prompt lets you pick
another existing container, type a new one, or clear it to clone straight into
the root.

Set the root with `--root`, `$LAZYPROJECTS_ROOT`, or `root = ~/somewhere` in
`~/.config/lazyprojects/config`.

## Deleting

Delete removes the directory for good. Before it does, it lists what exists
nowhere else: uncommitted files, unpushed commits, stashes, ignored files such
as `.env`, or a project with no remote at all. If there is anything, you have
to type the project's name. You can't delete the project you're standing in.

## Install

    go install github.com/HenriAnttila/lazyprojects@latest

or, from a clone:

    go build -o ~/.local/bin/lazyprojects .

Needs `gh` (logged in) and `git`.

## Changing directory

A program can't change its parent shell's directory, so the path is handed
over in one of two ways:

- **Typed in a shell:** add `eval "$(lazyprojects init zsh)"` (or `bash`,
  `fish`) to your rc file. It defines a function that runs the binary and then
  `cd`s.
- **From a tmux popup:** pass the pane underneath, and the `cd` is typed into
  it:

      bind -n M-p run-shell "tmux display-popup -E -w 80% -h 70% -d '#{pane_current_path}' 'lazyprojects --pane #{pane_id}'"

  This refuses when the pane is running something other than a shell.

Without either, it prints the path.

## tmux sessions

"Open in tmux session" on a project opens it in a session named after the
repo, `website` for `~/Code/acme/website`. If a session by that name already
exists, it switches to that one instead of making another. Run outside tmux,
it starts tmux in the terminal you're in.

## Commands

    lazyprojects            the project you're in, or the list when you're not in one
    lazyprojects projects   the list, even from inside a project
    lazyprojects add        start on Add
    lazyprojects pr         the pull requests of the project you're in

## Keys

| Key | Action |
|---|---|
| type | filter |
| `enter` | open / clone / go there / check out |
| `→` | go in a layer. Never goes there, clones or checks out |
| `←` | go back a layer; cancels a prompt |
| `tab` | switch between Projects and Add |
| `ctrl-o` | open in the browser |
| `ctrl-y` | copy the URL |
| `pgup` `pgdn` | scroll the preview |
| `ctrl-u` | clear the filter |
| `esc` | quit, from anywhere |
| `ctrl-c` | cancel a running clone or checkout; otherwise quit |

## Code

Go and [Bubble Tea](https://github.com/charmbracelet/bubbletea). Colours are
the terminal's 16 ANSI colours with no background, so it follows whatever
theme the terminal has.

    internal/config    where the root is
    internal/projects  what's on disk; no UI, no network
    internal/github    repos, PRs and READMEs through gh; no UI
    internal/nav       the cd hand-off
    internal/tui       the Bubble Tea model: one picker, used by every layer
