package build

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-mackit/build/buildopts"
	"github.com/devcell-sh/go-mackit/installer"
	"github.com/devcell-sh/go-mackit/installer/flow"
	"github.com/devcell-sh/go-mackit/internal/logx"
	"github.com/devcell-sh/go-mackit/media"
	"github.com/devcell-sh/go-mackit/vm"
	"github.com/devcell-sh/go-mackit/vm/qemu"
)

// fullDiskGB is the target disk size of a Full build. The install flow
// finds the disk by this size in `diskutil list`.
const fullDiskGB = 64

type Config struct {
	Dest         string
	CacheDir     string
	InstallerDMG string
	OpenCoreImg  string
	WorkDir      string
	Logger       *slog.Logger
	NoCache      bool
	Accel        string
	DisplayType  string
	GuestArch    string
	Opts         *buildopts.BuildOpts
	// RecoverySteps run on the macOS Recovery UI once the Utilities screen
	// is showing (Recovery mode only). They need OCR; see installer/flow.
	RecoverySteps []flow.Step
	// RecoveryTimeout bounds the wait for the macOS Utilities screen
	// (default 30m; TCG boots take 15 to 20 minutes).
	RecoveryTimeout time.Duration
	// StopAfterStep names an install flow step (Full builds; see
	// flow.InstallStepsFor) after which the build stops driving the UI and
	// idles with the VM running until the context ends, instead of going
	// on to the next step and then waiting for SSH. It is how the flow is
	// verified one step at a time against a live VM: run to a step, look
	// at the screen over VNC and the saved OCR readings, add the next step.
	StopAfterStep string
	// KeepVM leaves the VM running when the build returns (after a
	// StopAfterStep idle, an error, or a cancelled context) instead of
	// stopping it, so it can be inspected over VNC. The disk image is
	// then not flattened to Dest.
	KeepVM bool
	// MemoryGB overrides the VM memory (default 8 for Full, 4 for
	// Recovery). The installer touches all of it while it downloads, so
	// in a memory-capped container (cgroup limit) keep it a gigabyte or
	// so under the cap or the kernel kills QEMU without a message.
	MemoryGB int
}

// memoryGB is the VM memory to use given the build's default.
func (c Config) memoryGB(def uint64) uint64 {
	if c.MemoryGB > 0 {
		return uint64(c.MemoryGB)
	}
	return def
}

// logger returns the build logger. Every record gets service=mackit
// unless it already names its source (serial-port lines carry the guest
// component, textshot carries its own), so build.jsonl lines are
// attributable.
func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return logx.Logger(c.Logger, logx.ServiceMackit)
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fullBuild carries what the phases of a Full build share: the media, the
// patched OpenCore image, the work dir and target disk, the ports, and the
// QEMU settings every VM of the build starts with. A Full build runs three
// VMs in turn in the same work dir: the installer (Recovery, then the
// installer's reboots until the installed system boots), Recovery again
// for flow.HeadlessSetup (user, Setup Assistant skip, Remote Login), and
// the installed system alone for SSH, hooks, and shutdown.
type fullBuild struct {
	Config
	logger   *slog.Logger
	dmgPath  string
	ocImg    string
	workDir  string
	diskPath string
	shotDir  string
	sshPort  uint16
	vncPort  uint16
	accel    string
	display  string
	nic      string
	vga      string
	version  string
	creds    sshCreds
}

func defaultGuestArch() string {
	return "x86_64"
}

// installState is how the install phase ended.
type installState int

const (
	installContinue installState = iota // installed system booted: on to the headless setup
	installIdle                         // StopAfterStep reached: the build holds the VM
	installDone                         // no screen reading: the user finished over VNC, hooks ran
)

