package tui

import (
	"context"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/HenriAnttila/pj/internal/projects"
)

// prompt asks which container a clone goes into. It starts on the repo's
// owner, and tab walks the containers that already exist under the root.
type prompt struct {
	slug, name string
	input      string
	options    []string // owner first, then existing containers, then "" for the root
	option     int
	target     string // set once the clone starts
	err        string
}

func (m *Model) openPrompt(slug string) {
	owner, name, _ := strings.Cut(slug, "/")
	options := []string{owner}
	for _, c := range projects.Containers(m.opts.Root) {
		if !slices.Contains(options, c) {
			options = append(options, c)
		}
	}
	options = append(options, "")
	m.prompt = &prompt{slug: slug, name: name, input: owner, options: options}
}

func (m *Model) promptKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.prompt
	p.err = ""
	switch msg.String() {
	case "esc":
		m.prompt = nil
	case "tab", "down", "ctrl+n":
		p.option = (p.option + 1) % len(p.options)
		p.input = p.options[p.option]
	case "shift+tab", "up", "ctrl+p":
		p.option = (p.option + len(p.options) - 1) % len(p.options)
		p.input = p.options[p.option]
	case "backspace":
		if in := []rune(p.input); len(in) > 0 {
			p.input = string(in[:len(in)-1])
		}
	case "ctrl+u":
		p.input = ""
	case "enter":
		target, err := projects.Target(m.opts.Root, p.input, p.name)
		if err != nil {
			p.err = err.Error()
			return nil
		}
		p.target = target
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		m.busy, m.progress = "cloning "+p.slug, ""
		return m.cloneCmd(ctx, p.slug, target)
	default:
		p.input += typed(msg)
	}
	return nil
}
