package vz

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devcell-sh/go-mackit/vm"
	"github.com/devcell-sh/go-mackit/vm/vz/vzproto"
)

// HelperName is the name of the Swift helper binary.
const HelperName = "mackit-vz"

// GetVNCPort returns the VNC port from a VM handle, or 0 if not available.
func GetVNCPort(v vm.VM) int {
	type vncer interface{ VNCPort() int }
	if h, ok := v.(vncer); ok {
		return h.VNCPort()
	}
	return 0
}

// GetMACAddress returns the guest MAC address from a VM handle, or "" if not available.
func GetMACAddress(v vm.VM) string {
	type macer interface{ MACAddress() string }
	if h, ok := v.(macer); ok {
		return h.MACAddress()
	}
	return ""
}

// HelperBackend implements vm.VMBackend by delegating to the mackit-vz helper.
type HelperBackend struct {
	Logger     *slog.Logger
	HelperPath string
}

func (b *HelperBackend) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func (b *HelperBackend) helperPath() string {
	if b.HelperPath != "" {
		return b.HelperPath
	}
	return HelperName
}

func (b *HelperBackend) DefaultDiskFormat() vm.DiskFormat { return vm.DiskFormatRaw }

func (b *HelperBackend) CreateDisk(path string, sizeGB int, format vm.DiskFormat) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(sizeGB) * 1024 * 1024 * 1024); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (b *HelperBackend) CreateOverlay(path, base string) error {
	return fmt.Errorf("vz backend does not support overlays; use raw disks")
}

func (b *HelperBackend) Flatten(src, dst string) error {
	return copyFile(src, dst)
}

func (b *HelperBackend) StartInstall(ctx context.Context, cfg vm.VMInstallConfig) (vm.VM, error) {
	h, err := b.startHelper(ctx)
	if err != nil {
		return nil, err
	}

	req := &vzproto.Request{
		Cmd:        vzproto.CmdInstall,
		IPSW:       cfg.InstallerDMG,
		Disk:       cfg.DiskPath,
		DiskSizeGB: cfg.DiskSizeGB,
		CPUs:       cfg.CPUs,
		MemoryGB:   cfg.MemoryGB,
		OutDir:     cfg.OutputDir,
	}

	h.logger.Debug("starting install", "ipsw", cfg.InstallerDMG, "disk", cfg.DiskPath, "cpus", cfg.CPUs, "memory_gb", cfg.MemoryGB)

	go func() {
		defer h.markDone()
		resp, err := h.sendAndWait(req)
		if err != nil {
			h.logger.Debug("install sendAndWait failed", "error", err)
			h.setErr(err)
			return
		}
		if err := resp.Err(); err != nil {
			h.logger.Debug("install response error", "error", err)
			h.setErr(err)
			return
		}
		h.logger.Debug("install completed")
	}()

	return h, nil
}

func (b *HelperBackend) StartRun(ctx context.Context, cfg vm.VMRunConfig) (vm.VM, error) {
	h, err := b.startHelper(ctx)
	if err != nil {
		return nil, err
	}

	req := &vzproto.Request{
		Cmd:           vzproto.CmdBoot,
		Disk:          cfg.DiskPath,
		CPUs:          cfg.CPUs,
		MemoryGB:      cfg.MemoryGB,
		OutDir:        cfg.OutputDir,
		GuestUser:     cfg.GuestUser,
		GuestPassword: cfg.GuestPassword,
		EnableSSH:     cfg.EnableSSH,
	}

	h.logger.Debug("sending boot request", "disk", cfg.DiskPath, "cpus", cfg.CPUs, "memory_gb", cfg.MemoryGB)
	resp, err := h.sendAndWait(req)
	if err != nil {
		h.logger.Debug("boot sendAndWait failed", "error", err)
		return nil, fmt.Errorf("boot: %w", err)
	}
	if err := resp.Err(); err != nil {
		h.logger.Debug("boot response error", "error", err)
		return nil, fmt.Errorf("boot: %w", err)
	}
	h.setVNCPort(resp.VNCPort)
	h.setMACAddress(resp.MACAddress)
	h.logger.Debug("boot response received", "vnc_port", resp.VNCPort, "mac_address", resp.MACAddress)

	go func() {
		defer h.markDone()
		h.logger.Debug("waiting for helper process to exit", "pid", h.cmd.Process.Pid)
		h.waitOnce.Do(func() { h.cmd.Wait() })
		h.logger.Debug("helper process exited", "pid", h.cmd.Process.Pid)
	}()

	return h, nil
}