func Full(ctx context.Context, c Config) error {
	logger := c.logger()

	if c.Opts != nil {
		if err := c.Opts.Validate(); err != nil {
			return err
		}
	}

	if c.GuestArch == "" {
		c.GuestArch = defaultGuestArch()
	}

	dmgPath, err := resolveMedia(ctx, c, logger)
	if err != nil {
		return err
	}

	workDir := c.WorkDir
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(c.Dest), ".mackit-build")
	}
	ocImg, err := prepareOpenCore(ctx, c, workDir, logger)
	if err != nil {
		return err
	}

	logger.Info("starting full macOS install", "dest", c.Dest)

	diskPath := filepath.Join(workDir, "mackit-install.qcow2")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		logger.Info("creating target disk", "path", diskPath, "size", "64G")
		if err := qemu.CreateDisk(diskPath, fullDiskGB); err != nil {
			return fmt.Errorf("creating disk: %w", err)
		}
	}

	sshPort := buildopts.DefaultSSHPort
	vncPort := buildopts.DefaultVNCPort
	if c.Opts != nil {
		sshPort = c.Opts.Ports.SSHOrDefault()
		vncPort = c.Opts.Ports.VNCOrDefault()
	}
	if sshPort == 0 {
		sshPort = qemu.FreePort()
	}
	if vncPort == 0 {
		vncPort = qemu.FreePort()
	}

	accel := c.Accel
	if accel == "" {
		accel = qemu.DefaultAccelForArch(c.GuestArch)
	}

	display := c.DisplayType
	if display == "" {
		display = fmt.Sprintf("vnc=:%d", vncPort-5900)
	}

	version := ""
	if c.Opts != nil && c.Opts.From != "" {
		if v, _, err := media.ParseFrom(c.Opts.From); err == nil {
			version = v
		}
	}

	b := &fullBuild{
		Config:   c,
		logger:   logger,
		dmgPath:  dmgPath,
		ocImg:    ocImg,
		workDir:  workDir,
		diskPath: diskPath,
		shotDir:  filepath.Join(workDir, "screenshots"),
		sshPort:  sshPort,
		vncPort:  vncPort,
		accel:    accel,
		display:  display,
		nic:      GuestNIC(version),
		vga:      GuestVGA(version),
		version:  version,
		creds:    c.creds(),
	}
	os.MkdirAll(b.shotDir, 0o755)

	if c.StopAfterStep != "" {
		if err := b.checkStopStep(); err != nil {
			return err
		}
	}

	state, err := b.install(ctx)
	if err != nil || state != installContinue {
		if err == nil && state == installDone {
			return b.flatten()
		}
		return err
	}
	if !flow.HasSetupAssistant(b.version) {
		idle, err := b.headlessSetup(ctx)
		if err != nil || idle {
			return err
		}
	}
	if err := b.provision(ctx); err != nil {
		return err
	}
	return b.flatten()
}

// checkStopStep rejects a StopAfterStep that names no step of the install
// or headless setup flow, before any VM starts.
func (b *fullBuild) checkStopStep() error {
	install, err := flow.InstallStepsFor(b.version, fullDiskGB)
	if err != nil {
		return err
	}
	if hasStep(install, b.StopAfterStep) {
		return nil
	}
	if !flow.HasSetupAssistant(b.version) {
		headless, err := flow.HeadlessSetup(b.creds.User, b.creds.Password)
		if err != nil {
			return err
		}
		if hasStep(headless, b.StopAfterStep) {
			return nil
		}
	}
	return fmt.Errorf("StopAfterStep %q names no step of the %s install or headless setup flow", b.StopAfterStep, b.version)
}

func hasStep(steps []flow.Step, name string) bool {
	if name == "" {
		return false
	}
	for _, s := range steps {
		if s.Name == name {
			return true
		}
	}
	return false
}

func (b *fullBuild) installConfig() qemu.InstallConfig {
	return qemu.InstallConfig{
		InstallerDMG: b.dmgPath,
		OpenCoreImg:  b.ocImg,
		DiskPath:     b.diskPath,
		GuestArch:    b.GuestArch,
		CPUs:         4,
		MemoryGB:     b.memoryGB(8),
		SSHPort:      b.sshPort,
		VNCPort:      b.vncPort,
		Accel:        b.accel,
		DisplayType:  b.display,
		NIC:          b.nic,
		VGA:          b.vga,
		OutputDir:    b.workDir,
	}
}

