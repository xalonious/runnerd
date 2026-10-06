package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"runnerd/internal/config"
	"runnerd/internal/dedupe"
	"runnerd/internal/github"
	"runnerd/internal/logging"
	"runnerd/internal/queue"
	"runnerd/internal/runner"
	"runnerd/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		logging.New(os.Stderr, slog.LevelInfo).Error("runnerd stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	c, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	log := logging.New(os.Stdout, c.LogLevel)
	key, err := github.LoadPrivateKey(c.PrivateKeyPath)
	if err != nil {
		return err
	}
	workspaces, err := runner.NewWorkspaces(c.RunnerHome, c.StateDir)
	if err != nil {
		return err
	}
	unlock, err := runner.LockState(workspaces.State)
	if err != nil {
		return err
	}
	defer unlock()
	if err := workspaces.CleanupStale(log); err != nil {
		return fmt.Errorf("startup cleanup: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}
	listener, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	// Reserve part of the overall PM2 shutdown budget for HTTP and cleanup.
	grace := c.ShutdownTimeout / 2
	manager := &runner.Manager{GitHub: github.New(c.AppID, key, log), Workspaces: workspaces, Process: runner.Process{Grace: grace, Secrets: []string{c.WebhookSecret}}, Log: log, Hostname: host, Label: c.Label, GroupID: c.RunnerGroupID, Timeout: c.RunnerTimeout}
	workers := queue.New(context.Background(), c.QueueSize, c.Concurrency, manager, log)
	defer workers.Stop()
	cache := dedupe.New(time.Hour, 10000)
	handler := &webhook.Handler{Secret: c.WebhookSecret, Label: c.Label, Queue: workers, Cache: cache, Log: log}
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { health(w, r, true) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ready := !handler.Stopped.Load() && workspaces.Ready() == nil
		if ready {
			f, err := os.Open(c.PrivateKeyPath)
			ready = err == nil
			if err == nil {
				_ = f.Close()
			}
		}
		health(w, r, ready)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	signals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	log.Info("runnerd listening", "listen_addr", listener.Addr().String(), "workers", c.Concurrency, "queue_size", c.QueueSize)
	var serveErr error
loop:
	for {
		select {
		case <-signals.Done():
			break loop
		case serveErr = <-serverErr:
			break loop
		case <-ticker.C:
			cache.Cleanup()
		}
	}
	log.Info("shutting down")
	handler.Stopped.Store(true)
	workers.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		log.Warn("HTTP shutdown interrupted", "error", err)
	}
	// Workers kill their own process groups after the grace interval and always attempt cleanup.
	workers.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server: %w", serveErr)
	}
	log.Info("shutdown completed")
	return nil
}

func health(w http.ResponseWriter, r *http.Request, ready bool) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("{\"status\":\"unavailable\"}\n"))
		return
	}
	_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
}
