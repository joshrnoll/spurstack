package llm

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

func TestNewWithProviderUsesTimeoutAndResponseLimitDefaults(t *testing.T) {
	client := NewWithProvider("key", "https://example.test/", "model", nil, true)

	if client.http.Timeout != DefaultTimeout {
		t.Fatalf("http timeout = %s, want %s", client.http.Timeout, DefaultTimeout)
	}
	if client.timeout != DefaultTimeout {
		t.Fatalf("call timeout = %s, want %s", client.timeout, DefaultTimeout)
	}
	if client.maxResponseBytes != DefaultMaxResponseBytes {
		t.Fatalf("maxResponseBytes = %d, want %d", client.maxResponseBytes, DefaultMaxResponseBytes)
	}
}

func TestChatRejectsOversizedResponse(t *testing.T) {
	client := NewWithProviderAndLimits("key", "https://example.test", "model", nil, true, time.Minute, 32)
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return llmResponse(http.StatusOK, strings.Repeat("x", 33)), nil
	})}

	_, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "llm response exceeded 32 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestChatUsesPerCallTimeout(t *testing.T) {
	client := NewWithProviderAndLimits("key", "https://example.test", "model", nil, true, time.Nanosecond, 1024)
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}

	_, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func llmResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