func (b *fullBuild) runConfig() qemu.RunConfig {
	return qemu.RunConfig{
		DiskPath:     b.diskPath,
		OpenCoreImg:  b.ocImg,
		GuestArch:    b.GuestArch,
		CPUs:         4,
		MemoryGB:     b.memoryGB(8),
		SSHPort:      b.sshPort,
		SSHGuestPort: 22,
		VNCPort:      b.vncPort,
		Accel:        b.accel,
		DisplayType:  b.display,
		NIC:          b.nic,
		VGA:          b.vga,
		OutputDir:    b.workDir,
		VMName:       "mackit-install",
	}
}

// session is one VM of the build with its serial forwarding and periodic
// screenshots.
type session struct {
	b       *fullBuild
	machine *qemu.InstallVM
	stop    func()
}

// start launches the installer VM (install media attached, which boots
// Recovery) or the installed system alone, and starts forwarding its
// serial log and capturing its screen.
func (b *fullBuild) start(ctx context.Context, withMedia bool) (*session, error) {
	// With KeepVM the VM must outlive this call and its context: QEMU is
	// started from a context that is not cancelled with the build.
	vmCtx := ctx
	if b.KeepVM {
		vmCtx = context.WithoutCancel(ctx)
	}
	var machine *qemu.InstallVM
	var err error
	if withMedia {
		machine, err = qemu.StartInstall(vmCtx, b.installConfig())
	} else {
		machine, err = qemu.StartRun(vmCtx, b.runConfig())
	}
	if err != nil {
		return nil, err
	}
	stopSerial := forwardSerial(ctx, b.workDir, b.logger)
	shotStop := make(chan struct{})
	shotDone := make(chan struct{})
	go qemu.CaptureScreenshots(
		machine.QMPSocket(), b.shotDir, 30*time.Second,
		shotStop, shotDone,
		func(f string, a ...any) { b.logger.Debug(fmt.Sprintf(f, a...), "component", "screenshot") },
	)
	b.logger.Info("macOS VM running",
		"pid", machine.PID(),
		"install_media", withMedia,
		"ssh", b.sshAddr(),
		"vnc", fmt.Sprintf("127.0.0.1:%d", b.vncPort),
		"screenshots", b.shotDir,
		"serial_log", filepath.Join(b.workDir, "serial.log"),
	)
	return &session{b: b, machine: machine, stop: func() {
		close(shotStop)
		<-shotDone
		stopSerial()
	}}, nil
}

// finish ends the session. The VM is stopped unless the build ends here
// (an idle hold, an error) and KeepVM asks for it to be left running; the
// next phase needs the ports and the disk, so a phase that goes on always
// stops its VM.
func (s *session) finish(last bool) {
	s.stop()
	if last {
		s.b.stopVM(s.machine, s.b.vncPort, s.b.logger)
		return
	}
	s.machine.Stop()
}

// watch cancels the returned context when the VM exits, so a wait on the
// screen or on SSH ends with the VM instead of running to its timeout.
func (s *session) watch(ctx context.Context) (context.Context, context.CancelFunc) {
	wctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-s.machine.Done():
			cancel()
		case <-wctx.Done():
		}
	}()
	return wctx, cancel
}

// exited reports whether the VM is gone.
func (s *session) exited() bool {
	select {
	case <-s.machine.Done():
		return true
	default:
		return false
	}
}

// hold keeps the VM up for inspection until the build's context ends.
func (s *session) hold(ctx context.Context, step string) error {
	s.b.logger.Info("install flow stopped, VM idle for inspection",
		"step", step,
		"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", s.b.vncPort),
		"screenshots", s.b.shotDir,
		"ocr", filepath.Join(s.b.workDir, "ocr"))
	select {
	case <-ctx.Done():
		return nil
	case <-s.machine.Done():
		return fmt.Errorf("VM exited while idle")
	}
}

func (b *fullBuild) sshAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", b.sshPort)
}

func (b *fullBuild) recoveryTimeout() time.Duration {
	if b.RecoveryTimeout > 0 {
		return b.RecoveryTimeout
	}
	return 30 * time.Minute
}

