package media

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/devcell-sh/go-mackit/installer"
)

type FetchOptions struct {
	CacheDir string
	Version  string
	Arch     string
	NoCache  bool
	Logger   *slog.Logger
}

type FetchResult struct {
	Path    string
	Version string
	Build   string
	Arch    string
}

func FetchInstallerDMG(ctx context.Context, opts FetchOptions) (*FetchResult, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	version := opts.Version
	if version == "" {
		version = "sequoia"
	}
	arch := opts.Arch
	if arch == "" {
		arch = "arm64"
	}

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache", "mackit")
	}

	cachedPath := filepath.Join(cacheDir, fmt.Sprintf("BaseSystem-%s-%s.dmg", version, arch))
	if !opts.NoCache {
		if info, err := os.Stat(cachedPath); err == nil && info.Size() > 1024*1024 {
			logger.Info("using cached installer", "path", cachedPath)
			return &FetchResult{
				Path:    cachedPath,
				Version: version,
				Arch:    arch,
			}, nil
		}
	}

	logger.Info("fetching Apple software update catalog")
	products, err := FetchCatalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching catalog: %w", err)
	}

	product := FindProduct(products, version)
	if product == nil {
		available := listAvailable(products)
		return nil, fmt.Errorf("no macOS %s installer found in Apple catalog.\n\nAvailable versions:\n%s",
			version, available)
	}

	logger.Info("found installer",
		"version", product.Version,
		"build", product.Build,
		"title", product.Title,
	)

	if product.BaseSystemURL != "" {
		logger.Info("downloading BaseSystem.dmg", "url", product.BaseSystemURL)
		if err := DownloadFile(ctx, product.BaseSystemURL, cachedPath, logger); err != nil {
			return nil, fmt.Errorf("downloading BaseSystem.dmg: %w", err)
		}
	} else if _, ok := recoveryBoardIDs[version]; ok {
		// macOS 11+: use Apple's Internet Recovery endpoint to download a
		// small (~500-800 MB) BaseSystem.dmg instead of the 15 GB
		// InstallAssistant.pkg.
		logger.Info("fetching recovery image via osrecovery.apple.com", "version", version)
		if err := FetchRecoveryImage(ctx, version, cachedPath, logger); err != nil {
			return nil, fmt.Errorf("fetching recovery image: %w", err)
		}
	} else if product.InstallAssistantURL != "" {
		pkgPath := filepath.Join(cacheDir, fmt.Sprintf("InstallAssistant-%s.pkg", product.Version))
		if !opts.NoCache {
			if info, err := os.Stat(pkgPath); err == nil && info.Size() > 1024*1024 {
				logger.Info("using cached InstallAssistant.pkg", "path", pkgPath)
			} else {
				logger.Info("downloading InstallAssistant.pkg",
					"url", product.InstallAssistantURL,
					"size_gb", fmt.Sprintf("%.1f", float64(product.InstallAssistantSize)/(1024*1024*1024)),
				)
				if err := DownloadFile(ctx, product.InstallAssistantURL, pkgPath, logger); err != nil {
					return nil, fmt.Errorf("downloading InstallAssistant.pkg: %w", err)
				}
			}
		} else {
			logger.Info("downloading InstallAssistant.pkg",
				"url", product.InstallAssistantURL,
				"size_gb", fmt.Sprintf("%.1f", float64(product.InstallAssistantSize)/(1024*1024*1024)),
			)
			if err := DownloadFile(ctx, product.InstallAssistantURL, pkgPath, logger); err != nil {
				return nil, fmt.Errorf("downloading InstallAssistant.pkg: %w", err)
			}
		}

		logger.Info("extracting BaseSystem.dmg from InstallAssistant.pkg")
		extractDir := filepath.Join(cacheDir, "pkg-extract")
		dmgPath, err := installer.ExtractBaseSystemFromPkg(pkgPath, extractDir)
		if err != nil {
			return nil, fmt.Errorf("extracting BaseSystem.dmg: %w", err)
		}

		if dmgPath != cachedPath {
			if err := os.Rename(dmgPath, cachedPath); err != nil {
				in, readErr := os.Open(dmgPath)
				if readErr != nil {
					return nil, fmt.Errorf("reading extracted dmg: %w", readErr)
				}
				out, wErr := os.Create(cachedPath)
				if wErr != nil {
					in.Close()
					return nil, fmt.Errorf("creating cached dmg: %w", wErr)
				}
				_, cpErr := io.Copy(out, in)
				in.Close()
				out.Close()
				if cpErr != nil {
					return nil, fmt.Errorf("copying cached dmg: %w", cpErr)
				}
			}
		}
		os.RemoveAll(extractDir)
	} else {
		return nil, fmt.Errorf("no download URL found for macOS %s", version)
	}

	return &FetchResult{
		Path:    cachedPath,
		Version: product.Version,
		Build:   product.Build,
		Arch:    arch,
	}, nil
}

func listAvailable(products []CatalogProduct) string {
	byMajor := ListVersions(products)
	var lines []string
	for major, prods := range byMajor {
		name := VersionName(major)
		if name == "" {
			name = "macOS " + major
		}
		var latest string
		for _, p := range prods {
			if p.Version > latest {
				latest = p.Version
			}
		}
		mediaType := "BaseSystem.dmg"
		if len(prods) > 0 && prods[0].NeedsExtraction() {
			mediaType = "InstallAssistant.pkg"
		}
		lines = append(lines, fmt.Sprintf("  macOS %s (%s): %s [%s]", name, major, latest, mediaType))
	}
	if len(lines) == 0 {
		return "  (none found)"
	}
	return joinLines(lines)
}

func joinLines(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

func ChecksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
