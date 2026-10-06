package github

import (
	"context"
	"errors"
	"fmt"

	"runnerd/internal/model"
)

type JITRequest struct {
	Name          string   `json:"name"`
	RunnerGroupID int64    `json:"runner_group_id"`
	Labels        []string `json:"labels"`
	WorkFolder    string   `json:"work_folder"`
}
type JITConfig struct {
	Encoded string `json:"encoded_jit_config"`
	Runner  struct {
		ID int64 `json:"id"`
	} `json:"runner"`
}

func (c *Client) CreateJIT(ctx context.Context, req model.RunnerRequest, body JITRequest) (JITConfig, error) {
	var config JITConfig
	token, err := c.InstallationToken(ctx, req.InstallationID)
	if err != nil {
		return config, err
	}
	c.log.Debug("creating jit runner configuration", "repository", req.FullName, "runner_name", body.Name)
	err = c.request(ctx, "POST", repoPath(req.Owner, req.Repository)+"/generate-jitconfig", token, body, &config)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == 401 {
		c.invalidate(req.InstallationID)
	}
	if err != nil {
		return config, fmt.Errorf("JIT configuration could not be created with RUNNER_GROUP_ID=%d; check the configured repository runner group and App permissions: %w", body.RunnerGroupID, err)
	}
	if config.Encoded == "" || config.Runner.ID <= 0 {
		return config, fmt.Errorf("JIT response missing encoded configuration or runner ID")
	}
	return config, nil
}

func (c *Client) DeleteRunner(ctx context.Context, req model.RunnerRequest, id int64) error {
	token, err := c.InstallationToken(ctx, req.InstallationID)
	if err != nil {
		return err
	}
	err = c.request(ctx, "DELETE", fmt.Sprintf("%s/%d", repoPath(req.Owner, req.Repository), id), token, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == 404 {
			return nil
		} 
		if apiErr.Status == 401 {
			c.invalidate(req.InstallationID)
		}
	}
	return err
}
