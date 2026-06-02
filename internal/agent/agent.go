package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"spurstack/internal/github"
	"spurstack/internal/gitutil"
	"spurstack/internal/llm"
)

type Runner struct {
	GitHub            *github.Client
	Git               gitutil.Git
	LLM               *llm.Client
	Wrangler          *llm.Client
	WranglerModel     string
	MaxSteps          int
	MaxWranglerCycles int
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

type wranglerResult struct {
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Comments string `json:"comments"`
}

type wranglerOutcome struct {
	Ran          bool
	Passed       bool
	Cycles       int
	MaxCyclesHit bool
	Model        string
	Comments     string
}

func (r *Runner) Run(ctx context.Context, job Job) error {
	log := slog.With("repo", job.Repo.FullName, "issue", job.Issue.Number, "run_id", job.RunID)
	log.Info("starting issue agent")
	wt, branch, err := r.Git.PrepareWorktree(ctx, job.Repo.FullName, job.Repo.CloneURL, job.Repo.DefaultBranch, job.Issue.Number)
	if err != nil {
		return err
	}
	plan, err := r.plan(ctx, job, wt)
	if err != nil {
		return err
	}
	log.Info("implementation plan created", "plan", plan)

	messages := spurMessages(job, wt, plan)
	result, outcome, err := r.executeWithWrangler(ctx, job, wt, plan, messages)
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
	prBody = appendClosingReference(prBody, job.Issue.Number)
	prBody = appendWranglerComments(prBody, outcome)
	prBody = appendRunID(prBody, job.RunID)
	pr, err := r.GitHub.CreatePullRequest(ctx, job.Repo.FullName, github.CreatePREquest{Title: prTitle, Head: branch, Base: job.Repo.DefaultBranch, Body: prBody, Draft: false})
	if err != nil {
		if handled, handleErr := r.handleExistingPullRequest(ctx, job, branch, err); handled || handleErr != nil {
			if handleErr != nil {
				return handleErr
			}
			cleanupWorktree(ctx, log, r, job)
			return nil
		}
		return err
	}
	log.Info("pull request opened", "url", pr.HTMLURL)
	cleanupWorktree(ctx, log, r, job)
	return nil
}

func (r *Runner) handleExistingPullRequest(ctx context.Context, job Job, branch string, err error) (bool, error) {
	var ghErr github.Error
	if !errors.As(err, &ghErr) || ghErr.StatusCode != http.StatusUnprocessableEntity {
		return false, nil
	}
	head := job.Repo.Owner.Login + ":" + branch
	prs, listErr := r.GitHub.ListPullRequests(ctx, job.Repo.FullName, head, job.Repo.DefaultBranch)
	if listErr != nil {
		return true, listErr
	}
	if len(prs) == 0 {
		return false, nil
	}
	body := fmt.Sprintf("PR already open for this issue: %s", prs[0].HTMLURL)
	if _, commentErr := r.GitHub.CreateIssueComment(ctx, job.Repo.FullName, job.Issue.Number, body); commentErr != nil {
		return true, commentErr
	}
	return true, nil
}

func cleanupWorktree(ctx context.Context, log *slog.Logger, r *Runner, job Job) {
	if err := r.Git.RemoveWorktree(ctx, job.Repo.FullName, job.Issue.Number); err != nil {
		log.Warn("failed to clean up worktree", "error", err)
	} else {
		log.Info("cleaned up worktree")
	}
}

func appendClosingReference(body string, issueNumber int) string {
	return strings.TrimRight(body, "\n") + fmt.Sprintf("\n\n## Closes #%d\n", issueNumber)
}

func appendRunID(body, runID string) string {
	if strings.TrimSpace(runID) == "" {
		return body
	}
	return strings.TrimRight(body, "\n") + fmt.Sprintf("\n\n---\nSpur run ID: `%s`\n", runID)
}

func appendWranglerComments(body string, outcome wranglerOutcome) string {
	if !outcome.Ran {
		return body
	}
	comments := strings.TrimSpace(outcome.Comments)
	if comments == "" {
		comments = "No wrangler comments provided."
	}
	section := fmt.Sprintf("## Wrangler Comments (%s)\n\n", outcome.Model)
	if outcome.MaxCyclesHit && !outcome.Passed {
		section += "**NOTE: Max Wrangler Cycles Exceeded. Concerns Listed Below**\n\n"
	} else if outcome.Passed {
		section += fmt.Sprintf("Wrangler passed after %d wrangler cycle(s).\n\n", outcome.Cycles)
	}
	section += comments
	return strings.TrimRight(body, "\n") + "\n\n" + section
}

func (r *Runner) plan(ctx context.Context, job Job, wt string) (string, error) {
	tree, _ := repoTree(wt, 200)
	prompt := fmt.Sprintf("Issue #%d: %s\nURL: %s\n\n%s\n\nRepository tree:\n%s\n\nCreate a concise implementation plan. You may group larger work into multiple commits during implementation, but do not include push, PR, label, or issue-closing steps; the app handles those after implementation. Do not write code yet.", job.Issue.Number, job.Issue.Title, job.Issue.HTMLURL, job.Issue.Body, tree)
	return r.LLM.Chat(ctx, []llm.Message{{Role: "system", Content: "You are a senior software engineer planning a small GitHub issue implementation."}, {Role: "user", Content: prompt}})
}

func spurMessages(job Job, wt, plan string) []llm.Message {
	system := `You are an autonomous coding agent in a git worktree. Respond ONLY as JSON.
Available actions:
{"action":"read","path":"relative/file"}
{"action":"write","path":"relative/file","content":"full new file content"}
{"action":"edit","path":"relative/file","old_text":"exact text to replace","new_text":"replacement text"}
{"action":"commit","commit_message":"conventional commit <=72 chars"}
{"action":"finish","commit_message":"conventional commit <=72 chars","pr_title":"title","pr_body":"summary and testing notes"}
Rules: inspect files before editing, keep changes minimal, use edit for existing files, use write only for new files or complete rewrites, and do not run shell commands. Use commit to save coherent groups of completed changes during larger work. The app handles final commit of any remaining changes, push, and pull request creation after finish.`
	return []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: fmt.Sprintf("Implement issue #%d: %s\n\nIssue body:\n%s\n\nPlan:\n%s\n\nInitial tree:\n%s", job.Issue.Number, job.Issue.Title, job.Issue.Body, plan, mustTree(wt))}}
}

