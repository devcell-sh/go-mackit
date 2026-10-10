package installer

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ConvertDMGToRaw converts an Apple UDIF DMG to a raw disk image.
// Tries dmg2img first (handles all UDIF chunk types), then hdiutil on macOS,
// then qemu-img as last resort. qemu-img's DMG driver has a hard chunk sector
// limit (131072) that newer macOS DMGs exceed.
func ConvertDMGToRaw(dmgPath, rawPath string) (string, error) {
	if info, err := os.Stat(rawPath); err == nil && info.Size() > 1024*1024 {
		return rawPath, nil
	}

	converters := []struct {
		name string
		fn   func(src, dst string) error
	}{
		{"dmg2img", convertWithDmg2img},
		{"hdiutil", convertWithHdiutil},
		{"qemu-img", convertWithQemuImg},
	}

	var lastErr error
	for _, c := range converters {
		if err := c.fn(dmgPath, rawPath); err != nil {
			lastErr = fmt.Errorf("%s: %w", c.name, err)
			continue
		}
		info, err := os.Stat(rawPath)
		if err != nil {
			return "", fmt.Errorf("converted image not found: %w", err)
		}
		if info.Size() < 1024*1024 {
			os.Remove(rawPath)
			lastErr = fmt.Errorf("%s: converted image too small (%d bytes)", c.name, info.Size())
			continue
		}
		return rawPath, nil
	}
	return "", fmt.Errorf("all DMG converters failed (install dmg2img): %w", lastErr)
}

func convertWithDmg2img(src, dst string) error {
	bin, err := exec.LookPath("dmg2img")
	if err != nil {
		return fmt.Errorf("not found in PATH")
	}
	cmd := exec.Command(bin, src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func convertWithHdiutil(src, dst string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("hdiutil only available on macOS")
	}
	bin, err := exec.LookPath("hdiutil")
	if err != nil {
		return fmt.Errorf("not found in PATH")
	}
	cmd := exec.Command(bin, "convert", src, "-format", "UDTO", "-o", dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	// hdiutil appends .cdr extension
	cdrPath := dst + ".cdr"
	if _, err := os.Stat(cdrPath); err == nil {
		return os.Rename(cdrPath, dst)
	}
	return nil
}

func convertWithQemuImg(src, dst string) error {
	bin, err := exec.LookPath("qemu-img")
	if err != nil {
		return fmt.Errorf("not found in PATH")
	}
	cmd := exec.Command(bin, "convert", "-f", "dmg", "-O", "raw", src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// IsUDIFDMG checks if a file is an Apple UDIF DMG by looking for the "koly"
// trailer magic at the end of the file.
func IsUDIFDMG(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < 512 {
		return false
	}

	buf := make([]byte, 4)
	if _, err := f.ReadAt(buf, info.Size()-512); err != nil {
		return false
	}
	return string(buf) == "koly"
}
