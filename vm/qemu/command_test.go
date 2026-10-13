package qemu

import (
	"fmt"
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
	assert.Contains(t, cmd, fmt.Sprintf("-smp cpus=%d", TCGMaxCPUs))
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

func TestVNCDisplayOwnsItsPort(t *testing.T) {
	assert.Equal(t, uint16(25900), VNCDisplayPort("vnc=:20000"))
	assert.Equal(t, uint16(5901), VNCDisplayPort("vnc=127.0.0.1:1"))
	assert.Equal(t, uint16(5900), VNCDisplayPort("vnc=:0,password=on"))
	assert.Equal(t, uint16(0), VNCDisplayPort("none"))
	assert.Equal(t, uint16(0), VNCDisplayPort("vnc=unix:/tmp/sock"))

	// A build VM: the display on 25900 and no guest forward fighting for it.
	spec := Spec{DisplayType: "vnc=:20000", SSHPort: 20022, VNCPort: 25900}
	cmd := strings.Join(BuildInstallCommand(spec, "/tmp/BaseSystem.dmg", "/tmp/OpenCore.qcow2"), " ")
	assert.Contains(t, cmd, "-display vnc=:20000")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:20022-:22")
	assert.NotContains(t, cmd, "-:5900", "the guest Screen Sharing forward must not shadow the display")

	// Headless with the guest forward on a different port: kept.
	spec = Spec{DisplayType: "vnc=:1", SSHPort: 20022, VNCPort: 25900}
	cmd = strings.Join(BuildInstallCommand(spec, "/tmp/BaseSystem.dmg", "/tmp/OpenCore.qcow2"), " ")
	assert.Contains(t, cmd, "hostfwd=tcp:127.0.0.1:25900-:5900")
}

func TestNICModel(t *testing.T) {
	spec := Spec{DisplayType: "none", SSHPort: 20022}
	cmd := strings.Join(BuildRunCommand(spec), " ")
	assert.Contains(t, cmd, "-device virtio-net-pci,netdev=net0", "default NIC")

	spec.NIC = NICCatalina
	cmd = strings.Join(BuildInstallCommand(spec, "/tmp/BaseSystem.dmg", "/tmp/OpenCore.qcow2"), " ")
	assert.Contains(t, cmd, "-device vmxnet3,netdev=net0")
	assert.NotContains(t, cmd, "virtio-net")
}
