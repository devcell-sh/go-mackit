package build

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/devcell-sh/go-textshot"

	"github.com/devcell-sh/go-mackit/installer/flow"
	"github.com/devcell-sh/go-mackit/internal/logx"
	"github.com/devcell-sh/go-mackit/media"
	"github.com/devcell-sh/go-mackit/vm/qemu"
)

// screenDriver is the OCR-driven installer UI driver for one VM.
type screenDriver struct {
	runner  *flow.Runner
	engine  *textshot.Engine
	version string // macOS codename the catalog is for
}

// newScreenDriver prepares OCR screen detection for the VM behind qmpSock.
// It returns nil (and logs why) when OCR cannot run, so callers can fall
// back to timed keystrokes. The textshot cache lives under mackit's cache
// dir so everything mackit downloads stays in one place. With debug
// logging enabled (--debug), every frame the driver reads is saved into
// shotDir next to the periodic screenshots, and what OCR read from it as
// JSON into the sibling "ocr" directory.
func newScreenDriver(ctx context.Context, c Config, qmpSock, shotDir string, logger *slog.Logger) *screenDriver {
	from := "macos/sequoia"
	if c.Opts != nil && c.Opts.From != "" {
		from = c.Opts.From
	}
	version, _, err := media.ParseFrom(from)
	if err != nil {
		logger.Info("screen detection disabled", "reason", err)
		return nil
	}
	catalog, err := flow.CatalogFor(version)
	if err != nil {
		logger.Info("screen detection disabled", "reason", err)
		return nil
	}

	// One OCR thread: on TCG hosts the guest shares the CPUs with the OCR,
	// and a busy OCR loop visibly slows the boot. The runner only runs the
	// models when the frame changed, and the element detector only when
	// the text says the wanted screen is up.
	opts := textshot.Options{Logger: logger.With(logx.ServiceKey, "textshot"), Threads: 1}
	if c.CacheDir != "" {
		opts.CacheDir = filepath.Join(c.CacheDir, "textshot")
	}
	engine, err := textshot.New(ctx, opts)
	if err != nil {
		if errors.Is(err, textshot.ErrUnavailable) {
			logger.Info("OCR unavailable, using timed keystrokes", "reason", err)
		} else {
			logger.Warn("OCR setup failed, using timed keystrokes", "error", err)
		}
		return nil
	}

	runner := &flow.Runner{
		Capture: func(context.Context) (image.Image, error) { return qemu.Screendump(qmpSock) },
		OCR:     flow.NewEngineOCR(engine),
		Input:   qemu.QMPInput{Sock: qmpSock},
		Catalog: catalog,
		Logger:  logger.With("component", "flow"),
		// Captures are cheap and unchanged frames reuse their reading, so
		// poll often; after an action wait for the UI to stop moving.
		Interval:       2 * time.Second,
		SettleDelay:    300 * time.Millisecond,
		SettleTimeout:  3 * time.Second,
		EnterOnDefault: true,
	}
	if shotDir != "" && logger.Enabled(ctx, slog.LevelDebug) {
		runner.ShotDir = shotDir
		runner.OCRDir = filepath.Join(filepath.Dir(shotDir), "ocr")
		logger.Debug("saving every OCR frame and reading", "frames", runner.ShotDir, "readings", runner.OCRDir)
	}
	return &screenDriver{engine: engine, runner: runner, version: version}
}


// Close releases the OCR engine and its models (several hundred MB of
// memory). Safe to call more than once; the build calls it as soon as the
// screen no longer needs reading, before it idles or waits for SSH.
func (d *screenDriver) Close() {
	if d != nil && d.engine != nil {
		d.engine.Close()
		d.engine = nil
	}
}

// bootSteps wait for macOS Recovery's Utilities screen, pressing Enter
// whenever the OpenCore picker is seen on the way.
func bootSteps(recoveryTimeout time.Duration) []flow.Step {
	return []flow.Step{
		{
			Name:    "macOS Recovery",
			Screen:  "recovery.utilities",
			Timeout: recoveryTimeout,
			Reactions: []flow.Reaction{
				{Screen: "opencore.picker", Actions: []flow.Action{flow.Keys("ret")}},
			},
			DoneOn: []string{"recovery.language"},
		},
	}
}

// pickerStep confirms the OpenCore picker when it is visible; optional
// because the patched config hides it.
func pickerStep() flow.Step {
	return flow.Step{
		Name:     "opencore picker",
		Screen:   "opencore.picker",
		Timeout:  30 * time.Second,
		Optional: true,
		Actions:  []flow.Action{flow.Keys("ret")},
	}
}

// autoSelectPicker is the blind fallback used without OCR: press Enter
// after a fixed delay in case the OpenCore picker is showing.
func autoSelectPicker(qmpSock string, logger *slog.Logger) {
	time.Sleep(10 * time.Second)
	if err := qemu.QMPSendKey(qmpSock, "ret"); err != nil {
		logger.Debug("OpenCore auto-select key", "error", err)
	} else {
		logger.Info("sent Enter to OpenCore boot picker")
	}
}

// driveInstall boots to macOS Recovery and runs the unattended install
// flow for the catalog's macOS version onto a target disk of diskGB.
// Versions without a flow return an error naming it.
func (d *screenDriver) driveInstall(ctx context.Context, qmpSock string, diskGB int, timeout time.Duration, stopAfter string) error {
	steps, err := flow.InstallStepsFor(d.version, diskGB)
	if err != nil {
		return err
	}
	if steps, err = flow.StepsThrough(steps, stopAfter); err != nil {
		return err
	}
	if err := d.driveRecovery(ctx, qmpSock, nil, timeout); err != nil {
		return err
	}
	if err := d.runner.Run(ctx, steps); err != nil {
		return fmt.Errorf("install steps: %w", err)
	}
	return nil
}

// driveRecovery waits for macOS Recovery to reach the Utilities screen and
// then runs the configured recovery steps. The blind Enter stays as a
// safety net for a picker whose text OCR cannot read.
func (d *screenDriver) driveRecovery(ctx context.Context, qmpSock string, steps []flow.Step, timeout time.Duration) error {
	go autoSelectPicker(qmpSock, d.runner.Logger)
	if err := d.runner.Run(ctx, bootSteps(timeout)); err != nil {
		return fmt.Errorf("booting to macOS Recovery: %w", err)
	}
	d.runner.Logger.Info("macOS Recovery reached")
	if len(steps) == 0 {
		return nil
	}
	if err := d.runner.Run(ctx, steps); err != nil {
		return fmt.Errorf("recovery steps: %w", err)
	}
	return nil
}
