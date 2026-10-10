// Example: using go-mackit as a library. This program builds a macOS
// image programmatically without a YAML config file.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	mackit "github.com/devcell-sh/go-mackit"
	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/build/buildopts"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := build.Config{
		Dest:     "mackit-output.qcow2",
		CacheDir: os.TempDir(),
		Logger:   logger,
		Opts: &buildopts.BuildOpts{
			From:     "macos/sequoia",
			Hostname: "dev-mac",
			Hooks: []buildopts.Hook{
				{Phase: buildopts.FirstBoot, Cmd: "systemsetup -setremotelogin on"},
				{Phase: buildopts.Boot, Cmd: "echo hello from mackit"},
			},
			Ports: buildopts.Ports{
				SSH: 20022,
				VNC: 25900,
			},
		},
	}

	if err := mackit.Build(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n", err)
		os.Exit(1)
	}
}
