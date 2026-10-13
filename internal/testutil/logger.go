package testutil

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLogger returns a logger that writes to both t.Log and build.jsonl in resultDir.
func TestLogger(t *testing.T, resultDir string) *slog.Logger {
	t.Helper()
	var f *os.File
	if resultDir != "" {
		var err error
		f, err = os.OpenFile(filepath.Join(resultDir, "build.jsonl"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			t.Cleanup(func() { f.Close() })
		}
	}
	return slog.New(&tlogHandler{t: t, file: f})
}

type tlogHandler struct {
	t     *testing.T
	file  *os.File
	attrs []slog.Attr
}

func (h *tlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *tlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &tlogHandler{t: h.t, file: h.file, attrs: append(h.attrs[:len(h.attrs):len(h.attrs)], attrs...)}
}

func (h *tlogHandler) WithGroup(string) slog.Handler { return h }

func (h *tlogHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.Any(); return true })
	for _, a := range h.attrs {
		attrs[a.Key] = a.Value.Any()
	}

	line := r.Message
	for k, v := range attrs {
		line += " " + k + "=" + slog.AnyValue(v).String()
	}
	h.t.Log(line)

	if h.file != nil {
		entry := map[string]any{
			"ts":  r.Time.UTC().Format(time.RFC3339Nano),
			"msg": r.Message,
		}
		for k, v := range attrs {
			entry[k] = v
		}
		if b, err := json.Marshal(entry); err == nil {
			h.file.Write(append(b, '\n'))
		}
	}
	return nil
}
