//go:build darwin && arm64

package vz

import (
	"fmt"
	"os"

	"github.com/Code-Hex/vz/v3"
)

type configParams struct {
	cpus          uint
	memoryGB      uint64
	diskPath      string
	hardwareModel *vz.MacHardwareModel
	ecid          *vz.MacMachineIdentifier
	aux           *vz.MacAuxiliaryStorage
	sshPort       uint16
	vncPort       uint16
	recovery      bool
	serialLogPath string
}

func buildConfig(p configParams) (*vz.VirtualMachineConfiguration, error) {
	bootLoader, err := vz.NewMacOSBootLoader()
	if err != nil {
		return nil, fmt.Errorf("boot loader: %w", err)
	}
	cfg, err := vz.NewVirtualMachineConfiguration(
		bootLoader,
		p.cpus,
		p.memoryGB*1024*1024*1024,
	)
	if err != nil {
		return nil, err
	}

	platform, err := vz.NewMacPlatformConfiguration(
		vz.WithMacAuxiliaryStorage(p.aux),
		vz.WithMacHardwareModel(p.hardwareModel),
		vz.WithMacMachineIdentifier(p.ecid),
	)
	if err != nil {
		return nil, fmt.Errorf("platform config: %w", err)
	}
	cfg.SetPlatformVirtualMachineConfiguration(platform)

	disk, err := vz.NewDiskImageStorageDeviceAttachment(p.diskPath, false)
	if err != nil {
		return nil, fmt.Errorf("disk attachment: %w", err)
	}
	blockDev, err := vz.NewVirtioBlockDeviceConfiguration(disk)
	if err != nil {
		return nil, err
	}
	cfg.SetStorageDevicesVirtualMachineConfiguration([]vz.StorageDeviceConfiguration{blockDev})

	netCfg, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		return nil, err
	}
	netDev, err := vz.NewVirtioNetworkDeviceConfiguration(netCfg)
	if err != nil {
		return nil, err
	}
	cfg.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netDev})

	graphics, err := vz.NewMacGraphicsDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	display, err := vz.NewMacGraphicsDisplayConfiguration(1920, 1080, 144)
	if err != nil {
		return nil, err
	}
	graphics.SetDisplays(display)
	cfg.SetGraphicsDevicesVirtualMachineConfiguration([]vz.GraphicsDeviceConfiguration{graphics})

	keyboard, err := vz.NewMacKeyboardConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetKeyboardsVirtualMachineConfiguration([]vz.KeyboardConfiguration{keyboard})

	trackpad, err := vz.NewMacTrackpadConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetPointingDevicesVirtualMachineConfiguration([]vz.PointingDeviceConfiguration{trackpad})

	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropy})

	if p.serialLogPath != "" {
		f, err := os.OpenFile(p.serialLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("serial log: %w", err)
		}
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("opening /dev/null for serial read: %w", err)
		}
		attachment, err := vz.NewFileHandleSerialPortAttachment(devNull, f)
		if err != nil {
			devNull.Close()
			f.Close()
			return nil, fmt.Errorf("serial attachment: %w", err)
		}
		serial, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(attachment)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("serial port config: %w", err)
		}
		cfg.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{serial})
	}

	valid, err := cfg.Validate()
	if err != nil {
		return nil, fmt.Errorf("VM config validation: %w", err)
	}
	if !valid {
		return nil, fmt.Errorf("VM configuration is invalid")
	}

	return cfg, nil
}
