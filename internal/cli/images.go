package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/media"
)

type imageEntry struct {
	Image    string
	ID       string
	DiskSize int64
	Status   string
	Source   string
}

func newImagesCmd() *cobra.Command {
	var (
		cacheDir string
		remote   bool
	)

	cmd := &cobra.Command{
		Use:   "images",
		Short: "List local and available macOS images",
		Long:  "Shows locally cached installer media and built images,\nplus available versions from Apple's software update catalog.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cacheDir == "" {
				cacheDir = cache.Dir()
			}

			var entries []imageEntry

			localEntries := scanLocalImages(cacheDir)
			entries = append(entries, localEntries...)

			if remote {
				remoteEntries, err := fetchRemoteImages(cmd.Context(), localEntries)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not fetch Apple catalog: %v\n", err)
				} else {
					entries = append(entries, remoteEntries...)
				}
			}

			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No images found. Run 'mackit build' to fetch installer media.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 3, ' ', 0)
			fmt.Fprintln(w, "IMAGE\tSIZE\tSTATUS\tSOURCE")
			for _, e := range entries {
				size := formatSize(e.DiskSize)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Image, size, e.Status, e.Source)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: "+cache.DirEnv+")")
	cmd.Flags().BoolVarP(&remote, "all", "a", false, "include available remote images from Apple catalog")
	return cmd
}

func scanLocalImages(cacheDir string) []imageEntry {
	var entries []imageEntry

	if _, err := os.Stat(cacheDir); err != nil {
		return entries
	}

	dirEntries, err := os.ReadDir(cacheDir)
	if err != nil {
		return entries
	}

	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		info, err := de.Info()
		if err != nil {
			continue
		}

		switch {
		case strings.HasPrefix(name, "BaseSystem-") && strings.HasSuffix(name, ".dmg") && !strings.HasSuffix(name, ".dmg.raw"):
			tag := parseBaseSystemTag(name)
			entries = append(entries, imageEntry{
				Image:    "apple/macos:" + tag,
				DiskSize: info.Size(),
				Status:   "cached",
				Source:   "apple",
			})
		case strings.HasPrefix(name, "BaseSystem-") && strings.HasSuffix(name, ".dmg.raw"):
			tag := parseBaseSystemTag(strings.TrimSuffix(name, ".raw"))
			entries = append(entries, imageEntry{
				Image:    "apple/macos:" + tag + " (raw)",
				DiskSize: info.Size(),
				Status:   "converted",
				Source:   "local",
			})
		case strings.HasPrefix(name, "OpenCore-") && strings.HasSuffix(name, ".qcow2"):
			version := strings.TrimSuffix(strings.TrimPrefix(name, "OpenCore-"), ".qcow2")
			entries = append(entries, imageEntry{
				Image:    "opencore:" + version,
				DiskSize: info.Size(),
				Status:   "cached",
				Source:   "github",
			})
		case strings.HasPrefix(name, "InstallAssistant-") && strings.HasSuffix(name, ".pkg"):
			version := strings.TrimSuffix(strings.TrimPrefix(name, "InstallAssistant-"), ".pkg")
			entries = append(entries, imageEntry{
				Image:    "apple/macos:installer-" + version,
				DiskSize: info.Size(),
				Status:   "cached",
				Source:   "apple",
			})
		}
	}

	// Scan CWD for built qcow2 images.
	cwd, _ := os.Getwd()
	if cwd != "" {
		matches, _ := filepath.Glob(filepath.Join(cwd, "mackit-*.qcow2"))
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(cwd, m)
			entries = append(entries, imageEntry{
				Image:    rel,
				DiskSize: info.Size(),
				Status:   "built",
				Source:   "local",
			})
		}
	}

	return entries
}

func fetchRemoteImages(ctx context.Context, localEntries []imageEntry) ([]imageEntry, error) {
	products, err := media.FetchCatalog(ctx)
	if err != nil {
		return nil, err
	}

	// Build a set of locally-cached version names for dedup.
	localVersions := make(map[string]bool)
	for _, e := range localEntries {
		// "apple/macos:catalina-x86_64" -> "catalina"
		tag := strings.TrimPrefix(e.Image, "apple/macos:")
		tag = strings.TrimSuffix(tag, " (raw)")
		if idx := strings.IndexByte(tag, '-'); idx > 0 {
			tag = tag[:idx]
		}
		localVersions[tag] = true
	}

	// Group products by codename rather than major version number.
	// This handles the 10.x series (catalina=10.15, mojave=10.14, etc.)
	// where multiple codenames share major "10".
	type versionInfo struct {
		name    string
		version string
		size    int64
		media   string
	}
	seen := make(map[string]bool)
	var versions []versionInfo
	for _, p := range products {
		if p.Version == "" {
			continue
		}
		name := media.VersionNameForVersion(p.Version)
		if name == "" || seen[name] {
			continue
		}
		// Keep only the latest patch per codename.
		var best *media.CatalogProduct
		for i := range products {
			pp := &products[i]
			pn := media.VersionNameForVersion(pp.Version)
			if pn == name && (best == nil || pp.Version > best.Version) {
				best = pp
			}
		}
		if best == nil {
			continue
		}
		seen[name] = true
		size := best.BaseSystemSize
		mediaType := "BaseSystem.dmg"
		if best.NeedsExtraction() {
			size = best.InstallAssistantSize
			mediaType = "InstallAssistant.pkg"
		}
		versions = append(versions, versionInfo{
			name:    name,
			version: best.Version,
			size:    size,
			media:   mediaType,
		})
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].version > versions[j].version
	})

	var entries []imageEntry
	for _, v := range versions {
		if localVersions[v.name] {
			continue
		}
		entries = append(entries, imageEntry{
			Image:    fmt.Sprintf("apple/macos:%s", v.name),
			DiskSize: v.size,
			Status:   fmt.Sprintf("v%s", v.version),
			Source:   "apple",
		})
	}

	return entries, nil
}

func parseBaseSystemTag(filename string) string {
	// BaseSystem-catalina-x86_64.dmg -> catalina-x86_64
	name := strings.TrimPrefix(filename, "BaseSystem-")
	name = strings.TrimSuffix(name, ".dmg")
	return name
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