func (b *HelperBackend) startHelper(ctx context.Context) (*helperHandle, error) {
	path := b.helperPath()
	logger := b.log()
	logger.Debug("starting helper", "path", path)

	cmd := exec.CommandContext(ctx, path)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("helper stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", path, err)
	}
	logger.Debug("helper started", "pid", cmd.Process.Pid)

	h := &helperHandle{
		cmd:      cmd,
		stdin:    stdin,
		scanner:  bufio.NewScanner(stdout),
		logger:   logger,
		doneCh:   make(chan struct{}),
		eventsCh: make(chan *vzproto.Message, 64),
	}
	return h, nil
}

// helperHandle wraps a running helper process.
type helperHandle struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	logger  *slog.Logger

	mu         sync.Mutex
	nextID     atomic.Int32
	doneCh     chan struct{}
	err        error
	vncPort    int
	macAddress string

	eventsCh        chan *vzproto.Message
	lastLogProgress int
}

func (h *helperHandle) Stop() error {
	h.logger.Debug("stopping helper", "pid", h.cmd.Process.Pid)
	// Always send the stop command so the helper can shut down the VM
	// gracefully (releases the auxiliary storage lock). If doneCh is
	// already closed the install RPC finished but the helper process
	// (and its VM) is still alive.
	_, err := h.send(&vzproto.Request{Cmd: vzproto.CmdStop})
	if err != nil {
		h.logger.Debug("stop command send failed", "error", err)
	}

	// Give the helper time to exit cleanly; fall back to kill.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	exited := make(chan struct{})
	go func() {
		h.waitOnce.Do(func() { h.cmd.Wait() })
		close(exited)
	}()
	select {
	case <-exited:
		h.logger.Debug("helper exited gracefully", "pid", h.cmd.Process.Pid)
	case <-timer.C:
		h.logger.Debug("helper did not exit in 5s, killing", "pid", h.cmd.Process.Pid)
		if h.cmd.Process != nil {
			h.cmd.Process.Kill()
		}
		<-exited
		h.logger.Debug("helper killed", "pid", h.cmd.Process.Pid)
	}
	return nil
}

func (h *helperHandle) Wait() error {
	<-h.doneCh
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *helperHandle) SSHAddr() string       { return "" }
func (h *helperHandle) OutputDir() string      { return "" }
func (h *helperHandle) PID() int               { return h.cmd.Process.Pid }
func (h *helperHandle) Done() <-chan struct{}  { return h.doneCh }
func (h *helperHandle) VNCPort() int           { h.mu.Lock(); defer h.mu.Unlock(); return h.vncPort }
func (h *helperHandle) MACAddress() string     { h.mu.Lock(); defer h.mu.Unlock(); return h.macAddress }

func (h *helperHandle) markDone() {
	select {
	case <-h.doneCh:
	default:
		h.logger.Debug("helper marked done")
		close(h.doneCh)
	}
}

func (h *helperHandle) setErr(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}

func (h *helperHandle) setVNCPort(port int) {
	h.mu.Lock()
	h.vncPort = port
	h.mu.Unlock()
}

func (h *helperHandle) setMACAddress(mac string) {
	h.mu.Lock()
	h.macAddress = mac
	h.mu.Unlock()
}

// send writes a request and returns its ID.
func (h *helperHandle) send(req *vzproto.Request) (int, error) {
	id := int(h.nextID.Add(1))
	req.ID = id
	data, err := vzproto.MarshalRequest(req)
	if err != nil {
		return 0, err
	}
	h.logger.Debug("sending request", "id", id, "cmd", req.Cmd, "bytes", len(data))
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.stdin.Write(append(data, '\n')); err != nil {
		h.logger.Debug("send failed", "id", id, "cmd", req.Cmd, "error", err)
		return 0, fmt.Errorf("writing to helper: %w", err)
	}
	return id, nil
}

