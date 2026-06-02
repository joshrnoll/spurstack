package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"spurstack/internal/github"
	"spurstack/internal/gitutil"
	"spurstack/internal/llm"
)

type Runner struct {
	GitHub   *github.Client
	Git      gitutil.Git
	LLM      *llm.Client
	MaxSteps int
}

type Job struct {
	Issue github.Issue
	Repo  github.Repository
	RunID string
}

type action struct {
	Action        string `json:"action"`
	Path          string `json:"path,omitempty"`
	Content       string `json:"content,omitempty"`
	OldText       string `json:"old_text,omitempty"`
	NewText       string `json:"new_text,omitempty"`
	PRTitle       string `json:"pr_title,omitempty"`
	PRBody        string `json:"pr_body,omitempty"`
	CommitMessage string `json:"commit_message,omitempty"`
}

func (r *Runner) Run(ctx context.Context, job Job) error {
	log := slog.With("repo", job.Repo.FullName, "issue", job.Issue.Number, "run_id", job.RunID)
	log.Info("starting issue agent")
	wt, branch, err := r.Git.PrepareWorktree(ctx, job.Repo.FullName, r.GitHub.AuthedCloneURL(job.Repo.CloneURL), job.Repo.DefaultBranch, job.Issue.Number)
	if err != nil {
		return err
	}
	plan, err := r.plan(ctx, job, wt)
	if err != nil {
		return err
	}
	log.Info("implementation plan created", "plan", plan)
	result, err := r.execute(ctx, job, wt, plan)
	if err != nil {
		return err
	}
	msg := result.CommitMessage
	if msg == "" {
		msg = fmt.Sprintf("fix: address issue #%d", job.Issue.Number)
	}
	dirty, err := hasUncommittedChanges(ctx, wt)
	if err != nil {
		return err
	}
	if dirty {
		if _, err := r.Git.CommitAll(ctx, wt, msg); err != nil {
			return err
		}
	}
	changed, err := hasBranchFileChanges(ctx, wt, job.Repo.DefaultBranch)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("agent finished without file changes")
	}
	if err := r.Git.Push(ctx, wt, branch); err != nil {
		return err
	}
	prTitle := result.PRTitle
	if prTitle == "" {
		prTitle = msg
	}
	prBody := result.PRBody
	if prBody == "" {
		prBody = fmt.Sprintf("Addresses #%d.\n\nPlan:\n%s", job.Issue.Number, plan)
	}
	prBody = appendRunID(prBody, job.RunID)
	pr, err := r.GitHub.CreatePullRequest(ctx, job.Repo.FullName, github.CreatePREquest{Title: prTitle, Head: branch, Base: job.Repo.DefaultBranch, Body: prBody, Draft: false})
	if err != nil {
		return err
	}
	log.Info("pull request opened", "url", pr.HTMLURL)
	if err := r.Git.RemoveWorktree(ctx, job.Repo.FullName, job.Issue.Number); err != nil {
		log.Warn("failed to clean up worktree", "error", err)
	} else {
		log.Info("cleaned up worktree")
	}
	return nil
}

func appendRunID(body, runID string) string {
	if strings.TrimSpace(runID) == "" {
		return body
	}
	return strings.TrimRight(body, "\n") + fmt.Sprintf("\n\n---\nSpur run ID: `%s`\n", runID)
}

func (r *Runner) plan(ctx context.Context, job Job, wt string) (string, error) {
	tree, _ := repoTree(wt, 200)
	prompt := fmt.Sprintf("Issue #%d: %s\nURL: %s\n\n%s\n\nRepository tree:\n%s\n\nCreate a concise implementation plan. You may group larger work into multiple commits during implementation, but do not include push, PR, label, or issue-closing steps; the app handles those after implementation. Do not write code yet.", job.Issue.Number, job.Issue.Title, job.Issue.HTMLURL, job.Issue.Body, tree)
	return r.LLM.Chat(ctx, []llm.Message{{Role: "system", Content: "You are a senior software engineer planning a small GitHub issue implementation."}, {Role: "user", Content: prompt}})
}

