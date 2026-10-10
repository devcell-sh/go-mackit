package installer

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DefaultOpenCoreVersion = "v21"
	openCoreReleaseURLFmt  = "https://github.com/thenickdude/KVM-Opencore/releases/download/%s/OpenCore-%s.iso.gz"
	openCoreFallbackURL    = "https://github.com/kholia/OSX-KVM/raw/master/OpenCore/OpenCore.qcow2"
)

type OpenCoreConfig struct {
	Version  string
	Arch     string
	CacheDir string
	NoCache  bool
	Logger   *slog.Logger
}

func (c OpenCoreConfig) version() string {
	if c.Version != "" {
		return c.Version
	}
	return DefaultOpenCoreVersion
}

func EnsureOpenCore(ctx context.Context, cfg OpenCoreConfig) (string, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	arch := cfg.Arch
	if arch == "" {
		arch = runtime.GOARCH
		if arch == "amd64" {
			arch = "x86_64"
		}
	}

	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache", "mackit")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	ver := cfg.version()
	imgPath := filepath.Join(cacheDir, fmt.Sprintf("OpenCore-%s.qcow2", ver))

	if !cfg.NoCache {
		if _, err := os.Stat(imgPath); err == nil {
			logger.Info("using cached OpenCore image", "path", imgPath, "version", ver)
			return imgPath, nil
		}
		// Check legacy paths.
		for _, legacy := range []string{
			filepath.Join(cacheDir, fmt.Sprintf("OpenCore-%s.qcow2", arch)),
			filepath.Join(cacheDir, "OpenCore.qcow2"),
		} {
			if _, err := os.Stat(legacy); err == nil {
				logger.Info("using cached OpenCore image", "path", legacy)
				return legacy, nil
			}
		}
	}

	logger.Info("downloading OpenCore EFI image", "version", ver)

	if err := downloadOpenCoreRelease(ctx, ver, imgPath, logger); err != nil {
		logger.Info("versioned release failed, trying fallback", "error", err)
		if err := downloadOpenCoreFallback(ctx, imgPath, logger); err != nil {
			return "", fmt.Errorf("downloading OpenCore: %w", err)
		}
	}

	logger.Info("OpenCore ready", "path", imgPath)
	return imgPath, nil
}

func downloadOpenCoreRelease(ctx context.Context, version, destQcow2 string, logger *slog.Logger) error {
	url := fmt.Sprintf(openCoreReleaseURLFmt, version, version)
	logger.Info("fetching OpenCore release", "url", url)

	gzPath := destQcow2 + ".iso.gz"
	defer os.Remove(gzPath)

	if err := httpDownload(ctx, url, gzPath); err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}

	rawPath := destQcow2 + ".raw"
	defer os.Remove(rawPath)

	logger.Info("decompressing OpenCore image")
	if err := gunzipFile(gzPath, rawPath); err != nil {
		return fmt.Errorf("decompressing: %w", err)
	}

	logger.Info("converting to qcow2")
	if err := rawToQcow2(rawPath, destQcow2); err != nil {
		return fmt.Errorf("converting to qcow2: %w", err)
	}

	return nil
}

func downloadOpenCoreFallback(ctx context.Context, destQcow2 string, logger *slog.Logger) error {
	logger.Info("fetching OpenCore from OSX-KVM", "url", openCoreFallbackURL)
	return httpDownload(ctx, openCoreFallbackURL, destQcow2)
}

func httpDownload(ctx context.Context, url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

func gunzipFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, gz); err != nil {
		return err
	}
	return out.Close()
}

func rawToQcow2(rawPath, qcow2Path string) error {
	cmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawPath, qcow2Path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img convert: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
