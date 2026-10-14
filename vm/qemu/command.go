package qemu

import (
	"fmt"
	"strconv"
	"strings"
)

func isX86(arch string) bool {
	return arch == "amd64" || arch == "x86_64"
}

func machineType(spec Spec) string {
	if spec.MachineType != "" {
		return spec.MachineType
	}
	if isX86(spec.guestArch()) {
		return "q35"
	}
	accel := accelBase(spec.effectiveAccel())
	switch {
	case accel == "hvf":
		return "virt,highmem=on"
	case accel == "tcg":
		return "virt,virtualization=true"
	default:
		return "virt"
	}
}

func cpuType(spec Spec) string {
	if spec.CPU != "" {
		return spec.CPU
	}
	accel := accelBase(spec.effectiveAccel())
	if isX86(spec.guestArch()) {
		if accel == "hvf" || accel == "kvm" {
			return "host"
		}
		return "Haswell-noTSX"
	}
	switch accel {
	case "hvf":
		return "host"
	case "kvm":
		return "max"
	default:
		return "max,pauth-impdef=on"
	}
}

func baseCommand(spec Spec) []string {
	accel := spec.effectiveAccel()
	cpus := spec.CPUs
	if IsTCG(accel) && cpus > TCGMaxCPUs {
		cpus = TCGMaxCPUs
	}

	argv := []string{
		QEMUBinaryForArch(spec.guestArch()),
		"-machine", machineType(spec),
		"-cpu", cpuType(spec),
		"-accel", accel,
		"-smp", fmt.Sprintf("cpus=%d", cpus),
		"-m", fmt.Sprintf("%dG", spec.MemoryGB),
	}

	if IsTCG(accel) {
		if hp := HugepagesArgs(); hp != nil {
			argv = append(argv, hp...)
		}
	}

	if spec.VarsPath != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("if=pflash,format=raw,unit=0,file=%s,readonly=on", spec.FirmwarePath),
			"-drive", fmt.Sprintf("if=pflash,format=raw,unit=1,file=%s", spec.VarsPath),
		)
	} else {
		argv = append(argv, "-bios", spec.FirmwarePath)
	}

	// OpenCore requires AHCI/SATA drives to scan for macOS boot entries.
	// VirtIO block devices are not detectable by the OpenCore EFI scanner.
	argv = append(argv, "-device", "ich9-ahci,id=sata")

	if spec.OpenCoreImg != "" {
		ocFormat := "qcow2"
		if !strings.HasSuffix(spec.OpenCoreImg, ".qcow2") {
			ocFormat = "raw"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("id=OpenCore,if=none,format=%s,file=%s", ocFormat, spec.OpenCoreImg),
			"-device", "ide-hd,bus=sata.0,drive=OpenCore,bootindex=0",
		)
	}

	driveFormat := "qcow2"
	if !strings.HasSuffix(spec.DiskPath, ".qcow2") {
		driveFormat = "raw"
	}
	diskOpts := fmt.Sprintf("id=MacHDD,if=none,format=%s,file=%s", driveFormat, spec.DiskPath)
	if spec.DiskCacheMode != "" {
		diskOpts += ",cache=" + spec.DiskCacheMode
	}
	argv = append(argv,
		"-drive", diskOpts,
		"-device", "ide-hd,bus=sata.2,drive=MacHDD,bootindex=1",
	)

	nic := spec.NIC
	if nic == "" {
		nic = DefaultNIC
	}
	argv = append(argv,
		"-netdev", "user,id=net0",
		"-device", nic+",netdev=net0",
	)

	if spec.VGA != "" {
		argv = append(argv, "-vga", spec.VGA)
	}
	argv = append(argv, "-display", spec.DisplayType)

	// Input: EHCI USB for macOS (xHCI was ignored by macOS Recovery).
	// On x86 Q35, -usb creates a chipset EHCI; on arm64 virt there is
	// no built-in USB controller so we add one explicitly.
	if isX86(spec.guestArch()) {
		argv = append(argv, "-usb")
	} else {
		argv = append(argv, "-device", "ich9-usb-ehci1,id=usb-ctl")
	}
	argv = append(argv,
		"-device", "usb-kbd,id="+USBKeyboardID,
		"-device", "usb-tablet,id="+USBTabletID,
	)

	if spec.SerialLogPath != "" {
		argv = append(argv, "-serial", "file:"+spec.SerialLogPath)
	} else {
		argv = append(argv, "-serial", "mon:stdio")
	}

	qmpSock := QMPSocketPath(spec)
	argv = append(argv, "-qmp", "unix:"+qmpSock+",server,nowait")

	if spec.NoReboot {
		argv = append(argv, "-no-reboot")
	}
	if spec.VMName != "" {
		argv = append(argv, "-name", spec.VMName)
	}
	if spec.SMBIOSSerial != "" {
		argv = append(argv, "-smbios", "type=1,serial="+spec.SMBIOSSerial)
	}

	return argv
}

