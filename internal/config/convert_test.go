package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-mackit/build/buildopts"
)

func TestToBuildOpts_Basic(t *testing.T) {
	cfg := &Config{
		From:     "macos/sequoia-arm64",
		Hostname: "myvm",
		Commands: commandsField{Phases: map[string][]CommandEntry{}},
	}
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	assert.Equal(t, "macos/sequoia-arm64", opts.From)
	assert.Equal(t, "myvm", opts.Hostname)
	assert.Equal(t, buildopts.StageFull, opts.Stage())
}

func TestToBuildOpts_Recovery(t *testing.T) {
	cfg := &Config{
		Recovery: true,
		Commands: commandsField{Phases: map[string][]CommandEntry{}},
	}
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	assert.True(t, opts.Recovery)
	assert.Equal(t, buildopts.StageRecovery, opts.Stage())
}

func TestToBuildOpts_WithPorts(t *testing.T) {
	cfg := &Config{
		Ports: &PortsConfig{
			SSH:     22022,
			VNC:     25901,
			Forward: []string{"8080:80"},
		},
		Commands: commandsField{Phases: map[string][]CommandEntry{}},
	}
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	assert.Equal(t, uint16(22022), opts.Ports.SSH)
	assert.Equal(t, uint16(25901), opts.Ports.VNC)
	assert.Equal(t, []string{"8080:80"}, opts.Ports.Forward)
}

func TestToBuildOpts_WithHooks(t *testing.T) {
	cfg := &Config{
		Commands: commandsField{Phases: map[string][]CommandEntry{
			"firstboot": {
				{Cmd: "systemsetup -setremotelogin on"},
			},
			"boot": {
				{Cmd: "brew install git", Timeout: duration(10 * time.Minute), Retries: 2},
			},
		}},
	}
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	require.Len(t, opts.Hooks, 2)

	// Hooks should be sorted: FirstBoot before Boot.
	assert.Equal(t, buildopts.FirstBoot, opts.Hooks[0].Phase)
	assert.Equal(t, "systemsetup -setremotelogin on", opts.Hooks[0].Cmd)

	assert.Equal(t, buildopts.Boot, opts.Hooks[1].Phase)
	assert.Equal(t, "brew install git", opts.Hooks[1].Cmd)
	assert.Equal(t, 10*time.Minute, opts.Hooks[1].Timeout)
	assert.Equal(t, 2, opts.Hooks[1].Retries)
}

func TestToBuildOpts_DefaultFrom(t *testing.T) {
	cfg := &Config{
		Commands: commandsField{Phases: map[string][]CommandEntry{}},
	}
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	assert.Contains(t, opts.From, "macos/sequoia-")
}

func TestToBuildOpts_Files(t *testing.T) {
	cfg := &Config{
		Files: []FileEntry{
			{Src: "./setup.sh", Dest: "/tmp/setup.sh"},
		},
		Commands: commandsField{Phases: map[string][]CommandEntry{}},
	}
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	require.Len(t, opts.Hooks, 1)
	assert.Equal(t, buildopts.Boot, opts.Hooks[0].Phase)
	assert.Equal(t, "file-inject", opts.Hooks[0].Label)
	require.Len(t, opts.Hooks[0].Files, 1)
	assert.Equal(t, "./setup.sh", opts.Hooks[0].Files[0].Src)
}
