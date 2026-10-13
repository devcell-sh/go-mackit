package build

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/vm/qemu"
)

// TestPrepareOpenCore patches the cached OpenCore release into a work dir,
// as both builds and mackit.Start do. Skips without the cached image.
func TestPrepareOpenCore(t *testing.T) {
	if _, err := os.Stat(filepath.Join(cache.Dir(), "OpenCore-v21.qcow2")); err != nil {
		t.Skip("no cached OpenCore image")
	}
	work := t.TempDir()
	img, err := PrepareOpenCore(context.Background(), cache.Dir(), work, "", slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(work, "OpenCore.qcow2"), img, "patched copy lives in the work dir")

	cfg, err := qemu.ReadFileFromFATQcow2(img, "/EFI/OC/config.plist")
	require.NoError(t, err)
	require.Contains(t, string(cfg), "<key>ShowPicker</key>\n\t\t\t<false/>")
	require.Contains(t, string(cfg), "-v serial=1")
}