// install is the first phase: boot the installer VM to Recovery and drive
// the unattended install (erase, Reinstall macOS, the installer's reboots)
// until the installed system boots. Without screen reading it answers the
// picker blindly and waits for the user to finish over VNC.
func (b *fullBuild) install(ctx context.Context) (state installState, err error) {
	b.logger.Info("launching QEMU installer",
		"dmg", b.dmgPath,
		"opencore", b.ocImg,
		"accel", b.accel,
		"ssh_port", b.sshPort,
		"vnc_port", b.vncPort,
		"guest_arch", b.GuestArch,
		"nic", b.nic,
	)
	s, err := b.start(ctx, true)
	if err != nil {
		return 0, fmt.Errorf("starting installer VM: %w", err)
	}
	defer func() { s.finish(err != nil || state != installContinue) }()

	driver := newScreenDriver(ctx, b.Config, s.machine.QMPSocket(), b.shotDir, b.logger)
	defer driver.Close()
	if driver == nil {
		go autoSelectPicker(s.machine.QMPSocket(), b.logger)
		return installDone, b.manualInstall(ctx, s)
	}

	stopAfter := ""
	if steps, err := flow.InstallStepsFor(b.version, fullDiskGB); err == nil && hasStep(steps, b.StopAfterStep) {
		stopAfter = b.StopAfterStep
	}
	driveCtx, cancelDrive := s.watch(ctx)
	err = driver.driveInstall(driveCtx, s.machine.QMPSocket(), fullDiskGB, b.recoveryTimeout(), stopAfter)
	cancelDrive()
	driver.Close() // the screen is not read past this point; free the models
	switch {
	case err == nil && stopAfter != "":
		return installIdle, s.hold(ctx, stopAfter)
	case err == nil:
		b.logger.Info("macOS installed, installed system booted",
			"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", b.vncPort))
		return installContinue, nil
	case ctx.Err() != nil:
		return 0, fmt.Errorf("installing macOS: %w", err)
	case s.exited():
		return 0, fmt.Errorf("VM exited during the macOS install (out of memory? see qemu.log): %w", err)
	default:
		b.logger.Warn("unattended install not completed, continue over VNC",
			"error", err, "vnc", fmt.Sprintf("vnc://127.0.0.1:%d", b.vncPort))
		return installDone, b.manualInstall(ctx, s)
	}
}

// manualInstall is the fallback when the screen cannot be driven: the user
// completes the install and Setup Assistant over VNC, creates the build's
// user and enables Remote Login; the build goes on once SSH answers.
func (b *fullBuild) manualInstall(ctx context.Context, s *session) error {
	b.logger.Info("waiting for the install and Setup Assistant to be completed over VNC and SSH to become available",
		"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", b.vncPort), "user", b.creds.User)
	if err := b.waitSSH(ctx, s, 2*time.Hour); err != nil {
		return fmt.Errorf("waiting for macOS install: %w", err)
	}
	return b.hooksAndShutdown(ctx, s)
}

