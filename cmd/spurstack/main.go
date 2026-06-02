package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"spurstack/internal/agent"
	"spurstack/internal/config"
	"spurstack/internal/github"
	"spurstack/internal/gitutil"
	"spurstack/internal/llm"
)

const (
	runningLabel  = "agent-running"
	prOpenedLabel = "agent-pr-opened"
	failedLabel   = "agent-failed"
)

type jobPayload struct {
	RunID      string            `json:"run_id"`
	Issue      github.Issue      `json:"issue"`
	Repository github.Repository `json:"repository"`
}

type manager struct {
	workspace string
	label     string
	gh        *github.Client
	mu        sync.Mutex
	running   map[string]struct{}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.WorkspaceDir, 0o755); err != nil {
		slog.Error("workspace setup failed", "error", err)
		os.Exit(1)
	}
	if len(os.Args) == 3 && os.Args[1] == "run-job" {
		if err := runJob(cfg, os.Args[2]); err != nil {
			slog.Error("agent run failed", "error", err)
			os.Exit(1)
		}
		return
	}

	m := &manager{workspace: cfg.WorkspaceDir, label: cfg.AgentLabel, gh: github.NewClient(cfg.GitHubToken), running: map[string]struct{}{}}
	slog.Info("agent poller started", "interval", cfg.PollInterval.String(), "repos", cfg.GitHubRepos)
	m.pollLoop(context.Background(), cfg.GitHubRepos, cfg.PollInterval)
}

func (m *manager) pollLoop(ctx context.Context, repos []string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		m.pollOnce(ctx, repos)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *manager) pollOnce(ctx context.Context, repos []string) {
	for _, repoName := range repos {
		repo, err := m.gh.GetRepository(ctx, repoName)
		if err != nil {
			slog.Error("failed to fetch repository", "repo", repoName, "error", err)
			continue
		}
		issues, err := m.gh.ListIssues(ctx, repo.FullName, m.label)
		if err != nil {
			slog.Error("failed to list issues", "repo", repo.FullName, "label", m.label, "error", err)
			continue
		}
		for _, issue := range issues {
			if hasAnyLabel(issue, runningLabel, prOpenedLabel) {
				slog.Warn("skipping issue in non-ready agent state", "repo", repo.FullName, "issue", issue.Number, "labels", labelNames(issue))
				continue
			}
			m.start(jobPayload{Issue: issue, Repository: repo})
		}
	}
}

func (m *manager) start(job jobPayload) bool {
	if job.RunID == "" {
		job.RunID = newRunID()
	}
	key := job.Repository.FullName + "#" + strconv.Itoa(job.Issue.Number)
	m.mu.Lock()
	if _, ok := m.running[key]; ok {
		m.mu.Unlock()
		return false
	}
	m.running[key] = struct{}{}
	m.mu.Unlock()

	ctx := context.Background()
	if err := m.gh.AddLabels(ctx, job.Repository.FullName, job.Issue.Number, runningLabel); err != nil {
		slog.Error("failed to add running label", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "error", err)
		m.clear(key)
		return false
	}
	if err := m.gh.RemoveLabel(ctx, job.Repository.FullName, job.Issue.Number, m.label); err != nil {
		slog.Warn("failed to remove ready label", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "error", err)
	}
	// A failed issue can be retried by adding agent-ready again. Once claimed,
	// clear the failure state so the issue has exactly one active agent state.
	if hasAnyLabel(job.Issue, failedLabel) {
		if err := m.gh.RemoveLabel(ctx, job.Repository.FullName, job.Issue.Number, failedLabel); err != nil {
			slog.Warn("failed to remove failed label", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "error", err)
		}
	}

	jobPath, err := m.writeJob(job)
	if err != nil {
		slog.Error("failed to write job", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "error", err)
		m.clear(key)
		return false
	}
	cmd := exec.Command(os.Args[0], "run-job", jobPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start agent process", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "error", err)
		m.clear(key)
		return false
	}
	slog.Info("started agent process", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "pid", cmd.Process.Pid)
	go func() {
		err := cmd.Wait()
		m.clear(key)
		if err != nil {
			slog.Error("agent process exited with error", "repo", job.Repository.FullName, "issue", job.Issue.Number, "run_id", job.RunID, "error", err)
		}
	}()
	return true
}

func (m *manager) clear(key string) {
	m.mu.Lock()
	delete(m.running, key)
	m.mu.Unlock()
}

func hasAnyLabel(issue github.Issue, labels ...string) bool {
	wanted := map[string]struct{}{}
	for _, label := range labels {
		wanted[label] = struct{}{}
	}
	for _, label := range issue.Labels {
		if _, ok := wanted[label.Name]; ok {
			return true
		}
	}
	return false
}

func labelNames(issue github.Issue) []string {
	names := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		names = append(names, label.Name)
	}
	return names
}

func (m *manager) writeJob(job jobPayload) (string, error) {
	dir := filepath.Join(m.workspace, "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, job.Repository.Owner.Login+"-"+job.Repository.Name+"-"+strconv.Itoa(job.Issue.Number)+"-"+job.RunID+".json")
	b, err := json.Marshal(job)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o600)
}

func newRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "run-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "run-" + hex.EncodeToString(b)
}

func runJob(cfg config.Config, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var job jobPayload
	if err := json.Unmarshal(b, &job); err != nil {
		return err
	}
	gh := github.NewClient(cfg.GitHubToken)
	var wrangler *llm.Client
	if cfg.WranglerModel != "" {
		wrangler = llm.NewWithProvider(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.WranglerModel, cfg.OpenRouterProviderOrder, cfg.OpenRouterAllowFallbacks)
	}
	r := &agent.Runner{
		GitHub:            gh,
		Git:               gitutil.Git{Workspace: cfg.WorkspaceDir, AuthorName: cfg.GitAuthorName, AuthorEmail: cfg.GitAuthorEmail},
		LLM:               llm.NewWithProvider(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.SpurModel, cfg.OpenRouterProviderOrder, cfg.OpenRouterAllowFallbacks),
		Wrangler:          wrangler,
		WranglerModel:     cfg.WranglerModel,
		MaxSteps:          cfg.MaxAgentSteps,
		MaxWranglerCycles: cfg.MaxWranglerCycles,
	}
	if job.RunID == "" {
		job.RunID = newRunID()
	}
	err = r.Run(context.Background(), agent.Job{Issue: job.Issue, Repo: job.Repository, RunID: job.RunID})
	_ = gh.RemoveLabel(context.Background(), job.Repository.FullName, job.Issue.Number, runningLabel)
	if err != nil {
		_ = gh.AddLabels(context.Background(), job.Repository.FullName, job.Issue.Number, failedLabel)
		return err
	}
	_ = gh.AddLabels(context.Background(), job.Repository.FullName, job.Issue.Number, prOpenedLabel)
	return nil
}
