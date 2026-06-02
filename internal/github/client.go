package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	token string
	http  *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, http: http.DefaultClient}
}

type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
	Labels      []struct {
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

func (c *Client) AuthedCloneURL(cloneURL string) string {
	return strings.Replace(cloneURL, "https://", "https://x-access-token:"+c.token+"@", 1)
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
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("github %s %s failed: %s: %s", method, url, resp.Status, buf.String())
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