// headlessSetup is the second phase: boot Recovery again (a fresh QEMU
// with the install media attached boots it, the installer's bless does
// not survive the restart) and run flow.HeadlessSetup, which creates the
// user, skips Setup Assistant, enables Remote Login and shuts down.
func (b *fullBuild) headlessSetup(ctx context.Context) (idle bool, err error) {
	steps, err := flow.HeadlessSetup(b.creds.User, b.creds.Password)
	if err != nil {
		return false, err
	}
	stopAfter := ""
	if hasStep(steps, b.StopAfterStep) {
		stopAfter = b.StopAfterStep
	}
	steps, _ = flow.StepsThrough(steps, stopAfter)

	b.logger.Info("booting Recovery for the headless setup", "user", b.creds.User)
	s, err := b.start(ctx, true)
	if err != nil {
		return false, fmt.Errorf("starting Recovery VM: %w", err)
	}
	defer func() { s.finish(err != nil || idle) }()

	driver := newScreenDriver(ctx, b.Config, s.machine.QMPSocket(), b.shotDir, b.logger)
	if driver == nil {
		return false, fmt.Errorf("headless setup needs screen reading (OCR), which is unavailable")
	}
	defer driver.Close()
	driveCtx, cancelDrive := s.watch(ctx)
	err = driver.driveRecovery(driveCtx, s.machine.QMPSocket(), steps, b.recoveryTimeout())
	cancelDrive()
	driver.Close()
	if err != nil {
		if s.exited() {
			return false, fmt.Errorf("VM exited during the headless setup: %w", err)
		}
		return false, fmt.Errorf("headless setup: %w", err)
	}
	if stopAfter != "" {
		return true, s.hold(ctx, stopAfter)
	}
	b.logger.Info("headless setup done, Setup Assistant skipped and Remote Login enabled", "user", b.creds.User)
	// The last step typed shutdown; let the guest finish it.
	select {
	case <-s.machine.Done():
	case <-time.After(3 * time.Minute):
		b.logger.Info("Recovery did not shut down, stopping VM")
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return false, nil
}

// provision is the last phase: boot the installed system alone, wait for
// SSH, run the hooks, and shut it down.
func (b *fullBuild) provision(ctx context.Context) (err error) {
	b.logger.Info("booting the installed system", "nic", b.nic)
	s, err := b.start(ctx, false)
	if err != nil {
		return fmt.Errorf("starting installed system: %w", err)
	}
	defer func() { s.finish(err != nil) }()
	if err := b.waitSSH(ctx, s, 2*time.Hour); err != nil {
		return fmt.Errorf("waiting for the installed system: %w", err)
	}
	return b.hooksAndShutdown(ctx, s)
}

// waitSSH waits for the SSH banner, ending early if the VM goes away.
func (b *fullBuild) waitSSH(ctx context.Context, s *session, timeout time.Duration) error {
	sshCtx, cancel := s.watch(ctx)
	defer cancel()
	err := waitForSSH(sshCtx, b.sshAddr(), timeout, b.logger)
	if err != nil && s.exited() {
		return fmt.Errorf("VM exited (out of memory? see qemu.log): %w", err)
	}
	return err
}

// hooksAndShutdown runs the firstboot and boot hooks over SSH and shuts
// the guest down (passwordless sudo is part of the headless setup).
func (b *fullBuild) hooksAndShutdown(ctx context.Context, s *session) error {
	sshAddr := b.sshAddr()
	if b.Opts != nil && len(b.Opts.Hooks) > 0 {
		b.logger.Info("running firstboot hooks", "user", b.creds.User)
		if err := runHooks(ctx, b.Opts.Hooks, buildopts.FirstBoot, sshAddr, b.creds, b.logger); err != nil {
			return fmt.Errorf("firstboot hooks: %w", err)
		}

		b.logger.Info("running boot hooks")
		if err := runHooks(ctx, b.Opts.Hooks, buildopts.Boot, sshAddr, b.creds, b.logger); err != nil {
			return fmt.Errorf("boot hooks: %w", err)
		}
	}

	b.logger.Info("shutting down guest")
	_ = runSSHCommand(ctx, sshAddr, b.creds, "sudo shutdown -h now", 30*time.Second)
	select {
	case <-s.machine.Done():
	case <-time.After(3 * time.Minute):
		b.logger.Info("guest did not shut down gracefully, stopping VM")
		s.machine.Stop()
	}
	return nil
}

func (b *fullBuild) flatten() error {
	if b.KeepVM {
		b.logger.Info("build complete", "image", b.diskPath, "flattened", false)
		return nil
	}
	b.logger.Info("flattening disk image", "src", b.diskPath, "dest", b.Dest)
	if err := qemu.FlattenQcow2(b.diskPath, b.Dest); err != nil {
		return fmt.Errorf("flattening disk: %w", err)
	}
	b.logger.Info("build complete", "image", b.Dest)
	return nil
}

func Recovery(ctx context.Context, c Config) error {
	logger := c.logger()

	if c.Opts != nil {
		if err := c.Opts.Validate(); err != nil {
			return err
		}
	}

	if c.GuestArch == "" {
		c.GuestArch = defaultGuestArch()
	}

	dmgPath, err := resolveMedia(ctx, c, logger)
	if err != nil {
		return err
	}

	workDir := c.WorkDir
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(c.Dest), ".mackit-build")
	}
	ocImg, err := prepareOpenCore(ctx, c, workDir, logger)
	if err != nil {
		return err
	}

	logger.Info("starting recovery build", "dest", c.Dest)

	diskPath := filepath.Join(workDir, "mackit-recovery.qcow2")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		logger.Info("creating recovery disk", "path", diskPath, "size", "8G")
		if err := qemu.CreateDisk(diskPath, 8); err != nil {
			return fmt.Errorf("creating disk: %w", err)
		}
	}

	sshPort := buildopts.DefaultSSHPort
	vncPort := buildopts.DefaultVNCPort
	if c.Opts != nil {
		sshPort = c.Opts.Ports.SSHOrDefault()
		vncPort = c.Opts.Ports.VNCOrDefault()
	}
	if sshPort == 0 {
		sshPort = qemu.FreePort()
	}
	if vncPort == 0 {
		vncPort = qemu.FreePort()
	}

	guestArch := c.GuestArch

	accel := c.Accel
	if accel == "" {
		accel = qemu.DefaultAccelForArch(guestArch)
	}

	display := c.DisplayType
	if display == "" {
		display = fmt.Sprintf("vnc=:%d", vncPort-5900)
	}

	logger.Info("launching QEMU with recovery media",
		"dmg", dmgPath,
		"opencore", ocImg,
		"accel", accel,
		"vnc_port", vncPort,
		"guest_arch", guestArch,
	)

	installCfg := qemu.InstallConfig{
		InstallerDMG: dmgPath,
		OpenCoreImg:  ocImg,
		DiskPath:     diskPath,
		GuestArch:    guestArch,
		CPUs:         4,
		MemoryGB:     c.memoryGB(4),
		SSHPort:      sshPort,
		VNCPort:      vncPort,
		Accel:        accel,
		DisplayType:  display,
		OutputDir:    workDir,
	}

	// With KeepVM the VM must outlive this call and its context: QEMU is
	// started from a context that is not cancelled with the build.
	vmCtx := ctx
	if c.KeepVM {
		vmCtx = context.WithoutCancel(ctx)
	}
	machine, err := qemu.StartInstall(vmCtx, installCfg)
	if err != nil {
		return fmt.Errorf("starting recovery VM: %w", err)
	}
	defer c.stopVM(machine, vncPort, logger)
	stopSerial := forwardSerial(ctx, workDir, logger)
	defer stopSerial()

	shotDir := filepath.Join(workDir, "screenshots")
	os.MkdirAll(shotDir, 0o755)
	shotStop := make(chan struct{})
	shotDone := make(chan struct{})
	go qemu.CaptureScreenshots(
		machine.QMPSocket(), shotDir, 15*time.Second,
		shotStop, shotDone,
		func(f string, a ...any) { logger.Debug(fmt.Sprintf(f, a...), "component", "screenshot") },
	)
	defer func() {
		close(shotStop)
		<-shotDone
	}()

	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	hasHooks := c.Opts != nil && len(c.Opts.Hooks) > 0

	driver := newScreenDriver(ctx, c, machine.QMPSocket(), shotDir, logger)
	defer driver.Close()
	if driver == nil && len(c.RecoverySteps) > 0 {
		return fmt.Errorf("recovery steps need OCR screen detection, which is unavailable")
	}

	if driver != nil {
		// Drive the UI: confirm the picker if shown, wait for macOS
		// Utilities, run the configured steps. The VM exiting on its own
		// cancels the wait.
		driveCtx, cancelDrive := context.WithCancel(ctx)
		go func() {
			select {
			case <-machine.Done():
				cancelDrive()
			case <-driveCtx.Done():
			}
		}()
		logger.Info("recovery mode: watching the screen for macOS Recovery",
			"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", vncPort),
			"screenshots", shotDir,
			"serial_log", filepath.Join(workDir, "serial.log"),
		)
		recoveryTimeout := c.RecoveryTimeout
		if recoveryTimeout <= 0 {
			recoveryTimeout = 30 * time.Minute
		}
		err := driver.driveRecovery(driveCtx, machine.QMPSocket(), c.RecoverySteps, recoveryTimeout)
		cancelDrive()
		if err != nil {
			select {
			case <-machine.Done():
				return fmt.Errorf("VM exited before reaching macOS Recovery: %w", err)
			default:
			}
			return err
		}
	} else {
		go autoSelectPicker(machine.QMPSocket(), logger)
	}

	if hasHooks {
		logger.Info("waiting for recovery SSH")
		if err := waitForSSH(ctx, sshAddr, 30*time.Minute, logger); err != nil {
			return fmt.Errorf("waiting for recovery SSH: %w", err)
		}

		logger.Info("running boot hooks")
		if err := runHooks(ctx, c.Opts.Hooks, buildopts.Boot, sshAddr, c.creds(), logger); err != nil {
			return fmt.Errorf("boot hooks: %w", err)
		}
	} else if driver == nil {
		logger.Info("recovery mode: waiting for VM to boot (stop with ctrl-c or context timeout)",
			"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", vncPort),
			"screenshots", shotDir,
			"serial_log", filepath.Join(workDir, "serial.log"),
		)
		select {
		case <-ctx.Done():
			logger.Info("context timeout reached")
		case <-machine.Done():
			logger.Info("VM exited on its own")
		}
	}

	if c.KeepVM {
		return nil
	}
	logger.Info("stopping recovery VM")
	machine.Stop()
	<-machine.Done()

	logger.Info("flattening recovery image", "src", diskPath, "dest", c.Dest)
	if err := qemu.FlattenQcow2(diskPath, c.Dest); err != nil {
		return fmt.Errorf("flattening disk: %w", err)
	}

	logger.Info("recovery build complete", "image", c.Dest)
	return nil
}

