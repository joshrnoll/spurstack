package config

import "testing"

func TestLoadUsesSpurAndWranglerModels(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORIES", "owner/repo")
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("SPUR_MODEL", "spur-model")
	t.Setenv("WRANGLER_MODEL", "wrangler-model")
	t.Setenv("MAX_WRANGLER_CYCLES", "2")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SpurModel != "spur-model" {
		t.Fatalf("SpurModel = %q", cfg.SpurModel)
	}
	if cfg.WranglerModel != "wrangler-model" {
		t.Fatalf("WranglerModel = %q", cfg.WranglerModel)
	}
	if cfg.MaxWranglerCycles != 2 {
		t.Fatalf("MaxWranglerCycles = %d", cfg.MaxWranglerCycles)
	}
}

func TestLoadTrustedAuthorAssociations(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORIES", "owner/repo")
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("TRUSTED_AUTHOR_ASSOCIATIONS", "OWNER, MEMBER")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedAuthorAssociations) != 2 || cfg.TrustedAuthorAssociations[0] != "OWNER" || cfg.TrustedAuthorAssociations[1] != "MEMBER" {
		t.Fatalf("TrustedAuthorAssociations = %#v", cfg.TrustedAuthorAssociations)
	}
}

func TestLoadDoesNotUseOldModelAlias(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORIES", "owner/repo")
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("OPENAI_MODEL", "old-model")
	t.Setenv("SPUR_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SpurModel == "old-model" {
		t.Fatal("OPENAI_MODEL should not be used as an alias")
	}
}

func TestLoadUsesNetworkLimitConfig(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORIES", "owner/repo")
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("GITHUB_TIMEOUT", "5s")
	t.Setenv("GITHUB_MAX_ERROR_BODY_BYTES", "123")
	t.Setenv("LLM_TIMEOUT", "2m")
	t.Setenv("LLM_MAX_RESPONSE_BYTES", "456")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubTimeout.String() != "5s" || cfg.GitHubMaxErrorBodyBytes != 123 {
		t.Fatalf("github limits = %s/%d", cfg.GitHubTimeout, cfg.GitHubMaxErrorBodyBytes)
	}
	if cfg.LLMTimeout.String() != "2m0s" || cfg.LLMMaxResponseBytes != 456 {
		t.Fatalf("llm limits = %s/%d", cfg.LLMTimeout, cfg.LLMMaxResponseBytes)
	}
}

func TestMaxWranglerCyclesMinimum(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORIES", "owner/repo")
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("WRANGLER_MODEL", "wrangler-model")
	t.Setenv("MAX_WRANGLER_CYCLES", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxWranglerCycles != 1 {
		t.Fatalf("MaxWranglerCycles = %d, want 1", cfg.MaxWranglerCycles)
	}
}

func TestLoadProtectedPaths(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORIES", "owner/repo")
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("SPUR_PROTECTED_PATHS", ".github/, deploy/, *.lock")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".github/", "deploy/", "*.lock"}
	if len(cfg.ProtectedPaths) != len(want) {
		t.Fatalf("ProtectedPaths = %#v", cfg.ProtectedPaths)
	}
	for i := range want {
		if cfg.ProtectedPaths[i] != want[i] {
			t.Fatalf("ProtectedPaths = %#v", cfg.ProtectedPaths)
		}
	}
}
