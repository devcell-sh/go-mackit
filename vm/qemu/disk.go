package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func CreateDisk(path string, sizeGB int) error {
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img create: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func createRawDisk(path string, sizeGB int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(sizeGB) * 1024 * 1024 * 1024); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func CreateOverlay(path, base string) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", absBase, "-F", "qcow2", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img create overlay: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func FlattenQcow2(src, dst string) error {
	cmd := exec.Command("qemu-img", "convert", "-O", "qcow2", src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img convert: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func IsImageLocked(path string) bool {
	cmd := exec.Command("qemu-img", "info", path)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "lock") {
		return true
	}
	return false
}
