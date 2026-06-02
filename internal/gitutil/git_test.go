package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareWorktreePersistsPlainRemoteURL(t *testing.T) {
	ctx := context.Background()
	remote := newBareRemote(t)
	workspace := t.TempDir()

	git := Git{Workspace: workspace, AuthorName: "Spurstack", AuthorEmail: "spurstack@example.local", GitHubToken: "secret-token"}
	wt, _, err := git.PrepareWorktree(ctx, "owner/repo", remote, "main", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
	origin, err := Run(ctx, filepath.Join(workspace, "repos", "owner-repo"), "git", "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(origin) != remote {
		t.Fatalf("origin URL = %q, want %q", strings.TrimSpace(origin), remote)
	}
	if strings.Contains(origin, "secret-token") || strings.Contains(origin, "x-access-token") {
		t.Fatalf("origin URL contains credential material: %q", origin)
	}
}

func TestRunRedactsTokenizedGitErrors(t *testing.T) {
	_, err := Run(context.Background(), "", "git", "-c", "remote.origin.url=https://x-access-token:super-secret-token@github.com/owner/repo.git", "not-a-command")
	if err == nil {
		t.Fatal("expected git command to fail")
	}
	msg := err.Error()
	if strings.Contains(msg, "super-secret-token") {
		t.Fatalf("error leaked token: %s", msg)
	}
	if !strings.Contains(msg, "https://x-access-token:REDACTED@github.com/owner/repo.git") {
		t.Fatalf("error did not include redacted URL: %s", msg)
	}
}

func TestEnsurePlainOriginURLCleansExistingTokenizedRemote(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mustGit(t, ctx, dir, "init")
	mustGit(t, ctx, dir, "remote", "add", "origin", "https://x-access-token:old-token@github.com/owner/repo.git")

	if err := ensurePlainOriginURL(ctx, dir); err != nil {
		t.Fatal(err)
	}
	origin, err := Run(ctx, dir, "git", "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(origin), "https://github.com/owner/repo.git"; got != want {
		t.Fatalf("origin URL = %q, want %q", got, want)
	}
	config, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "old-token") || strings.Contains(string(config), "x-access-token") {
		t.Fatalf("git config still contains tokenized remote: %s", string(config))
	}
}

func newBareRemote(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	remote := filepath.Join(root, "remote.git")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, ctx, src, "init", "-b", "main")
	mustGit(t, ctx, src, "config", "user.name", "Test")
	mustGit(t, ctx, src, "config", "user.email", "test@example.local")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, ctx, src, "add", "README.md")
	mustGit(t, ctx, src, "commit", "-m", "init")
	mustGit(t, ctx, "", "clone", "--bare", src, remote)
	return remote
}

func mustGit(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	if _, err := Run(ctx, dir, append([]string{"git"}, args...)...); err != nil {
		t.Fatal(err)
	}
}
