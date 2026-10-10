package logx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		out = append(out, m)
	}
	return out
}

func TestWithService_AddsDefault(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(WithService(slog.NewJSONHandler(&buf, nil), "mackit"))
	l.Info("screen frame saved", "path", "x.png")
	l.With("component", "flow").Info("screen changed", "screen", "recovery.utilities")

	recs := records(t, &buf)
	require.Len(t, recs, 2)
	for _, r := range recs {
		require.Equal(t, "mackit", r["service"], r["msg"])
	}
	require.Equal(t, "flow", recs[1]["component"])
}

func TestWithService_KeepsExplicit(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(WithService(slog.NewJSONHandler(&buf, nil), "mackit"))
	l.Info("AppleACPIPlatform: ...", "service", "xnu")
	l.With("service", "textshot").Info("onnxruntime ready")

	recs := records(t, &buf)
	require.Len(t, recs, 2)
	require.Equal(t, "xnu", recs[0]["service"])
	require.Equal(t, "textshot", recs[1]["service"])
	for _, r := range recs {
		require.Equal(t, 1, strings.Count(buf.String(), `"service":"`+r["service"].(string)+`"`), "service must appear once per record")
	}
}

func TestWithService_Idempotent(t *testing.T) {
	var buf bytes.Buffer
	h := WithService(WithService(slog.NewJSONHandler(&buf, nil), "a"), "b")
	slog.New(h).Info("x")
	recs := records(t, &buf)
	require.Equal(t, "b", recs[0]["service"])
	require.Equal(t, 1, strings.Count(buf.String(), `"service"`))
}

func TestLogger_NilIsDiscard(t *testing.T) {
	l := Logger(nil, "mackit")
	require.NotNil(t, l)
	l.Info("dropped")
}
