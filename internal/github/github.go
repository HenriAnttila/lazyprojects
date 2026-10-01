// Package github fetches repos, pull requests and READMEs by running the gh
// CLI, which already holds the user's auth and clone protocol. It has no UI.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Repo struct {
	NameWithOwner string    `json:"nameWithOwner"`
	Description   string    `json:"description"`
	PushedAt      time.Time `json:"pushedAt"`
	IsPrivate     bool      `json:"isPrivate"`
	IsFork        bool      `json:"isFork"`
	IsArchived    bool      `json:"isArchived"`
	DiskUsage     int       `json:"diskUsage"` // KB
	Language      string    `json:"language"`
}

func (r Repo) Owner() string { o, _, _ := strings.Cut(r.NameWithOwner, "/"); return o }
func (r Repo) Name() string  { _, n, _ := strings.Cut(r.NameWithOwner, "/"); return n }

type PR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Head      string    `json:"headRefName"`
	Base      string    `json:"baseRefName"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	IsDraft   bool      `json:"isDraft"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PRLimit keeps the list call fast: paging further makes gh walk the API, and
// on a busy repo that is the difference between under a second and several.
const PRLimit = 30

// Client runs gh. Run is swappable so tests need no network.
type Client struct {
	Run func(ctx context.Context, args ...string) ([]byte, error)
}

func New() Client { return Client{Run: runGH} }

func runGH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// reposQuery asks only for fields GitHub returns cheaply. Adding the default
// branch or per-repo PR and issue counts takes 100 repos from about 1.5s to
// 4-8s, and has timed out with a 502.
const reposQuery = `query($endCursor: String) {
  viewer {
    repositories(first: 100, after: $endCursor,
      affiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
      ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
      orderBy: {field: PUSHED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        nameWithOwner description pushedAt isPrivate isFork isArchived diskUsage
        primaryLanguage { name }
      }
    }
  }
}`

// Repos lists every repo the user can access, most recently pushed first.
func (c Client) Repos(ctx context.Context) ([]Repo, error) {
	out, err := c.Run(ctx, "api", "graphql", "--paginate", "-f", "query="+reposQuery)
	if err != nil {
		return nil, err
	}
	return decodeRepos(out)
}

// decodeRepos reads the output of `gh api graphql --paginate`, which is one
// JSON document per page, back to back.
func decodeRepos(out []byte) ([]Repo, error) {
	type page struct {
		Data struct {
			Viewer struct {
				Repositories struct {
					Nodes []struct {
						Repo
						PushedAt        *time.Time `json:"pushedAt"`
						PrimaryLanguage *struct {
							Name string `json:"name"`
						} `json:"primaryLanguage"`
					} `json:"nodes"`
				} `json:"repositories"`
			} `json:"viewer"`
		} `json:"data"`
	}
	var repos []Repo
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p page
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decoding repo list: %w", err)
		}
		for _, n := range p.Data.Viewer.Repositories.Nodes {
			r := n.Repo
			if n.PushedAt != nil { // null for a repo that was never pushed to
				r.PushedAt = *n.PushedAt
			}
			if n.PrimaryLanguage != nil {
				r.Language = n.PrimaryLanguage.Name
			}
			repos = append(repos, r)
		}
	}
	return repos, nil
}

// PRs lists a repo's open pull requests in one call, bodies included, so the
// preview never needs a second round-trip.
func (c Client) PRs(ctx context.Context, slug string) ([]PR, error) {
	out, err := c.Run(ctx, "pr", "list", "-R", slug, "--limit", fmt.Sprint(PRLimit),
		"--json", "number,title,author,headRefName,baseRefName,isDraft,updatedAt,body,url")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		PR
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("decoding PR list: %w", err)
	}
	prs := make([]PR, len(raw))
	for i, r := range raw {
		prs[i] = r.PR
		prs[i].Author = r.Author.Login
	}
	return prs, nil
}

// Readme returns a repo's README as markdown, or "" when it has none.
func (c Client) Readme(ctx context.Context, slug string) (string, error) {
	out, err := c.Run(ctx, "api", "repos/"+slug+"/readme", "-H", "Accept: application/vnd.github.raw")
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

// CachePath is where the repo list is kept between runs so the list paints
// before the network answers.
func CachePath() string {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "lazyprojects", "repos.json")
}

func LoadCache(path string) []Repo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var repos []Repo
	if json.Unmarshal(data, &repos) != nil {
		return nil
	}
	return repos
}

func SaveCache(path string, repos []Repo) error {
	data, err := json.Marshal(repos)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write then rename, so a run that is killed mid-write leaves the old cache.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
