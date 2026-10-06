package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Process struct {
	Grace   time.Duration
	Secrets []string
}

func Command(ctx context.Context, workspace, encoded string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, filepath.Join(workspace, "run.sh"), "--jitconfig", encoded)
	cmd.Dir = workspace
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if strings.HasPrefix(key, "GITHUB_APP_") || key == "GITHUB_PRIVATE_KEY_PATH" || key == "GITHUB_WEBHOOK_SECRET" || strings.HasPrefix(key, "RUNNER_") || key == "LISTEN_ADDR" || key == "MAX_CONCURRENT_RUNNERS" || key == "QUEUE_SIZE" || key == "SHUTDOWN_TIMEOUT" {
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	return cmd
}

func (p Process) Run(ctx context.Context, workspace, encoded string, log *slog.Logger) (int, error) {
	cmd := Command(ctx, workspace, encoded)
	secrets := append(append([]string(nil), p.Secrets...), encoded)
	stdout := &logWriter{log: log, stream: "stdout", secrets: secrets}
	stderr := &logWriter{log: log, stream: "stderr", secrets: secrets}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	defer stdout.Flush()
	defer stderr.Flush()
	if err := os.WriteFile(filepath.Join(workspace, ".active"), []byte("pending\n"), 0600); err != nil {
		return -1, fmt.Errorf("mark runner launch: %w", err)
	}
	return runProcess(ctx, cmd, p.Grace, func(pid int) error {
		return os.WriteFile(filepath.Join(workspace, ".active"), []byte(fmt.Sprintf("%d\n", pid)), 0600)
	})
}

type logWriter struct {
	mu      sync.Mutex
	log     *slog.Logger
	stream  string
	secrets []string
	line    []byte
	dropped bool
}

var _ io.Writer = (*logWriter)(nil)

func (w *logWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ch := range b {
		if ch == '\n' {
			w.emit()
			continue
		}
		if w.dropped {
			continue
		}
		if len(w.line) >= 64<<10 {
			w.line = nil
			w.dropped = true
			continue
		}
		w.line = append(w.line, ch)
	}
	return len(b), nil
}
func (w *logWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.line) > 0 || w.dropped {
		w.emit()
	}
}
func (w *logWriter) emit() {
	line := string(w.line)
	if w.dropped {
		line = "[runner output line exceeded 64 KiB and was omitted]"
	} else {
		for _, secret := range w.secrets {
			if secret != "" {
				line = strings.ReplaceAll(line, secret, "[REDACTED]")
			}
		}
	}
	if line = strings.TrimSpace(line); line != "" {
		if w.stream == "stderr" {
			w.log.Info("runner: "+line, "stream", w.stream)
		} else {
			w.log.Info("runner: " + line)
		}
	}
	w.line = nil
	w.dropped = false
}
