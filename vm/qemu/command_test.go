package qemu

import (
	"strings"
	"testing"

	"github.com/devcell-sh/go-mackit/vm"
	"github.com/stretchr/testify/assert"
)

func TestBuildInstallCommand(t *testing.T) {
	spec := Spec{
		VMName:       "test-install",
		CPUs:         4,
		MemoryGB:     8,
		DiskPath:     "/tmp/mackit.qcow2",
		FirmwarePath: "/fw.fd",
		VarsPath:     "/vars.fd",
		DisplayType:  "none",
		Accel:        "tcg,thread=multi",
		SSHPort:      20022,
		VNCPort:      25900,
	}

	argv := BuildInstallCommand(spec, "/tmp/BaseSystem.dmg", "/tmp/OpenCore.qcow2")

	cmd := strings.Join(argv, " ")
	assert.Contains(t, cmd, "qemu-system-")
	assert.Contains(t, cmd, "-smp cpus=4")
	assert.Contains(t, cmd, "-m 8G")
	assert.Contains(t, cmd, "mackit.qcow2")
	assert.Contains(t, cmd, "BaseSystem.dmg")
	assert.Contains(t, cmd, "OpenCore.qcow2")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:20022-:22")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:25900-:5900")
}

func TestBuildRunCommand(t *testing.T) {
	spec := Spec{
		VMName:       "test-run",
		CPUs:         2,
		MemoryGB:     4,
		DiskPath:     "/tmp/mackit.qcow2",
		OpenCoreImg:  "/tmp/OpenCore.qcow2",
		FirmwarePath: "/fw.fd",
		DisplayType:  "none",
		Accel:        "tcg,thread=multi",
		SSHPort:      20022,
	}

	argv := BuildRunCommand(spec)
	cmd := strings.Join(argv, " ")
	assert.Contains(t, cmd, "-boot c")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:20022-:22")
}

func TestBuildRunCommand_Forwards(t *testing.T) {
	spec := Spec{
		VMName:       "test-fwd",
		DiskPath:     "/tmp/mackit.qcow2",
		FirmwarePath: "/fw.fd",
		DisplayType:  "none",
		Accel:        "tcg,thread=multi",
		SSHPort:      20022,
		Forwards: []vm.PortForward{
			{Host: 8080, Guest: 80},
			{Host: 3000, Guest: 3000},
		},
	}

	argv := BuildRunCommand(spec)
	cmd := strings.Join(argv, " ")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:8080-:80")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:3000-:3000")
}

func TestBuildRunCommand_SMBIOSSerial(t *testing.T) {
	spec := Spec{
		VMName:       "test-smbios",
		DiskPath:     "/tmp/mackit.qcow2",
		FirmwarePath: "/fw.fd",
		DisplayType:  "none",
		Accel:        "tcg,thread=multi",
		SMBIOSSerial: "MACKIT001",
	}

	argv := BuildRunCommand(spec)
	cmd := strings.Join(argv, " ")
	assert.Contains(t, cmd, "-smbios type=1,serial=MACKIT001")
}
