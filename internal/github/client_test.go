package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewClientUsesTimeoutAndErrorBodyLimitDefaults(t *testing.T) {
	client := NewClient("token")

	if client.http.Timeout != DefaultTimeout {
		t.Fatalf("http timeout = %s, want %s", client.http.Timeout, DefaultTimeout)
	}
	if client.maxErrorBodyBytes != DefaultMaxErrorBodyBytes {
		t.Fatalf("maxErrorBodyBytes = %d, want %d", client.maxErrorBodyBytes, DefaultMaxErrorBodyBytes)
	}
}

func TestRequestUsesClientTimeout(t *testing.T) {
	client := NewClientWithLimits("token", time.Nanosecond, DefaultMaxErrorBodyBytes)
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})

	_, err := client.GetRepository(context.Background(), "owner/repo")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestErrorBodyIsCapped(t *testing.T) {
	client := NewClientWithLimits("token", DefaultTimeout, 8)
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(http.StatusInternalServerError, "0123456789abcdef"), nil
	})

	_, err := client.GetRepository(context.Background(), "owner/repo")
	var ghErr Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("err = %v, want github Error", err)
	}
	if ghErr.Body != "01234567" {
		t.Fatalf("body = %q", ghErr.Body)
	}
}

func TestEnsureLabelCreatesMissingLabel(t *testing.T) {
	var methods []string
	client := &Client{token: "token", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		methods = append(methods, req.Method)
		switch req.Method {
		case "GET":
			return response(http.StatusNotFound, `{"message":"Not Found"}`), nil
		case "POST":
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), `"name":"agent-ready"`) {
				t.Fatalf("unexpected body: %s", string(body))
			}
			return response(http.StatusCreated, `{}`), nil
		default:
			t.Fatalf("unexpected method: %s", req.Method)
			return nil, nil
		}
	})}}

	if err := client.EnsureLabel(context.Background(), "owner/repo", LabelSpec{Name: "agent-ready", Color: "2da44e", Description: "ready"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "GET,POST" {
		t.Fatalf("methods = %v", methods)
	}
}

func TestEnsureLabelSkipsExistingLabel(t *testing.T) {
	calls := 0
	client := &Client{token: "token", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		return response(http.StatusOK, `{}`), nil
	})}}

	if err := client.EnsureLabel(context.Background(), "owner/repo", LabelSpec{Name: "agent-ready", Color: "2da44e", Description: "ready"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestEnsureLabelIgnoresCreateRace(t *testing.T) {
	client := &Client{token: "token", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "GET":
			return response(http.StatusNotFound, `{"message":"Not Found"}`), nil
		case "POST":
			return response(http.StatusUnprocessableEntity, `{"message":"already_exists"}`), nil
		default:
			t.Fatalf("unexpected method: %s", req.Method)
			return nil, nil
		}
	})}}

	if err := client.EnsureLabel(context.Background(), "owner/repo", LabelSpec{Name: "agent-ready", Color: "2da44e", Description: "ready"}); err != nil {
		t.Fatal(err)
	}
}

func TestListPullRequestsFiltersByHeadAndBase(t *testing.T) {
	client := &Client{token: "token", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		if req.URL.Path != "/repos/owner/repo/pulls" {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		query := req.URL.Query()
		if query.Get("state") != "open" || query.Get("head") != "owner:agent/issue-1" || query.Get("base") != "main" {
			t.Fatalf("unexpected query: %s", req.URL.RawQuery)
		}
		return response(http.StatusOK, `[{"html_url":"https://github.com/owner/repo/pull/1","number":1}]`), nil
	})}}

	prs, err := client.ListPullRequests(context.Background(), "owner/repo", "owner:agent/issue-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].HTMLURL != "https://github.com/owner/repo/pull/1" {
		t.Fatalf("prs = %#v", prs)
	}
}

func TestListIssuesIncludesAuthorAssociation(t *testing.T) {
	client := &Client{token: "token", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		return response(http.StatusOK, `[{"number":1,"title":"task","author_association":"MEMBER","labels":[{"name":"agent-ready"}]}]`), nil
	})}}

	issues, err := client.ListIssues(context.Background(), "owner/repo", "agent-ready")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].AuthorAssociation != "MEMBER" {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestCreateIssueComment(t *testing.T) {
	client := &Client{token: "token", http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		if req.URL.Path != "/repos/owner/repo/issues/7/comments" {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), `"body":"PR already open for this issue: https://github.com/owner/repo/pull/1"`) {
			t.Fatalf("unexpected body: %s", string(body))
		}
		return response(http.StatusCreated, `{"html_url":"https://github.com/owner/repo/issues/7#issuecomment-1"}`), nil
	})}}

	comment, err := client.CreateIssueComment(context.Background(), "owner/repo", 7, "PR already open for this issue: https://github.com/owner/repo/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	if comment.HTMLURL == "" {
		t.Fatal("expected comment url")
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
