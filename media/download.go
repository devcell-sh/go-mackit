package media

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func downloadFile(ctx context.Context, url, dest string, logger *slog.Logger) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	tmp := dest + ".tmp"
	defer os.Remove(tmp)

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

	totalBytes, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer out.Close()

	var written int64
	buf := make([]byte, 256*1024)
	lastPct := -1
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := out.Write(buf[:n]); wErr != nil {
				return wErr
			}
			written += int64(n)
			if totalBytes > 0 && logger != nil {
				pct := int(written * 100 / totalBytes)
				if pct/10 > lastPct/10 {
					lastPct = pct
					logger.Info("downloading", "progress", fmt.Sprintf("%d%%", pct), "bytes", written, "total", totalBytes)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
