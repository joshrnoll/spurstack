package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	gh "spurstack/internal/github"
)

type IssueEvent struct {
	Action     string        `json:"action"`
	Issue      gh.Issue      `json:"issue"`
	Repository gh.Repository `json:"repository"`
}

func ParseGitHubIssueEvent(r *http.Request, secret string) (IssueEvent, bool, error) {
	var ev IssueEvent
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return ev, false, err
	}
	if !validSignature(body, secret, r.Header.Get("X-Hub-Signature-256")) {
		return ev, false, fmt.Errorf("invalid webhook signature")
	}
	if r.Header.Get("X-GitHub-Event") != "issues" {
		return ev, false, nil
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return ev, false, err
	}
	switch ev.Action {
	case "opened", "edited", "reopened", "labeled":
		return ev, true, nil
	default:
		return ev, false, nil
	}
}

func HasLabel(issue gh.Issue, label string) bool {
	for _, l := range issue.Labels {
		if strings.EqualFold(l.Name, label) {
			return true
		}
	}
	return false
}

func validSignature(body []byte, secret, got string) bool {
	if secret == "" || !strings.HasPrefix(got, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(got))
}