func (r *Runner) execute(ctx context.Context, job Job, wt, plan string) (action, error) {
	system := `You are an autonomous coding agent in a git worktree. Respond ONLY as JSON.
Available actions:
{"action":"read","path":"relative/file"}
{"action":"write","path":"relative/file","content":"full new file content"}
{"action":"edit","path":"relative/file","old_text":"exact text to replace","new_text":"replacement text"}
{"action":"commit","commit_message":"conventional commit <=72 chars"}
{"action":"finish","commit_message":"conventional commit <=72 chars","pr_title":"title","pr_body":"summary and testing notes"}
Rules: inspect files before editing, keep changes minimal, use edit for existing files, use write only for new files or complete rewrites, and do not run shell commands. Use commit to save coherent groups of completed changes during larger work. The app handles final commit of any remaining changes, push, and pull request creation after finish.`
	messages := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: fmt.Sprintf("Implement issue #%d: %s\n\nIssue body:\n%s\n\nPlan:\n%s\n\nInitial tree:\n%s", job.Issue.Number, job.Issue.Title, job.Issue.Body, plan, mustTree(wt))}}
	for step := 0; step < r.MaxSteps; step++ {
		resp, err := r.LLM.Chat(ctx, messages)
		if err != nil {
			return action{}, err
		}
		act, err := parseAction(resp)
		if err != nil {
			messages = append(messages, llm.Message{Role: "assistant", Content: resp}, llm.Message{Role: "user", Content: "Invalid response. Return one valid JSON action only."})
			continue
		}
		slog.Info("agent action", "issue", job.Issue.Number, "action", act.Action, "path", act.Path)
		obs, done, err := applyAction(ctx, wt, r.Git, act)
		if err != nil {
			obs = "ERROR: " + err.Error()
		}
		messages = append(messages, llm.Message{Role: "assistant", Content: resp})
		if done {
			dirty, err := hasUncommittedChanges(ctx, wt)
			if err != nil {
				return action{}, err
			}
			changed, err := hasBranchFileChanges(ctx, wt, job.Repo.DefaultBranch)
			if err != nil {
				return action{}, err
			}
			if dirty || changed {
				return act, nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: "Cannot finish yet: there are no file changes or commits on the branch. Use write/edit to implement the issue, optionally commit, then finish."})
			continue
		}
		messages = append(messages, llm.Message{Role: "user", Content: truncate(obs, 12000)})
	}
	return action{}, fmt.Errorf("max agent steps reached")
}

func applyAction(ctx context.Context, wt string, git gitutil.Git, a action) (string, bool, error) {
	switch a.Action {
	case "read":
		p, err := safePath(wt, a.Path)
		if err != nil {
			return "", false, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", false, err
		}
		return string(b), false, nil
	case "write":
		p, err := safePath(wt, a.Path)
		if err != nil {
			return "", false, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", false, err
		}
		return "wrote " + a.Path, false, os.WriteFile(p, []byte(a.Content), 0o644)
	case "edit":
		p, err := safePath(wt, a.Path)
		if err != nil {
			return "", false, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", false, err
		}
		content := string(b)
		if a.OldText == "" {
			return "", false, fmt.Errorf("old_text is required")
		}
		if strings.Count(content, a.OldText) != 1 {
			return "", false, fmt.Errorf("old_text must match exactly once")
		}
		updated := strings.Replace(content, a.OldText, a.NewText, 1)
		return "edited " + a.Path, false, os.WriteFile(p, []byte(updated), 0o644)
	case "commit":
		if strings.TrimSpace(a.CommitMessage) == "" {
			return "", false, fmt.Errorf("commit_message is required")
		}
		committed, err := git.CommitAll(ctx, wt, a.CommitMessage)
		if err != nil {
			return "", false, err
		}
		if !committed {
			return "", false, fmt.Errorf("no file changes to commit")
		}
		return "committed changes", false, nil
	case "finish":
		return "finished", true, nil
	default:
		return "", false, fmt.Errorf("unknown action %q", a.Action)
	}
}

func parseAction(s string) (action, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	var a action
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return a, err
	}
	return a, nil
}

func hasUncommittedChanges(ctx context.Context, wt string) (bool, error) {
	status, err := gitutil.Run(ctx, wt, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) != "", nil
}

func hasBranchFileChanges(ctx context.Context, wt, defaultBranch string) (bool, error) {
	base := "origin/" + defaultBranch
	out, err := gitutil.Run(ctx, wt, "git", "diff", "--name-only", base+"..HEAD")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func safePath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid path")
	}
	p := filepath.Clean(filepath.Join(root, rel))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes worktree")
	}
	return p, nil
}

func mustTree(wt string) string { t, _ := repoTree(wt, 250); return t }
func repoTree(root string, max int) (string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || len(out) >= max {
			return nil
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == "build") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			if rel, e := filepath.Rel(root, p); e == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	return strings.Join(out, "\n"), err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...truncated..."
}
