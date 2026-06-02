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
