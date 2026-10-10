package qemu

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpec_ApplyDefaults(t *testing.T) {
	s := Spec{}
	s.ApplyDefaults()
	assert.Equal(t, uint(4), s.CPUs)
	assert.Equal(t, uint64(4), s.MemoryGB)
	assert.Equal(t, "none", s.DisplayType)
	assert.NotEmpty(t, s.Accel)
}

func TestSpec_Validate(t *testing.T) {
	t.Run("MissingDiskPath", func(t *testing.T) {
		s := Spec{FirmwarePath: "/fw.fd"}
		assert.Error(t, s.Validate())
	})

	t.Run("MissingFirmware", func(t *testing.T) {
		s := Spec{DiskPath: "/disk.qcow2"}
		assert.Error(t, s.Validate())
	})

	t.Run("Valid", func(t *testing.T) {
		s := Spec{DiskPath: "/disk.qcow2", FirmwarePath: "/fw.fd"}
		assert.NoError(t, s.Validate())
	})
}

func TestQMPSocketPath(t *testing.T) {
	s := Spec{VMName: "test", QMPSocketDir: "/tmp/mackit"}
	p := QMPSocketPath(s)
	assert.Contains(t, p, "mackit-test-qmp.sock")
}

func TestQMPSocketPath_LongName(t *testing.T) {
	name := ""
	for i := 0; i < 100; i++ {
		name += "a"
	}
	s := Spec{VMName: name, QMPSocketDir: "/tmp/mackit"}
	p := QMPSocketPath(s)
	assert.LessOrEqual(t, len(p), 104)
}

func TestDefaultAccel(t *testing.T) {
	a := DefaultAccel()
	assert.NotEmpty(t, a)
}

func TestQEMUBinaryPath(t *testing.T) {
	p := QEMUBinaryPath()
	assert.Contains(t, p, "qemu-system-")
}

func TestPrepareVarsFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "firmware.fd")
	fwData := []byte("fake firmware data for testing")
	assert.NoError(t, os.WriteFile(srcPath, fwData, 0o644))

	dstPath := filepath.Join(dir, "vars.fd")
	assert.NoError(t, PrepareVarsFile(srcPath, dstPath))

	data, err := os.ReadFile(dstPath)
	assert.NoError(t, err)
	assert.Equal(t, fwData, data)
}
