package github

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeReposAcrossPages(t *testing.T) {
	// Two documents back to back, as `gh api graphql --paginate` prints them.
	out := []byte(`{"data":{"viewer":{"repositories":{"nodes":[
		{"nameWithOwner":"Kompell/kompose","description":"d","pushedAt":"2026-09-29T10:00:00Z","isPrivate":true,"diskUsage":120,"primaryLanguage":{"name":"TypeScript"}},
		{"nameWithOwner":"HenriAnttila/empty","description":null,"pushedAt":null,"primaryLanguage":null}
	]}}}}{"data":{"viewer":{"repositories":{"nodes":[
		{"nameWithOwner":"ikiuscompany/x","pushedAt":"2025-01-01T00:00:00Z","isArchived":true}
	]}}}}`)
	repos, err := decodeRepos(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 3 {
		t.Fatalf("got %d repos, want 3", len(repos))
	}
	if r := repos[0]; r.Owner() != "Kompell" || r.Name() != "kompose" || r.Language != "TypeScript" || !r.IsPrivate || r.PushedAt.IsZero() {
		t.Errorf("first repo = %+v", r)
	}
	if r := repos[1]; r.Language != "" || !r.PushedAt.IsZero() || r.Description != "" {
		t.Errorf("repo with nulls = %+v", r)
	}
	if !repos[2].IsArchived {
		t.Errorf("third repo = %+v", repos[2])
	}
}

func TestPRs(t *testing.T) {
	var gotArgs []string
	c := Client{Run: func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(`[{"number":12,"title":"Fix it","author":{"login":"henri"},"headRefName":"fix","baseRefName":"main","isDraft":true,"updatedAt":"2026-09-30T08:00:00Z","body":"b","url":"https://github.com/o/n/pull/12"}]`), nil
	}}
	prs, err := c.PRs(context.Background(), "o/n")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 12 || prs[0].Author != "henri" || prs[0].Head != "fix" || !prs[0].IsDraft {
		t.Fatalf("prs = %+v", prs)
	}
	if joined := strings.Join(gotArgs, " "); !strings.Contains(joined, "-R o/n") {
		t.Errorf("gh args = %q, want the repo passed with -R", joined)
	}
}

func TestReadmeMissingIsNotAnError(t *testing.T) {
	c := Client{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}}
	if got, err := c.Readme(context.Background(), "o/n"); err != nil || got != "" {
		t.Fatalf("Readme = %q, %v", got, err)
	}
	c.Run = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("HTTP 502") }
	if _, err := c.Readme(context.Background(), "o/n"); err == nil {
		t.Fatal("a 502 should be reported")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "repos.json")
	if got := LoadCache(path); got != nil {
		t.Fatalf("missing cache = %v", got)
	}
	want := []Repo{{NameWithOwner: "a/b", Language: "Go"}}
	if err := SaveCache(path, want); err != nil {
		t.Fatal(err)
	}
	if got := LoadCache(path); !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadCache = %+v, want %+v", got, want)
	}
}
