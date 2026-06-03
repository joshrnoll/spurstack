package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultTimeout           = 60 * time.Second
	DefaultMaxErrorBodyBytes = 64 * 1024
)

type Client struct {
	token             string
	http              *http.Client
	maxErrorBodyBytes int64
}

type Error struct {
	Method     string
	URL        string
	Status     string
	StatusCode int
	Body       string
}

func (e Error) Error() string {
	return fmt.Sprintf("github %s %s failed: %s: %s", e.Method, e.URL, e.Status, e.Body)
}

type LabelSpec struct {
	Name        string
	Color       string
	Description string
}

func NewClient(token string) *Client {
	return NewClientWithLimits(token, DefaultTimeout, DefaultMaxErrorBodyBytes)
}

func NewClientWithLimits(token string, timeout time.Duration, maxErrorBodyBytes int64) *Client {
	if maxErrorBodyBytes <= 0 {
		maxErrorBodyBytes = DefaultMaxErrorBodyBytes
	}
	return &Client{token: token, http: &http.Client{Timeout: timeout}, maxErrorBodyBytes: maxErrorBodyBytes}
}

type Issue struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	HTMLURL           string    `json:"html_url"`
	AuthorAssociation string    `json:"author_association"`
	PullRequest       *struct{} `json:"pull_request,omitempty"`
	Labels            []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type Repository struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
	Name string `json:"name"`
}

type CreatePREquest struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
	Draft bool   `json:"draft"`
}

type PullRequest struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

type IssueComment struct {
	HTMLURL string `json:"html_url"`
}

func (c *Client) GetRepository(ctx context.Context, repoFullName string) (Repository, error) {
	var repo Repository
	if err := c.request(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s", repoFullName), nil, &repo); err != nil {
		return repo, err
	}
	return repo, nil
}

func (c *Client) ListIssues(ctx context.Context, repoFullName, label string) ([]Issue, error) {
	var issues []Issue
	u := fmt.Sprintf("https://api.github.com/repos/%s/issues?state=open&labels=%s&per_page=100", repoFullName, url.QueryEscape(label))
	if err := c.request(ctx, "GET", u, nil, &issues); err != nil {
		return nil, err
	}
	var filtered []Issue
	for _, issue := range issues {
		if issue.PullRequest == nil {
			filtered = append(filtered, issue)
		}
	}
	return filtered, nil
}

func (c *Client) AddLabels(ctx context.Context, repoFullName string, issueNumber int, labels ...string) error {
	b, _ := json.Marshal(map[string][]string{"labels": labels})
	return c.request(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/labels", repoFullName, issueNumber), b, nil)
}

func (c *Client) EnsureLabels(ctx context.Context, repoFullName string, labels ...LabelSpec) error {
	for _, label := range labels {
		if err := c.EnsureLabel(ctx, repoFullName, label); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) EnsureLabel(ctx context.Context, repoFullName string, label LabelSpec) error {
	name := strings.TrimSpace(label.Name)
	if name == "" {
		return fmt.Errorf("label name is required")
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/labels/%s", repoFullName, url.PathEscape(name))
	if err := c.request(ctx, "GET", url, nil, nil); err == nil {
		return nil
	} else {
		var ghErr Error
		if !errors.As(err, &ghErr) || ghErr.StatusCode != http.StatusNotFound {
			return err
		}
	}
	body, _ := json.Marshal(map[string]string{"name": name, "color": label.Color, "description": label.Description})
	if err := c.request(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/labels", repoFullName), body, nil); err != nil {
		var ghErr Error
		if errors.As(err, &ghErr) && ghErr.StatusCode == http.StatusUnprocessableEntity {
			return nil
		}
		return err
	}
	return nil
}

func (c *Client) RemoveLabel(ctx context.Context, repoFullName string, issueNumber int, label string) error {
	return c.request(ctx, "DELETE", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/labels/%s", repoFullName, issueNumber, url.PathEscape(label)), nil, nil)
}

func (c *Client) CreatePullRequest(ctx context.Context, repoFullName string, req CreatePREquest) (PullRequest, error) {
	var pr PullRequest
	b, _ := json.Marshal(req)
	if err := c.request(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/pulls", repoFullName), b, &pr); err != nil {
		return pr, err
	}
	return pr, nil
}

func (c *Client) ListPullRequests(ctx context.Context, repoFullName, head, base string) ([]PullRequest, error) {
	var prs []PullRequest
	u := fmt.Sprintf("https://api.github.com/repos/%s/pulls?state=open&head=%s&base=%s&per_page=100", repoFullName, url.QueryEscape(head), url.QueryEscape(base))
	if err := c.request(ctx, "GET", u, nil, &prs); err != nil {
		return nil, err
	}
	return prs, nil
}

func (c *Client) CreateIssueComment(ctx context.Context, repoFullName string, issueNumber int, body string) (IssueComment, error) {
	var comment IssueComment
	b, _ := json.Marshal(map[string]string{"body": body})
	if err := c.request(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments", repoFullName, issueNumber), b, &comment); err != nil {
		return comment, err
	}
	return comment, nil
}

func (c *Client) request(ctx context.Context, method, url string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(io.LimitReader(resp.Body, c.maxErrorBodyBytes))
		return Error{Method: method, URL: url, Status: resp.Status, StatusCode: resp.StatusCode, Body: buf.String()}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
