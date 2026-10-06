package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const APIVersion = "2026-03-10"

type cachedToken struct {
	token   string
	expires time.Time
}
type Client struct {
	base   string
	http   *http.Client
	appID  int64
	key    *rsa.PrivateKey
	log    *slog.Logger
	mu     sync.Mutex
	tokens map[int64]cachedToken
	now    func() time.Time
}

func New(appID int64, key *rsa.PrivateKey, log *slog.Logger) *Client {
	return WithEndpoint("https://api.github.com", appID, key, log)
}

func WithEndpoint(base string, appID int64, key *rsa.PrivateKey, log *slog.Logger) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, appID: appID, key: key, log: log, tokens: make(map[int64]cachedToken), now: time.Now}
}

type APIError struct {
	Status    int
	RequestID string
	Message   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API returned HTTP %d: %s (request ID %s)", e.Status, e.Message, e.RequestID)
}

func (c *Client) request(ctx context.Context, method, path, token string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode GitHub request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return fmt.Errorf("create GitHub request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "runnerd")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send GitHub request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var response struct {
			Message string `json:"message"`
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = json.Unmarshal(data, &response)
		message := sanitizeMessage(response.Message, token)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: message, RequestID: sanitizeMessage(resp.Header.Get("X-GitHub-Request-Id"), token)}
	}
	if out == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return fmt.Errorf("read GitHub response: %w", err)
	}
	if len(b) > 2<<20 {
		return fmt.Errorf("GitHub response exceeds 2 MiB")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

var credentialText = regexp.MustCompile(`(?i)(?:gh[pousr]_[a-z0-9_]+|github_pat_[a-z0-9_]+|[a-z0-9_+/=-]{80,}|eyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+)`)

func sanitizeMessage(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	message = credentialText.ReplaceAllString(message, "[REDACTED]")
	message = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, message)
	if len(message) > 512 {
		message = message[:512] + "..."
	}
	return message
}

func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runners"
}
