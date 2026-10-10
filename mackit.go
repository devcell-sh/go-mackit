package mackit

import (
	"context"
	"fmt"
	"os"

	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/build/buildopts"
)

func Build(ctx context.Context, cfg build.Config) error {
	if cfg.Opts == nil {
		cfg.Opts = &buildopts.BuildOpts{}
	}
	cfg.Opts.ApplyDefaults()
	if err := cfg.Opts.Validate(); err != nil {
		return err
	}

	switch {
	case cfg.Dest == "":
		return fmt.Errorf("build.Config.Dest is required")
	case cfg.InstallerDMG == "":
		return fmt.Errorf("build.Config.InstallerDMG is required")
	}

	if cfg.WorkDir == "" {
		workDir, err := os.MkdirTemp("", "mackit-build-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(workDir)
		cfg.WorkDir = workDir
	} else if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return fmt.Errorf("creating work dir: %w", err)
	}

	switch cfg.Opts.Stage() {
	case buildopts.StageRecovery:
		return build.Recovery(ctx, cfg)
	default:
		return build.Full(ctx, cfg)
	}
}