func (r *Runner) executeWithWrangler(ctx context.Context, job Job, wt, plan string, messages []llm.Message) (action, wranglerOutcome, error) {
	maxCycles := r.MaxWranglerCycles
	if maxCycles < 1 {
		maxCycles = 1
	}
	for cycle := 1; ; cycle++ {
		result, updatedMessages, err := r.execute(ctx, job, wt, messages)
		if err != nil {
			return action{}, wranglerOutcome{}, err
		}
		messages = updatedMessages
		if r.Wrangler == nil || strings.TrimSpace(r.WranglerModel) == "" {
			return result, wranglerOutcome{}, nil
		}
		review, err := r.review(ctx, job, wt, plan, result)
		if err != nil {
			return action{}, wranglerOutcome{}, err
		}
		outcome := wranglerOutcome{Ran: true, Passed: review.Status == "pass", Cycles: cycle, Model: r.WranglerModel, Comments: reviewComments(review)}
		if review.Status == "pass" {
			return result, outcome, nil
		}
		if cycle >= maxCycles {
			outcome.MaxCyclesHit = true
			return result, outcome, nil
		}
		messages = append(messages, llm.Message{Role: "user", Content: fmt.Sprintf("The wrangler model found issues. Address the feedback below using read/write/edit/commit, then call finish again.\n\nWrangler comments:\n%s", outcome.Comments)})
	}
}

func reviewComments(review wranglerResult) string {
	comments := strings.TrimSpace(review.Comments)
	summary := strings.TrimSpace(review.Summary)
	if comments == "" {
		return summary
	}
	if summary == "" || strings.Contains(comments, summary) {
		return comments
	}
	return summary + "\n\n" + comments
}

