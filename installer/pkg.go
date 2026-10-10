package installer

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func ExtractBaseSystemFromPkg(pkgPath, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}

	dest := filepath.Join(destDir, "BaseSystem.dmg")

	if runtime.GOOS == "darwin" {
		return extractWithPkgutil(pkgPath, destDir, dest)
	}

	if path, err := exec.LookPath("xar"); err == nil {
		return extractWithXar(path, pkgPath, destDir, dest)
	}

	if path, err := exec.LookPath("7z"); err == nil {
		return extractWith7z(path, pkgPath, destDir, dest)
	}

	if path, err := exec.LookPath("bsdtar"); err == nil {
		return extractWithBsdtar(path, pkgPath, destDir, dest)
	}

	return "", fmt.Errorf("cannot extract BaseSystem.dmg from InstallAssistant.pkg: "+
		"install xar, p7zip (7z), or bsdtar, or run on macOS.\n"+
		"Alternatively, provide a local BaseSystem.dmg via: from: ./path/to/BaseSystem.dmg")
}

func extractWithPkgutil(pkgPath, workDir, dest string) (string, error) {
	expandDir := filepath.Join(workDir, "pkg-expanded")
	os.RemoveAll(expandDir)

	cmd := exec.Command("pkgutil", "--expand-full", pkgPath, expandDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pkgutil --expand-full: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Look for the actual BaseSystem.dmg (~500MB) first.
	baseSystem := findFileRecursive(expandDir, "BaseSystem.dmg")
	if baseSystem != "" {
		if err := copyFile(baseSystem, dest); err != nil {
			return "", fmt.Errorf("copying BaseSystem.dmg: %w", err)
		}
		os.RemoveAll(expandDir)
		return dest, nil
	}

	// macOS 11+: BaseSystem.dmg is inside SharedSupport.dmg (~15GB).
	sharedSupport := findFileRecursive(expandDir, "SharedSupport.dmg")
	if sharedSupport != "" {
		result, err := extractBaseSystemFromSharedSupport(sharedSupport, dest)
		os.RemoveAll(expandDir)
		if err != nil {
			return "", fmt.Errorf("extracting from SharedSupport.dmg: %w", err)
		}
		return result, nil
	}

	os.RemoveAll(expandDir)
	return "", fmt.Errorf("BaseSystem.dmg not found inside InstallAssistant.pkg")
}

func extractWithXar(xarPath, pkgPath, workDir, dest string) (string, error) {
	extractDir := filepath.Join(workDir, "pkg-xar")
	os.RemoveAll(extractDir)
	os.MkdirAll(extractDir, 0o755)

	cmd := exec.Command(xarPath, "-xf", pkgPath, "-C", extractDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("xar extract pkg: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Check for BaseSystem.dmg directly first (older macOS).
	baseSystem := findFileRecursive(extractDir, "BaseSystem.dmg")
	if baseSystem != "" {
		if err := copyFile(baseSystem, dest); err != nil {
			return "", fmt.Errorf("copying BaseSystem.dmg: %w", err)
		}
		os.RemoveAll(extractDir)
		return dest, nil
	}

	// macOS 11+: BaseSystem.dmg is inside SharedSupport.dmg.
	sharedSupport := findFileRecursive(extractDir, "SharedSupport.dmg")
	if sharedSupport != "" {
		result, err := extractBaseSystemFromSharedSupport(sharedSupport, dest)
		os.RemoveAll(extractDir)
		if err != nil {
			return "", fmt.Errorf("extracting from SharedSupport.dmg: %w", err)
		}
		return result, nil
	}

	os.RemoveAll(extractDir)
	return "", fmt.Errorf("BaseSystem.dmg or SharedSupport.dmg not found inside InstallAssistant.pkg (via xar)")
}

func extractWith7z(sevenZ, pkgPath, workDir, dest string) (string, error) {
	extractDir := filepath.Join(workDir, "pkg-7z")
	os.RemoveAll(extractDir)

	cmd := exec.Command(sevenZ, "x", "-o"+extractDir, pkgPath, "-y")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("7z extract pkg: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// The pkg contains a Payload file (gzip'd cpio).
	payloadPath := filepath.Join(extractDir, "Payload")
	if _, err := os.Stat(payloadPath); err == nil {
		payloadDir := filepath.Join(workDir, "pkg-payload")
		os.RemoveAll(payloadDir)
		cmd = exec.Command(sevenZ, "x", "-o"+payloadDir, payloadPath, "-y")
		out, err = cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("7z extract payload: %s: %w", strings.TrimSpace(string(out)), err)
		}

		candidates := []string{
			filepath.Join(payloadDir, "SharedSupport", "BaseSystem.dmg"),
			filepath.Join(payloadDir, "Applications", "Install macOS*.app", "Contents", "SharedSupport", "BaseSystem.dmg"),
		}
		if found := findFirstGlob(candidates); found != "" {
			if err := copyFile(found, dest); err != nil {
				return "", err
			}
			os.RemoveAll(extractDir)
			os.RemoveAll(payloadDir)
			return dest, nil
		}
		os.RemoveAll(payloadDir)
	}

	os.RemoveAll(extractDir)
	return "", fmt.Errorf("BaseSystem.dmg not found inside InstallAssistant.pkg (via 7z)")
}

func extractWithBsdtar(bsdtar, pkgPath, workDir, dest string) (string, error) {
	extractDir := filepath.Join(workDir, "pkg-tar")
	os.RemoveAll(extractDir)
	os.MkdirAll(extractDir, 0o755)

	cmd := exec.Command(bsdtar, "-xf", pkgPath, "-C", extractDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bsdtar extract pkg: %s: %w", strings.TrimSpace(string(out)), err)
	}

	payloadPath := filepath.Join(extractDir, "Payload")
	if _, err := os.Stat(payloadPath); err == nil {
		payloadDir := filepath.Join(workDir, "pkg-payload")
		os.RemoveAll(payloadDir)
		os.MkdirAll(payloadDir, 0o755)
		cmd = exec.Command(bsdtar, "-xf", payloadPath, "-C", payloadDir)
		if out, err = cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("bsdtar extract payload: %s: %w", strings.TrimSpace(string(out)), err)
		}
		candidates := []string{
			filepath.Join(payloadDir, "SharedSupport", "BaseSystem.dmg"),
		}
		if found := findFirst(candidates); found != "" {
			if err := copyFile(found, dest); err != nil {
				return "", err
			}
			os.RemoveAll(extractDir)
			os.RemoveAll(payloadDir)
			return dest, nil
		}
		os.RemoveAll(payloadDir)
	}

	os.RemoveAll(extractDir)
	return "", fmt.Errorf("BaseSystem.dmg not found inside InstallAssistant.pkg (via bsdtar)")
}

func extractBaseSystemFromSharedSupport(sharedSupportPath, dest string) (string, error) {
	// Try 7z first: can extract individual files from DMG images.
	if sevenZ, err := exec.LookPath("7z"); err == nil {
		dir := filepath.Dir(dest)
		ssDir := filepath.Join(dir, "ss-extract")
		os.RemoveAll(ssDir)
		cmd := exec.Command(sevenZ, "x", "-o"+ssDir, sharedSupportPath, "BaseSystem.dmg", "-y")
		out, err := cmd.CombinedOutput()
		if err == nil {
			bsd := findFileRecursive(ssDir, "BaseSystem.dmg")
			if bsd != "" {
				if err := copyFile(bsd, dest); err == nil {
					os.RemoveAll(ssDir)
					return dest, nil
				}
			}
		} else {
			_ = out
		}
		os.RemoveAll(ssDir)
	}

	// On macOS, mount SharedSupport.dmg and copy BaseSystem.dmg out.
	if runtime.GOOS == "darwin" {
		if hdiutil, err := exec.LookPath("hdiutil"); err == nil {
			mountPoint := filepath.Join(filepath.Dir(dest), "ss-mount")
			os.MkdirAll(mountPoint, 0o755)
			defer func() {
				exec.Command(hdiutil, "detach", mountPoint, "-force").Run()
				os.RemoveAll(mountPoint)
			}()
			cmd := exec.Command(hdiutil, "attach", sharedSupportPath,
				"-mountpoint", mountPoint, "-nobrowse", "-readonly")
			if out, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("hdiutil attach SharedSupport: %s: %w",
					strings.TrimSpace(string(out)), err)
			}
			bsd := findFileRecursive(mountPoint, "BaseSystem.dmg")
			if bsd != "" {
				if err := copyFile(bsd, dest); err != nil {
					return "", err
				}
				return dest, nil
			}
			return "", fmt.Errorf("BaseSystem.dmg not found inside mounted SharedSupport.dmg")
		}
	}

	return "", fmt.Errorf("could not extract BaseSystem.dmg from SharedSupport.dmg "+
		"(install 7z, or run on macOS)")
}

func findFirst(paths []string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func findFirstGlob(patterns []string) string {
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err == nil && len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func findFileRecursive(root, name string) string {
	var found string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
