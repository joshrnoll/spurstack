package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GitHubToken              string
	GitHubRepos              []string
	OpenAIAPIKey             string
	OpenAIBaseURL            string
	SpurModel                string
	WranglerModel            string
	OpenRouterProviderOrder  []string
	OpenRouterAllowFallbacks bool
	WorkspaceDir             string
	AgentLabel               string
	MaxAgentSteps            int
	MaxWranglerCycles        int
	GitAuthorName            string
	GitAuthorEmail           string
	PollInterval             time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		GitHubToken:              os.Getenv("GITHUB_TOKEN"),
		GitHubRepos:              splitCSV(os.Getenv("GITHUB_REPOSITORIES")),
		OpenAIAPIKey:             os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:            getenv("OPENAI_BASE_URL", "https://openrouter.ai/api/v1"),
		SpurModel:                getenv("SPUR_MODEL", "anthropic/claude-sonnet-4.6"),
		WranglerModel:            os.Getenv("WRANGLER_MODEL"),
		OpenRouterProviderOrder:  splitCSV(os.Getenv("OPENROUTER_PROVIDER_ORDER")),
		OpenRouterAllowFallbacks: getenvBool("OPENROUTER_ALLOW_FALLBACKS", true),
		WorkspaceDir:             getenv("WORKSPACE_DIR", "/var/lib/spurstack/workspace"),
		AgentLabel:               getenv("SPUR_LABEL", "agent-ready"),
		MaxAgentSteps:            getenvInt("MAX_SPUR_STEPS", 30),
		MaxWranglerCycles:        getenvIntMin("MAX_WRANGLER_CYCLES", 3, 1),
		GitAuthorName:            getenv("GIT_AUTHOR_NAME", "Spurstack"),
		GitAuthorEmail:           getenv("GIT_AUTHOR_EMAIL", "spurstack@example.local"),
		PollInterval:             getenvDuration("POLL_INTERVAL", 60*time.Second),
	}
	if cfg.GitHubToken == "" {
		return cfg, fmt.Errorf("GITHUB_TOKEN is required")
	}
	if len(cfg.GitHubRepos) == 0 {
		return cfg, fmt.Errorf("GITHUB_REPOSITORIES is required, e.g. owner/repo,owner/other-repo")
	}
	if cfg.OpenAIAPIKey == "" {
		return cfg, fmt.Errorf("OPENAI_API_KEY is required")
	}
	return cfg, nil
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getenvBool(name string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func getenvInt(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getenvIntMin(name string, fallback, min int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	if n < min {
		return min
	}
	return n
}

func getenvDuration(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
