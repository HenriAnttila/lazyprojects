// Package config resolves where projects live.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	// Root is the directory every project lives under, one container deep.
	Root string
}

// Load resolves the root in order of precedence: the --root flag, $LAZYPROJECTS_ROOT,
// the config file, then ~/Code.
func Load(flagRoot string) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	root := filepath.Join(home, "Code")
	if v := fromFile(Path(home))["root"]; v != "" {
		root = v
	}
	if v := os.Getenv("LAZYPROJECTS_ROOT"); v != "" {
		root = v
	}
	if flagRoot != "" {
		root = flagRoot
	}
	root, err = filepath.Abs(expand(root, home))
	if err != nil {
		return Config{}, err
	}
	return Config{Root: root}, nil
}

// Path is where the config file is read from.
func Path(home string) string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "lazyprojects", "config")
}

// fromFile reads `key = value` lines. Values may be quoted; # starts a comment.
func fromFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

func expand(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}
