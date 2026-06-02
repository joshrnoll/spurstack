package gitutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Git struct {
	Workspace   string
	AuthorName  string
	AuthorEmail string
}

func (g Git) PrepareWorktree(ctx context.Context, repoFullName, cloneURL, defaultBranch string, issueNumber int) (string, string, error) {
	repoID := sanitize(repoFullName)
	cacheDir := filepath.Join(g.Workspace, "repos", repoID)
	wtDir := filepath.Join(g.Workspace, "worktrees", repoID, fmt.Sprintf("issue-%d", issueNumber))
	branch := fmt.Sprintf("agent/issue-%d", issueNumber)
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); os.IsNotExist(err) {
		if _, err := run(ctx, "", "git", "clone", cloneURL, cacheDir); err != nil {
			return "", "", err
		}
	} else {
		if _, err := run(ctx, cacheDir, "git", "fetch", "origin", "--prune"); err != nil {
			return "", "", err
		}
	}
	_, _ = run(ctx, cacheDir, "git", "worktree", "remove", "--force", wtDir)
	base := "origin/" + defaultBranch
	if _, err := run(ctx, cacheDir, "git", "worktree", "add", "-B", branch, wtDir, base); err != nil {
		return "", "", err
	}
	if _, err := run(ctx, wtDir, "git", "config", "user.name", g.AuthorName); err != nil {
		return "", "", err
	}
	if _, err := run(ctx, wtDir, "git", "config", "user.email", g.AuthorEmail); err != nil {
		return "", "", err
	}
	return wtDir, branch, nil
}

func (g Git) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	if _, err := run(ctx, dir, "git", "add", "-A"); err != nil {
		return false, err
	}
	status, err := run(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	_, err = run(ctx, dir, "git", "commit", "-m", message)
	return err == nil, err
}

func (g Git) Push(ctx context.Context, dir, branch string) error {
	_, err := run(ctx, dir, "git", "push", "-u", "origin", branch, "--force-with-lease")
	return err
}

func (g Git) RemoveWorktree(ctx context.Context, repoFullName string, issueNumber int) error {
	repoID := sanitize(repoFullName)
	cacheDir := filepath.Join(g.Workspace, "repos", repoID)
	wtDir := filepath.Join(g.Workspace, "worktrees", repoID, fmt.Sprintf("issue-%d", issueNumber))
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		return nil
	}
	if _, err := run(ctx, cacheDir, "git", "worktree", "remove", "--force", wtDir); err != nil {
		return err
	}
	_, _ = run(ctx, cacheDir, "git", "worktree", "prune")
	return nil
}

func Run(ctx context.Context, dir string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command provided")
	}
	return run(ctx, dir, args[0], args[1:]...)
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

var unsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitize(s string) string { return strings.Trim(unsafe.ReplaceAllString(s, "-"), "-") }
