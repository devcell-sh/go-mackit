package installer

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func PrepareInstall(installerDMG string) error {
	info, err := os.Stat(installerDMG)
	if err != nil {
		return fmt.Errorf("installer media not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("installer media %s is a directory, expected a file", installerDMG)
	}
	if info.Size() < 1024*1024 {
		return fmt.Errorf("installer media %s is too small (%d bytes)", installerDMG, info.Size())
	}
	return nil
}

var ipswDMGPaths = []string{
	"AssetData/Restore/BaseSystem.dmg",
	"Restore/BaseSystem.dmg",
	"BaseSystem.dmg",
}

func ExtractFromIPSW(ipswPath, destDir string) (string, error) {
	r, err := zip.OpenReader(ipswPath)
	if err != nil {
		return "", fmt.Errorf("opening IPSW %s: %w", ipswPath, err)
	}
	defer r.Close()

	for _, candidate := range ipswDMGPaths {
		for _, f := range r.File {
			if strings.EqualFold(f.Name, candidate) {
				dest := filepath.Join(destDir, "BaseSystem.dmg")
				if err := extractZipFile(f, dest); err != nil {
					return "", fmt.Errorf("extracting %s from IPSW: %w", f.Name, err)
				}
				return dest, nil
			}
		}
	}

	return "", fmt.Errorf("no BaseSystem.dmg found in IPSW %s", ipswPath)
}

func extractZipFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
