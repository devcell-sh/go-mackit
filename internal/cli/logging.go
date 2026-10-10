package cli

import (
	"io"
	"log/slog"
	"os"
)

func newLogger(debug bool) *slog.Logger {
	if debug {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