// DefaultNIC is the network device model when Spec.NIC is empty. macOS
// 11 and later drive it natively. An installed Catalina does not: on
// virtio-net-pci its en0 came up with a random MAC and never saw slirp's
// DHCP offers (verified 2026-10-10 with a packet dump), although the same
// device works in Catalina's Recovery. Those guests take NICCatalina.
const DefaultNIC = "virtio-net-pci"

// NICCatalina is the network device model for macOS 10.15 and earlier
// guests, driven by the VMware driver macOS ships.
const NICCatalina = "vmxnet3"

func BuildInstallCommand(spec Spec, installerIMG, openCoreImg string) []string {
	spec.OpenCoreImg = openCoreImg
	argv := baseCommand(spec)

	// Installer media on SATA port 1 (between OpenCore on 0 and disk on 2).
	imgFormat := "raw"
	if strings.HasSuffix(installerIMG, ".qcow2") {
		imgFormat = "qcow2"
	}
	argv = append(argv,
		"-drive", fmt.Sprintf("id=InstallMedia,if=none,format=%s,file=%s", imgFormat, installerIMG),
		"-device", "ide-hd,bus=sata.1,drive=InstallMedia,bootindex=2",
	)

	argv = applySSHForward(spec, argv)
	return argv
}

func BuildRunCommand(spec Spec) []string {
	argv := baseCommand(spec)
	argv = append(argv, "-boot", "c")
	argv = applySSHForward(spec, argv)
	return argv
}

func applySSHForward(spec Spec, argv []string) []string {
	host := spec.SSHHost
	if host == "" {
		host = "127.0.0.1"
	}

	var fwds []string

	if spec.SSHPort != 0 {
		guestPort := spec.SSHGuestPort
		if guestPort == 0 {
			guestPort = 22
		}
		fwds = append(fwds, fmt.Sprintf("hostfwd=tcp:%s:%d-:%d", host, spec.SSHPort, guestPort))
	}

	// The guest's own Screen Sharing (5900) is forwarded to VNCPort unless
	// QEMU's VNC display already listens there: builds put the display on
	// that port, and a loopback forward on the same number would win over
	// the display's wildcard bind and send viewers into a guest that has
	// no VNC server (Recovery), which closes the connection.
	if spec.VNCPort != 0 && VNCDisplayPort(spec.DisplayType) != spec.VNCPort {
		fwds = append(fwds, fmt.Sprintf("hostfwd=tcp:%s:%d-:5900", host, spec.VNCPort))
	}

	for _, f := range spec.Forwards {
		fwds = append(fwds, fmt.Sprintf("hostfwd=tcp:%s:%d-:%d", host, f.Host, f.Guest))
	}

	if len(fwds) == 0 {
		return argv
	}

	for i, arg := range argv {
		if arg == "user,id=net0" {
			argv[i] = "user,id=net0," + strings.Join(fwds, ",")
			break
		}
	}
	return argv
}

// VNCDisplayPort returns the TCP port a "vnc=[host]:N" display listens on
// (5900+N), or 0 for any other display type.
func VNCDisplayPort(displayType string) uint16 {
	if !strings.HasPrefix(displayType, "vnc=") {
		return 0
	}
	spec := strings.TrimPrefix(displayType, "vnc=")
	if i := strings.IndexByte(spec, ','); i >= 0 {
		spec = spec[:i]
	}
	i := strings.LastIndexByte(spec, ':')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(spec[i+1:])
	if err != nil || n < 0 || 5900+n > 65535 {
		return 0
	}
	return uint16(5900 + n)
}