// sendAndWait sends a request and blocks until the matching response arrives,
// forwarding events to the logger.
func (h *helperHandle) sendAndWait(req *vzproto.Request) (*vzproto.Message, error) {
	id, err := h.send(req)
	if err != nil {
		return nil, err
	}
	h.logger.Debug("waiting for response", "id", id, "cmd", req.Cmd)
	for h.scanner.Scan() {
		raw := h.scanner.Bytes()
		msg, err := vzproto.UnmarshalMessage(raw)
		if err != nil {
			h.logger.Warn("helper: bad JSON", "line", h.scanner.Text(), "error", err)
			continue
		}
		if msg.IsEvent() {
			h.logger.Debug("received event", "event", msg.Event, "waiting_for", id)
			h.handleEvent(msg)
			continue
		}
		if msg.ID == id {
			h.logger.Debug("received response", "id", id, "ok", msg.OK, "error", msg.Error)
			return msg, nil
		}
		h.logger.Debug("received response for different request", "got_id", msg.ID, "waiting_for", id)
	}
	if err := h.scanner.Err(); err != nil {
		h.logger.Debug("scanner error", "error", err, "waiting_for", id)
		return nil, fmt.Errorf("reading helper stdout: %w", err)
	}
	h.logger.Debug("helper stdout closed", "waiting_for", id)
	return nil, fmt.Errorf("helper closed stdout before responding to request %d", id)
}

func (h *helperHandle) handleEvent(msg *vzproto.Message) {
	switch msg.Event {
	case vzproto.EventProgress:
		pct := msg.Progress
		if pct == 0 || pct == 100 || pct-h.lastLogProgress >= 5 {
			h.lastLogProgress = pct
			h.logger.Info(msg.Msg, "progress", pct, "uom", msg.UOM)
			fmt.Fprintf(os.Stderr, "%s progress=%d uom=%s\n", msg.Msg, pct, msg.UOM)
		}
	case vzproto.EventBooted:
		h.setVNCPort(msg.VNCPort)
		h.logger.Info("VM booted", "vnc_port", msg.VNCPort)
	case vzproto.EventStopped:
		h.logger.Info("VM stopped", "exit_code", msg.ExitCode)
	case vzproto.EventLog:
		h.logger.Info(msg.Msg, "level", msg.Level)
	default:
		h.logger.Debug("helper event", "event", msg.Event, "msg", msg.Msg)
	}

	select {
	case h.eventsCh <- msg:
	default:
	}
}

// Screenshot sends a screenshot request and returns the decoded image.
func (h *helperHandle) Screenshot(ctx context.Context) (image.Image, error) {
	req := &vzproto.Request{Cmd: vzproto.CmdScreenshot}
	resp, err := h.sendAndWait(req)
	if err != nil {
		return nil, err
	}
	if err := resp.Err(); err != nil {
		return nil, err
	}
	if resp.PNGBase64 == "" {
		return nil, fmt.Errorf("screenshot response has no image data")
	}
	decoded, err := base64.StdEncoding.DecodeString(resp.PNGBase64)
	if err != nil {
		return nil, fmt.Errorf("decoding screenshot: %w", err)
	}
	return png.Decode(strings.NewReader(string(decoded)))
}

// SendKeys sends a key chord to the VM.
func (h *helperHandle) SendKeys(ctx context.Context, chord ...string) error {
	req := &vzproto.Request{Cmd: vzproto.CmdSendKeys, Keys: chord}
	resp, err := h.sendAndWait(req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// Click sends a click at (x, y).
func (h *helperHandle) Click(ctx context.Context, pt image.Point, display image.Rectangle) error {
	req := &vzproto.Request{Cmd: vzproto.CmdClick, X: pt.X, Y: pt.Y}
	resp, err := h.sendAndWait(req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// Type sends text as key presses.
func (h *helperHandle) Type(ctx context.Context, text string) error {
	req := &vzproto.Request{Cmd: vzproto.CmdType, Text: text}
	resp, err := h.sendAndWait(req)
	if err != nil {
		return err
	}
	return resp.Err()
}

