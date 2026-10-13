package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_BasicConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
from: macos/sequoia-arm64
hostname: myvm
`))
	require.NoError(t, err)
	assert.Equal(t, "macos/sequoia-arm64", cfg.From)
	assert.Equal(t, "myvm", cfg.Hostname)
	assert.False(t, cfg.Recovery)
}

func TestParse_UserPassword(t *testing.T) {
	cfg, err := Parse([]byte(`
from: macos/catalina-x86_64
user: dev
password: s3cret
`))
	require.NoError(t, err)
	assert.Equal(t, "dev", cfg.User)
	assert.Equal(t, "s3cret", cfg.Password)
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	assert.Equal(t, "dev", opts.User)
	assert.Equal(t, "s3cret", opts.Password)

	cfg, err = Parse([]byte("from: macos/catalina-x86_64\n"))
	require.NoError(t, err)
	opts, err = cfg.ToBuildOpts()
	require.NoError(t, err)
	assert.Equal(t, "admin", opts.User, "default user")
	assert.Equal(t, "admin", opts.Password, "default password")
}

func TestParse_CommandsFlat(t *testing.T) {
	cfg, err := Parse([]byte(`
commands:
  - echo hello
  - echo world
`))
	require.NoError(t, err)
	require.Len(t, cfg.Commands.Phases["boot"], 2)
	assert.Equal(t, "echo hello", cfg.Commands.Phases["boot"][0].Cmd)
	assert.Equal(t, "echo world", cfg.Commands.Phases["boot"][1].Cmd)
}

func TestParse_CommandsMap(t *testing.T) {
	cfg, err := Parse([]byte(`
commands:
  firstboot:
    - /usr/sbin/systemsetup -setremotelogin on
  boot:
    - brew install git
`))
	require.NoError(t, err)
	require.Len(t, cfg.Commands.Phases["firstboot"], 1)
	require.Len(t, cfg.Commands.Phases["boot"], 1)
	assert.Equal(t, "/usr/sbin/systemsetup -setremotelogin on", cfg.Commands.Phases["firstboot"][0].Cmd)
	assert.Equal(t, "brew install git", cfg.Commands.Phases["boot"][0].Cmd)
}

func TestParse_CommandsStructured(t *testing.T) {
	cfg, err := Parse([]byte(`
commands:
  boot:
    - cmd: long-task.sh
      timeout: 10m
      retries: 2
      valid-exit-codes: [0, 1]
`))
	require.NoError(t, err)
	require.Len(t, cfg.Commands.Phases["boot"], 1)
	entry := cfg.Commands.Phases["boot"][0]
	assert.Equal(t, "long-task.sh", entry.Cmd)
	assert.Equal(t, 2, entry.Retries)
	assert.Equal(t, []int{0, 1}, entry.ValidExitCodes)
}

func TestParse_Recovery(t *testing.T) {
	cfg, err := Parse([]byte(`
from: macos/sonoma
recovery: true
`))
	require.NoError(t, err)
	assert.True(t, cfg.Recovery)
	assert.Equal(t, "macos/sonoma", cfg.From)
}

func TestValidate_RecoveryRejectsFirstboot(t *testing.T) {
	cfg := &Config{
		Recovery: true,
		Commands: commandsField{Phases: map[string][]CommandEntry{
			"firstboot": {{Cmd: "echo nope"}},
		}},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recovery mode only supports boot phase")
}

func TestValidate_UnknownPhase(t *testing.T) {
	cfg := &Config{
		Commands: commandsField{Phases: map[string][]CommandEntry{
			"oobe": {{Cmd: "echo nope"}},
		}},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command phase")
}

func TestValidate_Hostname(t *testing.T) {
	tests := []struct {
		hostname string
		wantErr  bool
	}{
		{"mackit", false},
		{"my-vm", false},
		{"a", false},
		{"-bad", true},
		{"bad-", true},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			cfg := &Config{Hostname: tt.hostname, Commands: commandsField{Phases: map[string][]CommandEntry{}}}
			err := cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_DuplicatePorts(t *testing.T) {
	cfg := &Config{
		Ports: &PortsConfig{SSH: 20022, VNC: 20022},
		Commands: commandsField{Phases: map[string][]CommandEntry{}},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both set to 20022")
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "mackit.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("from: macos/sequoia\n"), 0o644))

	found, err := Discover(dir)
	require.NoError(t, err)
	assert.Equal(t, yamlPath, found)
}

func TestDiscover_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Discover(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mackit.yaml")
}

func TestDiscover_YML(t *testing.T) {
	dir := t.TempDir()
	ymlPath := filepath.Join(dir, "mackit.yml")
	require.NoError(t, os.WriteFile(ymlPath, []byte("from: macos/sequoia\n"), 0o644))

	found, err := Discover(dir)
	require.NoError(t, err)
	assert.Equal(t, ymlPath, found)
}
