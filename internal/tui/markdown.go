package tui

import (
	"strings"

	"charm.land/glamour/v2"
)

// mdStyle renders markdown with ANSI colours only and no backgrounds, for the
// same reason styles.go does: it follows the terminal's palette.
const mdStyle = `{
  "document": {},
  "block_quote": {"indent": 1, "indent_token": "│ ", "color": "8"},
  "list": {"level_indent": 2},
  "heading": {"block_suffix": "\n", "color": "4", "bold": true},
  "h1": {"prefix": "# "},
  "h2": {"prefix": "## "},
  "h3": {"prefix": "### "},
  "h4": {"prefix": "#### "},
  "h5": {"prefix": "##### "},
  "h6": {"prefix": "###### "},
  "strikethrough": {"crossed_out": true},
  "emph": {"italic": true},
  "strong": {"bold": true},
  "hr": {"color": "8", "format": "\n────────\n"},
  "item": {"block_prefix": "• "},
  "enumeration": {"block_prefix": ". "},
  "task": {"ticked": "[x] ", "unticked": "[ ] "},
  "link": {"color": "6", "underline": true},
  "link_text": {"color": "6", "bold": true},
  "image": {"color": "8"},
  "image_text": {"color": "8", "format": "[image: {{.text}}]"},
  "code": {"color": "3"},
  "code_block": {"color": "7", "margin": 2},
  "table": {},
  "definition_description": {"block_prefix": "\n  "},
  "html_block": {"color": "8"},
  "html_span": {"color": "8"}
}`

// markdown renders and caches by source and width: glamour is far too slow to
// run on every frame, and the preview is redrawn on every keypress.
type markdown struct {
	width    int
	renderer *glamour.TermRenderer
	cache    map[string]string
}

func (m *markdown) render(src string, width int) string {
	if width != m.width || m.renderer == nil {
		r, err := glamour.NewTermRenderer(
			glamour.WithStylesFromJSONBytes([]byte(mdStyle)),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return src
		}
		m.width, m.renderer, m.cache = width, r, map[string]string{}
	}
	if out, ok := m.cache[src]; ok {
		return out
	}
	out, err := m.renderer.Render(src)
	if err != nil {
		out = src
	}
	out = strings.Trim(out, "\n")
	m.cache[src] = out
	return out
}
