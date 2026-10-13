package build

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/devcell-sh/go-mackit/build/buildopts"
)

// sshCreds is the guest account hooks run as: the one a Full build
// creates in HeadlessSetup (buildopts.BuildOpts.User, Password).
type sshCreds struct {
	User     string
	Password string
}

// creds returns the build's guest account, defaults applied.
func (c Config) creds() sshCreds {
	cr := sshCreds{User: buildopts.DefaultUser, Password: buildopts.DefaultPassword}
	if c.Opts != nil {
		if c.Opts.User != "" {
			cr.User = c.Opts.User
		}
		if c.Opts.Password != "" {
			cr.Password = c.Opts.Password
		}
	}
	return cr
}

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

// checkSSHBanner reports whether an SSH server answers at addr. One read
// is enough: the banner is the first thing the server sends, before any
// client data, so it arrives in the first packet.
func checkSSHBanner(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	return strings.HasPrefix(string(buf[:n]), "SSH-")
}

func runHooks(ctx context.Context, hooks []buildopts.Hook, phase buildopts.HookPhase, sshAddr string, creds sshCreds, logger *slog.Logger) error {
	for _, h := range hooks {
		if h.Phase != phase {
			continue
		}
		label := h.Label
		if label == "" {
			label = h.Cmd
		}
		logger.Info("running hook", "phase", phase, "cmd", label)

		if err := runSSHCommand(ctx, sshAddr, creds, h.Cmd, h.Timeout); err != nil {
			if isExitOK(err, h.ValidExitCodes) {
				logger.Info("hook exited with accepted code", "cmd", label)
				continue
			}
			if h.Retries > 0 {
				var retryErr error
				for r := 0; r < h.Retries; r++ {
					logger.Info("retrying hook", "cmd", label, "attempt", r+2)
					retryErr = runSSHCommand(ctx, sshAddr, creds, h.Cmd, h.Timeout)
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

// runSSHCommand runs command on the guest over SSH with password
// authentication, as the build's user. Errors from the command carry its
// exit status (see isExitOK) and the trimmed combined output.
func runSSHCommand(ctx context.Context, addr string, creds sshCreds, command string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := &ssh.ClientConfig{
		User:            creds.User,
		Auth:            []ssh.AuthMethod{ssh.Password(creds.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // a throwaway build VM on localhost
		Timeout:         30 * time.Second,
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh %s@%s: %w", creds.User, addr, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			client.Close()
		case <-done:
		}
	}()
	out, err := sess.CombinedOutput(command)
	close(done)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %s", ctx.Err(), strings.TrimSpace(string(out)))
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isExitOK(err error, validCodes []int) bool {
	if err == nil {
		return true
	}
	if len(validCodes) == 0 {
		return false
	}
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	code := exitErr.ExitStatus()
	for _, c := range validCodes {
		if c == code {
			return true
		}
	}
	return false
}
