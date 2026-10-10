package qemu

import (
	"fmt"
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
		return "Penryn,+ssse3,+sse4.2,+popcnt,+avx,+aes,+pclmulqdq,+xsave,+xsaveopt,check"
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
	argv := []string{
		QEMUBinaryForArch(spec.guestArch()),
		"-machine", machineType(spec),
		"-cpu", cpuType(spec),
		"-accel", spec.effectiveAccel(),
		"-smp", fmt.Sprintf("cpus=%d", spec.CPUs),
		"-m", fmt.Sprintf("%dG", spec.MemoryGB),
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

	argv = append(argv,
		"-netdev", "user,id=net0",
		"-device", "virtio-net-pci,netdev=net0",
	)

	argv = append(argv, "-display", spec.DisplayType)

	// Input devices on the chipset EHCI/UHCI controllers (-usb). macOS
	// Recovery ignored both usb-kbd and usb-tablet when they hung off a
	// qemu-xhci controller under EDK2/OpenCore (no cursor movement, no
	// key events); the EHCI path is the recipe used by macOS-Simple-KVM
	// and friends for Catalina.
	argv = append(argv,
		"-usb",
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

	if spec.VNCPort != 0 {
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