// forwardSerial tails <workDir>/serial.log into the build logger so the
// guest console (UEFI, OpenCore, kernel with -v serial=1) lands in the
// same log as mackit's own records, each line tagged with its source.
// The returned func stops the tailer and waits for it to flush.
func forwardSerial(ctx context.Context, workDir string, logger *slog.Logger) func() {
	tailCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		qemu.TailSerial(tailCtx, filepath.Join(workDir, "serial.log"), logger)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

func ResolveBackend() (vm.VMBackend, string, error) {
	registry := map[string]vm.VMBackend{
		"qemu": &qemu.Backend{},
	}
	return vm.ResolveBackend("", registry)
}

func resolveMedia(ctx context.Context, c Config, logger *slog.Logger) (string, error) {
	var dmgPath string

	if c.InstallerDMG != "" {
		logger.Info("using provided installer media", "path", c.InstallerDMG)
		if err := installer.PrepareInstall(c.InstallerDMG); err != nil {
			return "", err
		}
		dmgPath = c.InstallerDMG
	} else {
		from := "sequoia"
		arch := c.GuestArch
		if c.Opts != nil && c.Opts.From != "" {
			kind := buildopts.DetectFrom(c.Opts.From)
			switch kind {
			case buildopts.FromDMG:
				logger.Info("using local DMG", "path", c.Opts.From)
				if err := installer.PrepareInstall(c.Opts.From); err != nil {
					return "", err
				}
				dmgPath = c.Opts.From
			case buildopts.FromIPSW:
				logger.Info("extracting BaseSystem.dmg from IPSW", "path", c.Opts.From)
				cacheDir := c.CacheDir
				if cacheDir == "" {
					cacheDir = filepath.Join(os.TempDir(), "mackit-ipsw")
				}
				p, err := installer.ExtractFromIPSW(c.Opts.From, cacheDir)
				if err != nil {
					return "", err
				}
				dmgPath = p
			case buildopts.FromCatalog:
				parsed, parsedArch, err := media.ParseFrom(c.Opts.From)
				if err != nil {
					return "", err
				}
				from = parsed
				if arch == "" {
					arch = parsedArch
				}
			}
		}

		if dmgPath == "" {
			result, err := media.FetchInstallerDMG(ctx, media.FetchOptions{
				CacheDir: c.CacheDir,
				Version:  from,
				Arch:     arch,
				NoCache:  c.NoCache,
				Logger:   logger,
			})
			if err != nil {
				return "", err
			}
			dmgPath = result.Path
		}
	}

	// Apple DMGs are UDIF-compressed. QEMU needs raw block images with
	// readable GPT partition tables so OpenCore can scan for boot entries.
	if installer.IsUDIFDMG(dmgPath) {
		rawPath := dmgPath + ".raw"
		logger.Info("converting UDIF DMG to raw image", "src", dmgPath, "dest", rawPath)
		converted, err := installer.ConvertDMGToRaw(dmgPath, rawPath)
		if err != nil {
			return "", fmt.Errorf("converting DMG: %w", err)
		}
		return converted, nil
	}

	return dmgPath, nil
}

func patchOpenCore(srcImg, destImg string, timeout int, logger *slog.Logger) error {
	files, err := qemu.ReadAllFilesFromFATQcow2(srcImg)
	if err != nil {
		return fmt.Errorf("reading OpenCore image: %w", err)
	}

	var configPath string
	for p := range files {
		if strings.HasSuffix(strings.ToUpper(p), "/EFI/OC/CONFIG.PLIST") {
			configPath = p
			break
		}
	}
	if configPath == "" {
		return fmt.Errorf("config.plist not found in OpenCore image")
	}

	logger.Info("patching OpenCore config.plist", "path", configPath, "timeout", timeout)
	patched, err := installer.PatchOpenCorePlist(files[configPath], timeout)
	if err != nil {
		return err
	}

	// Patch in place: the image keeps its GPT and ESP geometry, which the
	// firmware's FAT driver is picky about.
	if err := qemu.ReplaceFileInFATQcow2(srcImg, destImg, configPath, patched); err != nil {
		return fmt.Errorf("writing patched OpenCore image: %w", err)
	}
	return nil
}

// PrepareOpenCore returns the OpenCore boot image a VM should boot from:
// the cached release (downloaded on first use into cacheDir, version ""
// for the default) with its config.plist patched for an unattended,
// observable boot (see installer.PatchOpenCorePlist), written to
// <workDir>/OpenCore.qcow2. When patching fails the unpatched cached
// image is returned with a log line, so the VM still boots. Builds and
// mackit.Start share this so every boot shows the kernel's verbose console
// and streams it to the serial log.
func PrepareOpenCore(ctx context.Context, cacheDir, workDir, version string, logger *slog.Logger) (string, error) {
	return prepareOpenCore(ctx, Config{CacheDir: cacheDir, Opts: &buildopts.BuildOpts{OpenCoreVersion: version}}, workDir, logger)
}

func prepareOpenCore(ctx context.Context, c Config, workDir string, logger *slog.Logger) (string, error) {
	ocImg, err := resolveOpenCore(ctx, c, logger)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("creating work dir: %w", err)
	}
	patched := filepath.Join(workDir, "OpenCore.qcow2")
	if err := patchOpenCore(ocImg, patched, 5, logger); err != nil {
		logger.Info("could not patch OpenCore config, using original", "error", err)
		return ocImg, nil
	}
	return patched, nil
}

func resolveOpenCore(ctx context.Context, c Config, logger *slog.Logger) (string, error) {
	if c.OpenCoreImg != "" {
		if _, err := os.Stat(c.OpenCoreImg); err != nil {
			return "", fmt.Errorf("OpenCore image not found: %w", err)
		}
		return c.OpenCoreImg, nil
	}

	cacheDir := c.CacheDir
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache", "mackit")
	}

	ocVersion := ""
	if c.Opts != nil {
		ocVersion = c.Opts.OpenCoreVersion
	}

	return installer.EnsureOpenCore(ctx, installer.OpenCoreConfig{
		Version:  ocVersion,
		CacheDir: cacheDir,
		NoCache:  c.NoCache,
		Logger:   logger,
	})
}

// stopVM stops the build VM on the way out, unless KeepVM asks for it to
// be left running for inspection; then it logs where to find it.
func (c Config) stopVM(machine *qemu.InstallVM, vncPort uint16, logger *slog.Logger) {
	if !c.KeepVM {
		machine.Stop()
		return
	}
	select {
	case <-machine.Done():
		return
	default:
	}
	logger.Info("VM left running",
		"pid", machine.PID(),
		"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", vncPort),
		"qmp", machine.QMPSocket())
}
