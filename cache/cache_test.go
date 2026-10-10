package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDir_Default(t *testing.T) {
	t.Setenv("MACKIT_CACHE_DIR", "")
	d := Dir()
	assert.Contains(t, d, "mackit")
}

func TestDir_EnvOverride(t *testing.T) {
	t.Setenv("MACKIT_CACHE_DIR", "/custom/cache")
	assert.Equal(t, "/custom/cache", Dir())
}

func TestConfig_Resolve(t *testing.T) {
	c := Config{Dir: "/explicit/path"}
	assert.Equal(t, "/explicit/path", c.Resolve())
}

func TestConfig_ResolveDefault(t *testing.T) {
	t.Setenv("MACKIT_CACHE_DIR", "")
	c := Config{}
	d := c.Resolve()
	assert.Contains(t, d, "mackit")
}

func TestConfig_InstallerDMG(t *testing.T) {
	c := Config{Dir: "/cache"}
	assert.Equal(t, filepath.Join("/cache", "BaseSystem.dmg"), c.InstallerDMG())
}

func TestConfig_OpenCoreImg(t *testing.T) {
	c := Config{Dir: "/cache"}
	assert.Equal(t, filepath.Join("/cache", "OpenCore.qcow2"), c.OpenCoreImg())
}

func TestDir_NoHome(t *testing.T) {
	t.Setenv("MACKIT_CACHE_DIR", "")
	t.Setenv("HOME", "")
	os.Unsetenv("HOME")
	d := Dir()
	assert.NotEmpty(t, d)
}