func (r *Runner) execute(ctx context.Context, job Job, wt string, messages []llm.Message) (action, []llm.Message, error) {
	for step := 0; step < r.MaxSteps; step++ {
		resp, err := r.LLM.Chat(ctx, messages)
		if err != nil {
			return action{}, messages, err
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
				return action{}, messages, err
			}
			changed, err := hasBranchFileChanges(ctx, wt, job.Repo.DefaultBranch)
			if err != nil {
				return action{}, messages, err
			}
			if dirty || changed {
				return act, messages, nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: "Cannot finish yet: there are no file changes or commits on the branch. Use write/edit to implement the issue, optionally commit, then finish."})
			continue
		}
		messages = append(messages, llm.Message{Role: "user", Content: truncate(obs, 12000)})
	}
	return action{}, messages, fmt.Errorf("max agent steps reached")
}

func (r *Runner) review(ctx context.Context, job Job, wt, plan string, result action) (wranglerResult, error) {
	diff, err := fullDiff(ctx, wt, job.Repo.DefaultBranch)
	if err != nil {
		return wranglerResult{}, err
	}
	log, _ := commitLog(ctx, wt, job.Repo.DefaultBranch)
	prompt := fmt.Sprintf(`Review this Spurstack implementation. Return ONLY JSON matching {"status":"pass|fail","summary":"...","comments":"..."}.

Pass only if the changes are ready for a human PR review. Fail when concrete issues should be sent back to the spur for another implementation pass. Be concise and actionable.

Repository: %s
Issue #%d: %s
URL: %s

Issue body:
%s

Plan:
%s

Spur-proposed commit message: %s
Spur-proposed PR title: %s
Spur-proposed PR body:
%s

Repository tree:
%s

Branch commit log:
%s

Diff against base branch, including uncommitted changes:
%s`, job.Repo.FullName, job.Issue.Number, job.Issue.Title, job.Issue.HTMLURL, job.Issue.Body, plan, result.CommitMessage, result.PRTitle, result.PRBody, mustTree(wt), log, diff)
	resp, err := r.Wrangler.Chat(ctx, []llm.Message{{Role: "system", Content: "You are a senior code wrangler. You inspect changes and produce concise, actionable review feedback."}, {Role: "user", Content: prompt}})
	if err != nil {
		return wranglerResult{}, err
	}
	return parseWranglerResult(resp)
}

func parseWranglerResult(s string) (wranglerResult, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	var r wranglerResult
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return r, err
	}
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))
	if r.Status != "pass" && r.Status != "fail" {
		return r, fmt.Errorf("unknown wrangler status %q", r.Status)
	}
	if strings.TrimSpace(r.Comments) == "" && strings.TrimSpace(r.Summary) == "" {
		r.Comments = "Wrangler did not provide comments."
	}
	return r, nil
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

func fullDiff(ctx context.Context, wt, defaultBranch string) (string, error) {
	base := "origin/" + defaultBranch
	committed, err := gitutil.Run(ctx, wt, "git", "diff", base+"...HEAD")
	if err != nil {
		return "", err
	}
	staged, err := gitutil.Run(ctx, wt, "git", "diff", "--cached")
	if err != nil {
		return "", err
	}
	unstaged, err := gitutil.Run(ctx, wt, "git", "diff")
	if err != nil {
		return "", err
	}
	untracked, err := untrackedFilesForReview(ctx, wt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.Join([]string{committed, staged, unstaged, untracked}, "\n")), nil
}

func untrackedFilesForReview(ctx context.Context, wt string) (string, error) {
	out, err := gitutil.Run(ctx, wt, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	var sections []string
	for _, rel := range strings.Split(out, "\n") {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		p, err := safePath(wt, rel)
		if err != nil {
			return "", err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		sections = append(sections, fmt.Sprintf("Untracked file: %s\n%s", rel, truncate(string(b), 20000)))
	}
	return strings.Join(sections, "\n\n"), nil
}

func commitLog(ctx context.Context, wt, defaultBranch string) (string, error) {
	base := "origin/" + defaultBranch
	out, err := gitutil.Run(ctx, wt, "git", "log", "--oneline", base+"..HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "No branch commits yet.", nil
	}
	return out, nil
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
