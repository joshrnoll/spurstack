package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"spurstack/internal/gitutil"
)

func TestAppendClosingReference(t *testing.T) {
	got := appendClosingReference("Summary\n", 12)
	want := "Summary\n\n## Closes #12\n"
	if got != want {
		t.Fatalf("unexpected body\nwant: %q\n got: %q", want, got)
	}
}

func TestAppendWranglerCommentsSkippedWhenNotRun(t *testing.T) {
	body := "## Summary\nChanged things."
	got := appendWranglerComments(body, wranglerOutcome{})
	if got != body {
		t.Fatalf("expected body unchanged, got %q", got)
	}
}

func TestAppendWranglerCommentsPassed(t *testing.T) {
	got := appendWranglerComments("Summary", wranglerOutcome{Ran: true, Passed: true, Cycles: 2, Model: "test-model", Comments: "Looks good."})
	want := "Summary\n\n## Wrangler Comments (test-model)\n\nWrangler passed after 2 wrangler cycle(s).\n\nLooks good."
	if got != want {
		t.Fatalf("unexpected body\nwant: %q\n got: %q", want, got)
	}
}

func TestAppendWranglerCommentsMaxCycles(t *testing.T) {
	got := appendWranglerComments("Summary", wranglerOutcome{Ran: true, Cycles: 3, MaxCyclesHit: true, Model: "test-model", Comments: "Fix this."})
	want := "Summary\n\n## Wrangler Comments (test-model)\n\n**NOTE: Max Wrangler Cycles Exceeded. Concerns Listed Below**\n\nFix this."
	if got != want {
		t.Fatalf("unexpected body\nwant: %q\n got: %q", want, got)
	}
}

func TestParseWranglerResult(t *testing.T) {
	got, err := parseWranglerResult("```json\n{\"status\":\"pass\",\"summary\":\"ok\",\"comments\":\"ship it\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pass" || got.Summary != "ok" || got.Comments != "ship it" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestParseWranglerResultRejectsUnknownStatus(t *testing.T) {
	_, err := parseWranglerResult(`{"status":"maybe","summary":"hmm"}`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApplyActionDeniesProtectedWrite(t *testing.T) {
	wt := t.TempDir()
	_, _, err := applyAction(context.Background(), wt, gitutil.Git{}, action{Action: "write", Path: ".github/workflows/ci.yml", Content: "name: ci"})
	if err == nil {
		t.Fatal("expected protected path write to fail")
	}
	if !strings.Contains(err.Error(), "protected path") {
		t.Fatalf("expected protected path error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(wt, ".github", "workflows", "ci.yml")); !os.IsNotExist(statErr) {
		t.Fatalf("protected file should not be written, stat err: %v", statErr)
	}
}

func TestApplyActionDeniesProtectedEdit(t *testing.T) {
	wt := t.TempDir()
	path := filepath.Join(wt, "Dockerfile")
	if err := os.WriteFile(path, []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := applyAction(context.Background(), wt, gitutil.Git{}, action{Action: "edit", Path: "Dockerfile", OldText: "alpine", NewText: "debian"})
	if err == nil {
		t.Fatal("expected protected path edit to fail")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "FROM alpine\n" {
		t.Fatalf("protected file changed: %q", got)
	}
}

func TestApplyActionAllowsNormalWriteAndEdit(t *testing.T) {
	wt := t.TempDir()
	ctx := context.Background()
	if _, _, err := applyAction(ctx, wt, gitutil.Git{}, action{Action: "write", Path: "docs/guide.md", Content: "hello"}); err != nil {
		t.Fatalf("normal write failed: %v", err)
	}
	if _, _, err := applyAction(ctx, wt, gitutil.Git{}, action{Action: "edit", Path: "docs/guide.md", OldText: "hello", NewText: "hello world"}); err != nil {
		t.Fatalf("normal edit failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(wt, "docs", "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("unexpected file content %q", got)
	}
}

func TestWritablePathNormalizesBeforeProtectionCheck(t *testing.T) {
	wt := t.TempDir()
	_, err := writablePath(wt, "docs/../.github/workflows/ci.yml")
	if err == nil {
		t.Fatal("expected normalized protected path to be denied")
	}
	if !strings.Contains(err.Error(), ".github/workflows/ci.yml") {
		t.Fatalf("expected normalized protected path in error, got %v", err)
	}
}

func TestApplyActionReadAllowsProtectedPath(t *testing.T) {
	wt := t.TempDir()
	path := filepath.Join(wt, "go.mod")
	if err := os.WriteFile(path, []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	obs, _, err := applyAction(context.Background(), wt, gitutil.Git{}, action{Action: "read", Path: "go.mod"})
	if err != nil {
		t.Fatalf("protected read failed: %v", err)
	}
	if obs != "module test\n" {
		t.Fatalf("unexpected read content %q", obs)
	}
}

func TestEnsureNoProtectedChangesBlocksCommitSurface(t *testing.T) {
	wt := t.TempDir()
	runGit(t, wt, "init")
	runGit(t, wt, "config", "user.name", "Test")
	runGit(t, wt, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "add", "README.md")
	runGit(t, wt, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(wt, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureNoProtectedChanges(context.Background(), wt, ""); err == nil {
		t.Fatal("expected protected package manifest change to be blocked")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
