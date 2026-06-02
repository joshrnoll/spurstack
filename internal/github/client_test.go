package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
