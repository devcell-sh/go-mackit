package buildopts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseForward(t *testing.T) {
	t.Run("ValidForward", func(t *testing.T) {
		h, g, err := ParseForward("8080:80")
		require.NoError(t, err)
		assert.Equal(t, uint16(8080), h)
		assert.Equal(t, uint16(80), g)
	})

	t.Run("WithSpaces", func(t *testing.T) {
		h, g, err := ParseForward(" 3000 : 3000 ")
		require.NoError(t, err)
		assert.Equal(t, uint16(3000), h)
		assert.Equal(t, uint16(3000), g)
	})

	t.Run("MissingColon", func(t *testing.T) {
		_, _, err := ParseForward("8080")
		assert.Error(t, err)
	})

	t.Run("InvalidPort", func(t *testing.T) {
		_, _, err := ParseForward("0:80")
		assert.Error(t, err)
	})

	t.Run("PortTooLarge", func(t *testing.T) {
		_, _, err := ParseForward("70000:80")
		assert.Error(t, err)
	})
}

func TestPorts_Defaults(t *testing.T) {
	p := Ports{}
	assert.Equal(t, DefaultSSHPort, p.SSHOrDefault())
	assert.Equal(t, DefaultVNCPort, p.VNCOrDefault())
}

func TestPorts_Custom(t *testing.T) {
	p := Ports{SSH: 2222, VNC: 5901}
	assert.Equal(t, uint16(2222), p.SSHOrDefault())
	assert.Equal(t, uint16(5901), p.VNCOrDefault())
}

func TestDetectFrom(t *testing.T) {
	assert.Equal(t, FromCatalog, DetectFrom("macos/sequoia"))
	assert.Equal(t, FromDMG, DetectFrom("./BaseSystem.dmg"))
	assert.Equal(t, FromDMG, DetectFrom("/path/to/Recovery.DMG"))
	assert.Equal(t, FromIPSW, DetectFrom("./UniversalMac.ipsw"))
	assert.Equal(t, FromIPSW, DetectFrom("/path/to/installer.IPSW"))
}

func TestBuildOpts_Stage(t *testing.T) {
	opts := &BuildOpts{}
	assert.Equal(t, StageFull, opts.Stage())

	opts.Recovery = true
	assert.Equal(t, StageRecovery, opts.Stage())
}

func TestBuildOpts_ApplyDefaults(t *testing.T) {
	opts := &BuildOpts{}
	opts.ApplyDefaults()
	assert.NotEmpty(t, opts.From)
	assert.Contains(t, opts.From, "macos/sequoia")
}

func TestBuildOpts_Validate_RecoveryPhase(t *testing.T) {
	opts := &BuildOpts{
		Recovery: true,
		Hooks: []Hook{
			{Phase: FirstBoot, Cmd: "echo fail"},
		},
	}
	err := opts.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "recovery mode only supports boot phase")
}

func TestBuildOpts_Validate_RecoveryBootOK(t *testing.T) {
	opts := &BuildOpts{
		Recovery: true,
		Hooks: []Hook{
			{Phase: Boot, Cmd: "echo ok"},
		},
	}
	err := opts.Validate()
	assert.NoError(t, err)
}

func TestHookPhase_String(t *testing.T) {
	assert.Equal(t, "firstboot", FirstBoot.String())
	assert.Equal(t, "boot", Boot.String())
}

func TestDefaultFrom(t *testing.T) {
	from := DefaultFrom()
	assert.Contains(t, from, "macos/sequoia")
}
