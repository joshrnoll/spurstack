package gitutil

import (
	"context"
	"fmt"
	"net/url"
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
	GitHubToken string
}

func (g Git) PrepareWorktree(ctx context.Context, repoFullName, cloneURL, defaultBranch string, issueNumber int) (string, string, error) {
	repoID := sanitize(repoFullName)
	cacheDir := filepath.Join(g.Workspace, "repos", repoID)
	wtDir := filepath.Join(g.Workspace, "worktrees", repoID, fmt.Sprintf("issue-%d", issueNumber))
	branch := fmt.Sprintf("agent/issue-%d", issueNumber)
	plainCloneURL := plainRemoteURL(cloneURL)
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); os.IsNotExist(err) {
		if _, err := g.runAuthedGit(ctx, "", "clone", plainCloneURL, cacheDir); err != nil {
			return "", "", err
		}
		if err := ensurePlainOriginURL(ctx, cacheDir); err != nil {
			return "", "", err
		}
	} else {
		if err := ensurePlainOriginURL(ctx, cacheDir); err != nil {
			return "", "", err
		}
		if _, err := g.runAuthedGit(ctx, cacheDir, "fetch", "origin", "--prune"); err != nil {
			return "", "", err
		}
		if err := ensurePlainOriginURL(ctx, cacheDir); err != nil {
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
	if err := ensurePlainOriginURL(ctx, dir); err != nil {
		return err
	}
	_, err := g.runAuthedGit(ctx, dir, "push", "-u", "origin", branch, "--force-with-lease")
	_ = ensurePlainOriginURL(ctx, dir)
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

func (g Git) runAuthedGit(ctx context.Context, dir string, args ...string) (string, error) {
	gitArgs := append([]string{"-c", "credential.helper=", "-c", "credential.useHttpPath=true"}, args...)
	if g.GitHubToken == "" {
		return run(ctx, dir, "git", gitArgs...)
	}
	askpassDir, err := os.MkdirTemp("", "spurstack-git-askpass-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(askpassDir)
	askpassPath := filepath.Join(askpassDir, "askpass.sh")
	script := `#!/bin/sh
case "$1" in
  *Username*) printf '%s\n' "$GIT_USERNAME" ;;
  *Password*) printf '%s\n' "$GIT_PASSWORD" ;;
  *) printf '\n' ;;
esac
`
	if err := os.WriteFile(askpassPath, []byte(script), 0o700); err != nil {
		return "", err
	}
	env := []string{
		"GIT_ASKPASS=" + askpassPath,
		"GIT_USERNAME=x-access-token",
		"GIT_PASSWORD=" + g.GitHubToken,
	}
	return runWithEnv(ctx, dir, env, "git", gitArgs...)
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return runWithEnv(ctx, dir, nil, name, args...)
}

func runWithEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_TRACE=0", "GIT_TRACE_PACKET=0", "GIT_TRACE_CURL=0", "GIT_CURL_VERBOSE=0")
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	cleanOut := redactSecrets(string(out))
	if err != nil {
		return cleanOut, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(redactArgs(args), " "), err, cleanOut)
	}
	return cleanOut, nil
}

func ensurePlainOriginURL(ctx context.Context, dir string) error {
	origin, err := run(ctx, dir, "git", "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	plain := plainRemoteURL(strings.TrimSpace(origin))
	if plain == strings.TrimSpace(origin) {
		return nil
	}
	_, err = run(ctx, dir, "git", "remote", "set-url", "origin", plain)
	return err
}

func plainRemoteURL(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.User == nil {
		return remote
	}
	u.User = nil
	return u.String()
}

var tokenURLPattern = regexp.MustCompile(`https://x-access-token:[^@\s]+@`)

func redactSecrets(s string) string {
	return tokenURLPattern.ReplaceAllString(s, "https://x-access-token:REDACTED@")
}

func redactArgs(args []string) []string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		redacted[i] = redactSecrets(arg)
	}
	return redacted
}

var unsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitize(s string) string { return strings.Trim(unsafe.ReplaceAllString(s, "-"), "-") }
