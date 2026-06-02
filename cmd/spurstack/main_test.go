package main

import (
	"context"
	"testing"

	"spurstack/internal/github"
)

type fakeGitHub struct {
	repo             github.Repository
	issues           []github.Issue
	comments         []string
	removedLabels    []string
	addedLabels      []string
	ensuredLabelSets int
}

func (f *fakeGitHub) GetRepository(context.Context, string) (github.Repository, error) {
	return f.repo, nil
}

func (f *fakeGitHub) EnsureLabels(context.Context, string, ...github.LabelSpec) error {
	f.ensuredLabelSets++
	return nil
}

func (f *fakeGitHub) EnsureLabel(context.Context, string, github.LabelSpec) error {
	return nil
}

func (f *fakeGitHub) ListIssues(context.Context, string, string) ([]github.Issue, error) {
	return f.issues, nil
}

func (f *fakeGitHub) AddLabels(_ context.Context, _ string, _ int, labels ...string) error {
	f.addedLabels = append(f.addedLabels, labels...)
	return nil
}

func (f *fakeGitHub) RemoveLabel(_ context.Context, _ string, _ int, label string) error {
	f.removedLabels = append(f.removedLabels, label)
	return nil
}

func (f *fakeGitHub) CreateIssueComment(_ context.Context, _ string, _ int, body string) (github.IssueComment, error) {
	f.comments = append(f.comments, body)
	return github.IssueComment{HTMLURL: "https://github.com/owner/repo/issues/1#issuecomment-1"}, nil
}

func TestPollOnceStartsTrustedAuthorIssue(t *testing.T) {
	gh := &fakeGitHub{repo: testRepo(), issues: []github.Issue{testIssue("COLLABORATOR")}}
	m := newManager(t.TempDir(), "agent-ready", gh, []string{"OWNER", "MEMBER", "COLLABORATOR"})
	started := 0
	m.startJob = func(job jobPayload) bool {
		started++
		if job.Issue.Number != 1 || job.Repository.FullName != "owner/repo" {
			t.Fatalf("unexpected job: %#v", job)
		}
		return true
	}

	m.pollOnce(context.Background(), []string{"owner/repo"})

	if started != 1 {
		t.Fatalf("started = %d, want 1", started)
	}
	if len(gh.comments) != 0 {
		t.Fatalf("comments = %#v, want none", gh.comments)
	}
	if len(gh.removedLabels) != 0 {
		t.Fatalf("removedLabels = %#v, want none", gh.removedLabels)
	}
}

func TestPollOnceSkipsUntrustedAuthorIssue(t *testing.T) {
	gh := &fakeGitHub{repo: testRepo(), issues: []github.Issue{testIssue("CONTRIBUTOR")}}
	m := newManager(t.TempDir(), "agent-ready", gh, []string{"OWNER", "MEMBER", "COLLABORATOR"})
	m.startJob = func(jobPayload) bool {
		t.Fatal("untrusted issue must not start a job")
		return false
	}

	m.pollOnce(context.Background(), []string{"owner/repo"})

	if len(gh.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(gh.comments))
	}
	if len(gh.removedLabels) != 1 || gh.removedLabels[0] != "agent-ready" {
		t.Fatalf("removedLabels = %#v, want agent-ready", gh.removedLabels)
	}
	if len(gh.addedLabels) != 0 {
		t.Fatalf("addedLabels = %#v, want none", gh.addedLabels)
	}
}

func TestAuthorizedUsesConfiguredAssociations(t *testing.T) {
	m := newManager(t.TempDir(), "agent-ready", &fakeGitHub{}, []string{"owner"})
	if !m.authorized(testIssue("OWNER")) {
		t.Fatal("OWNER should be trusted")
	}
	if m.authorized(testIssue("MEMBER")) {
		t.Fatal("MEMBER should not be trusted when omitted from config")
	}
}

func testRepo() github.Repository {
	var repo github.Repository
	repo.FullName = "owner/repo"
	repo.Name = "repo"
	repo.Owner.Login = "owner"
	return repo
}

func testIssue(authorAssociation string) github.Issue {
	return github.Issue{Number: 1, Title: "task", AuthorAssociation: authorAssociation, Labels: []struct {
		Name string `json:"name"`
	}{{Name: "agent-ready"}}}
}
