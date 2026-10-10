package build

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/devcell-sh/go-mackit/build/buildopts"
)

func waitForSSH(ctx context.Context, addr string, timeout time.Duration, logger *slog.Logger) error {
	logger.Info("waiting for SSH", "addr", addr, "timeout", timeout)
	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("SSH did not become available at %s within %v", addr, timeout)
		case <-ticker.C:
			if checkSSHBanner(addr) {
				logger.Info("SSH is reachable", "addr", addr)
				return nil
			}
		}
	}
}

func checkSSHBanner(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	return strings.HasPrefix(string(buf[:n]), "SSH-")
}

func runHooks(ctx context.Context, hooks []buildopts.Hook, phase buildopts.HookPhase, sshAddr string, logger *slog.Logger) error {
	for _, h := range hooks {
		if h.Phase != phase {
			continue
		}
		label := h.Label
		if label == "" {
			label = h.Cmd
		}
		logger.Info("running hook", "phase", phase, "cmd", label)

		if err := runSSHCommand(ctx, sshAddr, h.Cmd, h.Timeout); err != nil {
			if isExitOK(err, h.ValidExitCodes) {
				logger.Info("hook exited with accepted code", "cmd", label)
				continue
			}
			if h.Retries > 0 {
				var retryErr error
				for r := 0; r < h.Retries; r++ {
					logger.Info("retrying hook", "cmd", label, "attempt", r+2)
					retryErr = runSSHCommand(ctx, sshAddr, h.Cmd, h.Timeout)
					if retryErr == nil || isExitOK(retryErr, h.ValidExitCodes) {
						retryErr = nil
						break
					}
				}
				if retryErr != nil {
					return fmt.Errorf("hook %q failed after %d retries: %w", label, h.Retries, retryErr)
				}
				continue
			}
			return fmt.Errorf("hook %q: %w", label, err)
		}
	}
	return nil
}

func runSSHCommand(ctx context.Context, addr, command string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = "127.0.0.1"
		port = "20022"
	}

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-p", port,
		fmt.Sprintf("root@%s", host),
		command,
	}

	cmd := exec.CommandContext(ctx, "ssh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isExitOK(err error, validCodes []int) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if !isExitError(err, &exitErr) {
		return false
	}
	code := exitErr.ExitCode()
	for _, c := range validCodes {
		if code == c {
			return true
		}
	}
	return false
}

func isExitError(err error, target **exec.ExitError) bool {
	for err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}
