package vz

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/devcell-sh/go-mackit/vm"
)

const ScreenshotTimeLayout = "20060102T150405Z"

// CaptureScreenshots polls the VM for screenshots and saves them as PNGs
// until stop is closed. Mirrors qemu.CaptureScreenshots.
func CaptureScreenshots(v vm.VM, shotDir string, interval time.Duration, stop <-chan struct{}, done chan<- struct{}, logger *slog.Logger) {
	defer close(done)
	if interval == 0 {
		interval = 30 * time.Second
	}
	os.MkdirAll(shotDir, 0755)

	type screenshotter interface {
		Screenshot(context.Context) (image.Image, error)
	}
	ss, ok := v.(screenshotter)
	if !ok {
		logger.Debug("screenshot: VM does not support Screenshot()")
		return
	}

	seq := 0
	tick := time.NewTicker(interval)
	defer tick.Stop()

	shoot := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		seq++
		img, err := ss.Screenshot(ctx)
		if err != nil {
			logger.Debug("screenshot failed", "seq", seq, "error", err)
			return
		}
		name := fmt.Sprintf("%s-periodic-%04d.png", time.Now().UTC().Format(ScreenshotTimeLayout), seq)
		path := filepath.Join(shotDir, name)
		f, err := os.Create(path)
		if err != nil {
			logger.Debug("screenshot save failed", "path", path, "error", err)
			return
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			logger.Debug("screenshot encode failed", "error", err)
			return
		}
		logger.Debug("screenshot saved", "path", path, "seq", seq)
	}

	shoot()
	for {
		select {
		case <-stop:
			shoot()
			return
		case <-tick.C:
			shoot()
		}
	}
}
