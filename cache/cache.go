package cache

import (
	"os"
	"path/filepath"
)

const DirEnv = "MACKIT_CACHE_DIR"

func Dir() string {
	if d := os.Getenv(DirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "mackit-cache")
	}
	return filepath.Join(home, ".cache", "mackit")
}

type Config struct {
	Dir string
}

func (c Config) Resolve() string {
	if c.Dir != "" {
		return c.Dir
	}
	return Dir()
}

func (c Config) InstallerDMG() string {
	return filepath.Join(c.Resolve(), "BaseSystem.dmg")
}

func (c Config) OpenCoreImg() string {
	return filepath.Join(c.Resolve(), "OpenCore.qcow2")
}
