package agent

import "testing"

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
