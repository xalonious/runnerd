package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"runnerd/internal/dedupe"
	"runnerd/internal/model"
)

const MaxBodyBytes int64 = 2 << 20

type Enqueuer interface {
	Enqueue(model.RunnerRequest) error
}
type Handler struct {
	Secret, Label string
	Queue         Enqueuer
	Cache         *dedupe.Cache
	Log           *slog.Logger
	Stopped       atomic.Bool
}

var deliveryPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "cannot read payload", http.StatusBadRequest)
		}
		return
	}
	if !Verify(body, r.Header.Get("X-Hub-Signature-256"), h.Secret) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if h.Stopped.Load() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")
	if !deliveryPattern.MatchString(delivery) {
		http.Error(w, "missing or invalid delivery ID", http.StatusBadRequest)
		return
	}
	if event != "workflow_job" {
		h.Log.Debug("webhook ignored", "event", event, "delivery_id", delivery)
		w.WriteHeader(http.StatusOK)
		return
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil || p.Action == "" || p.WorkflowJob.Status == "" {
		http.Error(w, "malformed workflow_job payload", http.StatusBadRequest)
		return
	}
	if p.Action != "queued" || p.WorkflowJob.Status != "queued" {
		h.Log.Debug("workflow job ignored", "delivery_id", delivery, "event", event, "action", p.Action, "status", p.WorkflowJob.Status)
		w.WriteHeader(http.StatusOK)
		return
	}
	match := false
	for _, label := range p.WorkflowJob.Labels {
		if strings.EqualFold(label, h.Label) {
			match = true
			break
		}
	}
	if !match {
		h.Log.Debug("workflow job ignored", "delivery_id", delivery, "reason", "broker label absent")
		w.WriteHeader(http.StatusOK)
		return
	}
	req, err := p.Request(delivery)
	if err != nil {
		http.Error(w, "invalid workflow_job metadata", http.StatusBadRequest)
		return
	}
	log := h.Log.With("delivery_id", delivery, "repository", req.FullName, "installation_id", req.InstallationID, "workflow_job_id", req.JobID, "workflow_run_id", req.RunID)
	repositoryKey := "name:" + strings.ToLower(req.FullName)
	if req.RepositoryID > 0 {
		repositoryKey = fmt.Sprintf("id:%d", req.RepositoryID)
	}
	jobKey := fmt.Sprintf("job:%d:%s:%d", req.InstallationID, repositoryKey, req.JobID)
	duplicate, err := h.Cache.Commit("delivery:"+delivery, func() error { return h.Queue.Enqueue(req) }, jobKey)
	if err != nil {
		log.Warn("runner request rejected", "error", err)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "broker capacity unavailable; redeliver this webhook", http.StatusServiceUnavailable)
		return
	}
	if duplicate {
		log.Debug("duplicate webhook or workflow job ignored")
		w.WriteHeader(http.StatusOK)
		return
	}
	log.Info("job queued")
	w.WriteHeader(http.StatusAccepted)
}
