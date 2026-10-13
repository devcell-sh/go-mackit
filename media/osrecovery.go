package media

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// recoveryBoardIDs maps macOS version codenames to Apple board identifiers
// used by the osrecovery.apple.com endpoint. These return a small (~500-800 MB)
// BaseSystem.dmg that boots macOS Recovery, from which the full installer
// downloads during "Reinstall macOS".
var recoveryBoardIDs = map[string]string{
	"sequoia":    "Mac-7BA5B2D9E42DDD94",
	"sonoma":     "Mac-827FAC58A8FDFA22",
	"ventura":    "Mac-4B682C642B45593E",
	"monterey":   "Mac-B809C3757DA9BB8D",
	"bigsur":     "Mac-2BD1B31983FE1663",
	"catalina":   "Mac-00BE6ED71E35EB86",
	"mojave":     "Mac-7BA5B2DFE22DDD8C",
	"highsierra": "Mac-7BA5B2D9E42DDD94",
}

// FetchRecoveryImage downloads a BaseSystem.dmg from Apple's Internet
// Recovery endpoint (osrecovery.apple.com). This works for all macOS
// versions and returns a standard UDIF DMG (~500-800 MB) that boots
// Recovery in QEMU. For macOS 11+ this is the only practical way to get
// a small bootable image without the 15 GB InstallAssistant.pkg.
func FetchRecoveryImage(ctx context.Context, version, dest string, logger *slog.Logger) error {
	bid, ok := recoveryBoardIDs[version]
	if !ok {
		return fmt.Errorf("no recovery board ID for macOS %s", version)
	}

	cid := randHex(16)
	k := randHex(64)
	fg := randHex(64)

	body := fmt.Sprintf("cid=%s\nsn=00000000000000000\nbid=%s\nk=%s\nfg=%s", cid, bid, k, fg)

	// Step 1: POST to get session cookie (returns 401).
	session, err := recoverySession(ctx, body)
	if err != nil {
		return fmt.Errorf("osrecovery session: %w", err)
	}

	// Step 2: POST with session cookie to get download URL and auth token.
	dmgURL, authToken, err := recoveryURL(ctx, body, session)
	if err != nil {
		return fmt.Errorf("osrecovery URL: %w", err)
	}

	logger.Info("downloading recovery BaseSystem.dmg", "url", dmgURL)
	return downloadFileWithCookie(ctx, dmgURL, "AssetToken="+authToken, dest, logger)
}

func recoverySession(ctx context.Context, body string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://osrecovery.apple.com/InstallationPayload/RecoveryImage",
		strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Host", "osrecovery.apple.com")
	req.Header.Set("User-Agent", "InternetRecovery/1.0")
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Connection", "close")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	cookie := resp.Header.Get("Set-Cookie")
	if cookie == "" {
		return "", fmt.Errorf("no session cookie in response (status %d)", resp.StatusCode)
	}
	if i := strings.IndexByte(cookie, ';'); i > 0 {
		cookie = cookie[:i]
	}
	return cookie, nil
}

func recoveryURL(ctx context.Context, body, session string) (dmgURL, authToken string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://osrecovery.apple.com/InstallationPayload/RecoveryImage",
		strings.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Host", "osrecovery.apple.com")
	req.Header.Set("User-Agent", "InternetRecovery/1.0")
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Connection", "close")
	req.Header.Set("Cookie", session)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status %d", resp.StatusCode)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "AU: ") {
			dmgURL = strings.TrimSpace(line[4:])
		}
		if strings.HasPrefix(line, "AT: ") {
			authToken = strings.TrimSpace(line[4:])
		}
	}
	if dmgURL == "" {
		return "", "", fmt.Errorf("no AU (download URL) in response")
	}
	return dmgURL, authToken, nil
}

func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	rand.Read(b)
	s := fmt.Sprintf("%X", b)
	return s[:n]
}
