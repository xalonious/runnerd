package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
)

type handler struct {
	out   io.Writer
	mu    *sync.Mutex
	level slog.Level
	attrs []slog.Attr
	group string
}

func New(out io.Writer, level slog.Level) *slog.Logger {
	return slog.New(&handler{out: out, mu: &sync.Mutex{}, level: level})
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	attrs := append([]slog.Attr(nil), h.attrs...)
	record.Attrs(func(a slog.Attr) bool {
		attrs = flatten(attrs, a, h.group)
		return true
	})
	var repository, job string
	var details []string
	for _, a := range attrs {
		if a.Value.Kind() == slog.KindAny && a.Value.Any() == nil {
			continue
		}
		value := a.Value.String()
		if a.Value.Kind() == slog.KindDuration {
			value = a.Value.Duration().Round(100 * time.Millisecond).String()
		}
		switch a.Key {
		case "repository":
			repository = clean(value)
		case "workflow_job_id":
			job = "job " + clean(value)
		case "delivery_id", "installation_id", "workflow_run_id", "runner_name":
			if h.level <= slog.LevelDebug {
				details = append(details, strings.ReplaceAll(a.Key, "_", " ")+": "+clean(value))
			}
		default:
			key := strings.ReplaceAll(a.Key, "_", " ")
			details = append(details, clean(key)+": "+clean(value))
		}
	}
	parts := []string{record.Time.Format("2006-01-02 15:04:05"), record.Level.String()}
	if repository != "" {
		parts = append(parts, repository)
	}
	if job != "" {
		parts = append(parts, job)
	}
	line := strings.Join(parts, " ") + " | " + clean(record.Message)
	if len(details) > 0 {
		line += " | " + strings.Join(details, " | ")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprintln(h.out, line)
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range attrs {
		copy.attrs = flatten(copy.attrs, a, h.group)
	}
	return &copy
}

func (h *handler) WithGroup(name string) slog.Handler {
	copy := *h
	if name != "" {
		copy.group += name + "."
	}
	return &copy
}

func flatten(attrs []slog.Attr, a slog.Attr, group string) []slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return attrs
	}
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			group += a.Key + "."
		}
		for _, child := range a.Value.Group() {
			attrs = flatten(attrs, child, group)
		}
		return attrs
	}
	a.Key = group + a.Key
	return append(attrs, a)
}

func clean(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value))
}
