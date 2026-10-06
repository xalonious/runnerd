package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"runnerd/internal/github"
	"runnerd/internal/model"
)

type GitHub interface {
	CreateJIT(context.Context, model.RunnerRequest, github.JITRequest) (github.JITConfig, error)
	DeleteRunner(context.Context, model.RunnerRequest, int64) error
}
type Launcher interface {
	Run(context.Context, string, string, *slog.Logger) (int, error)
}
type Manager struct {
	GitHub          GitHub
	Workspaces      *Workspaces
	Process         Launcher
	Log             *slog.Logger
	Hostname, Label string
	GroupID         int64
	Timeout         time.Duration
}

func Name(host string, jobID int64) (string, error) {
	var clean strings.Builder
	for _, ch := range host {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' {
			clean.WriteRune(ch)
		} else {
			clean.WriteByte('-')
		}
		if clean.Len() >= 32 {
			break
		}
	}
	if clean.Len() == 0 {
		clean.WriteString("host")
	}
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate runner name: %w", err)
	}
	return fmt.Sprintf("runnerd-%s-%d-%s", clean.String(), jobID, hex.EncodeToString(random)), nil
}

func Labels(custom string) []string {
	arch := map[string]string{"arm64": "ARM64", "amd64": "X64", "arm": "ARM"}[runtime.GOARCH]
	if arch == "" {
		arch = strings.ToUpper(runtime.GOARCH)
	}
	labels := []string{"self-hosted", "Linux", arch}
	for _, label := range labels {
		if strings.EqualFold(label, custom) {
			return labels
		}
	}
	return append(labels, custom)
}

func (m *Manager) Run(parent context.Context, req model.RunnerRequest) (err error) {
	ctx, cancel := context.WithTimeout(parent, m.Timeout)
	defer cancel()
	name, err := Name(m.Hostname, req.JobID)
	if err != nil {
		return err
	}
	log := m.Log.With("delivery_id", req.DeliveryID, "repository", req.FullName, "installation_id", req.InstallationID, "workflow_job_id", req.JobID, "workflow_run_id", req.RunID, "runner_name", name)
	workspace, err := m.Workspaces.Create(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanupErr := m.Workspaces.Cleanup(workspace)
		if cleanupErr != nil {
			log.Error("workspace cleanup failed", "error", cleanupErr)
			err = errors.Join(err, cleanupErr)
		} else {
			log.Info("cleanup completed")
		}
	}()
	config, err := m.GitHub.CreateJIT(ctx, req, github.JITRequest{Name: name, RunnerGroupID: m.GroupID, Labels: Labels(m.Label), WorkFolder: "_work"})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if deleteErr := m.GitHub.DeleteRunner(cleanupCtx, req, config.Runner.ID); deleteErr != nil {
			log.Warn("remote runner cleanup failed; inspect repository runner settings", "error", deleteErr)
			err = errors.Join(err, deleteErr)
		}
	}()
	log.Info("starting runner")
	start := time.Now()
	code, err := m.Process.Run(ctx, workspace, config.Encoded, log)
	log.Info("runner exited", "exit_code", code, "duration", time.Since(start), "status", ctx.Err())
	return err
}
