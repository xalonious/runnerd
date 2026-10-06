package github

import (
	"context"
	"fmt"
	"time"
)

func (c *Client) InstallationToken(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("installation ID must be positive")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if token, ok := c.tokens[id]; ok && now.Add(2*time.Minute).Before(token.expires) {
		return token.token, nil
	}
	for key, token := range c.tokens {
		if !now.Add(2 * time.Minute).Before(token.expires) {
			delete(c.tokens, key)
		}
	}
	if len(c.tokens) >= 1024 {
		return "", fmt.Errorf("installation token cache at capacity")
	}
	jwt, err := JWT(c.key, c.appID, now)
	if err != nil {
		return "", err
	}
	c.log.Debug("creating installation token", "installation_id", id)
	var response struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = c.request(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", id), jwt, struct{}{}, &response)
	if err != nil {
		return "", fmt.Errorf("create installation token: %w", err)
	}
	if response.Token == "" || !response.ExpiresAt.After(now.Add(2*time.Minute)) {
		return "", fmt.Errorf("installation token response is missing a token or usable expiry")
	}
	c.tokens[id] = cachedToken{token: response.Token, expires: response.ExpiresAt}
	return response.Token, nil
}

func (c *Client) invalidate(id int64) { c.mu.Lock(); delete(c.tokens, id); c.mu.Unlock() }
