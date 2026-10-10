package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
)

const CatalogURL = "https://swscan.apple.com/content/catalogs/others/index-15-14-13-12-10.16-10.15-10.14-10.13-10.12-10.11-10.10-10.9-mountainlion-lion-snowleopard-leopard.merged-1.sucatalog"

var versionNames = map[string]string{
	"goldengate":  "27",
	"tahoe":       "26",
	"sequoia":     "15",
	"sonoma":      "14",
	"ventura":     "13",
	"monterey":    "12",
	"bigsur":      "11",
	"catalina":    "10.15",
	"mojave":      "10.14",
	"highsierra":  "10.13",
}

type CatalogProduct struct {
	ID                  string
	Version             string
	Build               string
	Title               string
	BaseSystemURL       string
	BaseSystemSize      int64
	InstallAssistantURL string
	InstallAssistantSize int64
	DistURL             string
}

func (p *CatalogProduct) InstallerURL() string {
	if p.BaseSystemURL != "" {
		return p.BaseSystemURL
	}
	return p.InstallAssistantURL
}

func (p *CatalogProduct) NeedsExtraction() bool {
	return p.BaseSystemURL == "" && p.InstallAssistantURL != ""
}

func FetchCatalog(ctx context.Context) ([]CatalogProduct, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CatalogURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Apple catalog: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching Apple catalog: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Apple catalog: %w", err)
	}

	root, err := decodePlist(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parsing Apple catalog plist: %w", err)
	}

	rootDict := plistDict(root)
	if rootDict == nil {
		return nil, fmt.Errorf("Apple catalog: root is not a dict")
	}

	products := plistDict(rootDict["Products"])
	if products == nil {
		return nil, fmt.Errorf("Apple catalog: no Products dict")
	}

	var results []CatalogProduct
	for productID, pVal := range products {
		product := plistDict(pVal)
		if product == nil {
			continue
		}

		packages := plistArray(product["Packages"])
		var baseURL, iaURL string
		var baseSize, iaSize int64
		for _, pkgVal := range packages {
			pkg := plistDict(pkgVal)
			if pkg == nil {
				continue
			}
			url := plistString(pkg["URL"])
			size := plistInt(pkg["Size"])
			if strings.HasSuffix(url, "BaseSystem.dmg") {
				baseURL = url
				baseSize = size
			}
			if strings.HasSuffix(url, "InstallAssistant.pkg") {
				iaURL = url
				iaSize = size
			}
		}
		if baseURL == "" && iaURL == "" {
			continue
		}

		var distURL string
		dists := plistDict(product["Distributions"])
		if dists != nil {
			if d := plistString(dists["English"]); d != "" {
				distURL = d
			} else {
				for _, v := range dists {
					if s := plistString(v); s != "" {
						distURL = s
						break
					}
				}
			}
		}

		results = append(results, CatalogProduct{
			ID:                   productID,
			BaseSystemURL:        baseURL,
			BaseSystemSize:       baseSize,
			InstallAssistantURL:  iaURL,
			InstallAssistantSize: iaSize,
			DistURL:              distURL,
		})
	}

	if err := enrichProductVersions(ctx, results); err != nil {
		return results, err
	}

	return results, nil
}

var distVersionRe = regexp.MustCompile(`<key>VERSION</key>\s*<string>([^<]+)</string>`)
var distBuildRe = regexp.MustCompile(`<key>BUILD</key>\s*<string>([^<]+)</string>`)
var distTitleRe = regexp.MustCompile(`<key>TITLE</key>\s*<string>([^<]+)</string>`)

func enrichProductVersions(ctx context.Context, products []CatalogProduct) error {
	for i := range products {
		if products[i].DistURL == "" {
			continue
		}
		ver, build, title, err := fetchDistInfo(ctx, products[i].DistURL)
		if err != nil {
			continue
		}
		products[i].Version = ver
		products[i].Build = build
		products[i].Title = title
	}
	return nil
}

func fetchDistInfo(ctx context.Context, url string) (version, build, title string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", err
	}

	text := string(body)
	if m := distVersionRe.FindStringSubmatch(text); len(m) > 1 {
		version = m[1]
	}
	if m := distBuildRe.FindStringSubmatch(text); len(m) > 1 {
		build = m[1]
	}
	if m := distTitleRe.FindStringSubmatch(text); len(m) > 1 {
		title = m[1]
	}
	return version, build, title, nil
}

func FindProduct(products []CatalogProduct, versionName string) *CatalogProduct {
	majorVer := VersionNumber(versionName)
	if majorVer == "" {
		return nil
	}

	var best *CatalogProduct
	for i := range products {
		p := &products[i]
		if p.Version == "" {
			continue
		}
		if !strings.HasPrefix(p.Version, majorVer+".") && p.Version != majorVer {
			continue
		}
		if best == nil || p.Version > best.Version {
			best = p
		}
	}
	return best
}

func ListVersions(products []CatalogProduct) map[string][]CatalogProduct {
	byMajor := make(map[string][]CatalogProduct)
	for _, p := range products {
		if p.Version == "" {
			continue
		}
		parts := strings.SplitN(p.Version, ".", 2)
		major := parts[0]
		byMajor[major] = append(byMajor[major], p)
	}
	return byMajor
}

func ParseFrom(from string) (version, arch string, err error) {
	rest, ok := strings.CutPrefix(from, "macos/")
	if !ok {
		return "", "", fmt.Errorf("from %q: expected macos/<version>[-<arch>]", from)
	}

	if idx := strings.LastIndex(rest, "-"); idx > 0 {
		candidate := rest[idx+1:]
		if candidate == "arm64" || candidate == "x86_64" || candidate == "amd64" {
			version = rest[:idx]
			arch = candidate
			if arch == "amd64" {
				arch = "x86_64"
			}
			return version, arch, validateVersion(version)
		}
	}

	version = rest
	arch = runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	return version, arch, validateVersion(version)
}

func validateVersion(version string) error {
	if _, ok := versionNames[version]; ok {
		return nil
	}
	known := make([]string, 0, len(versionNames))
	for k := range versionNames {
		known = append(known, k)
	}
	return fmt.Errorf("unknown macOS version %q (known: %s)", version, strings.Join(known, ", "))
}

func VersionNumber(name string) string {
	if n, ok := versionNames[name]; ok {
		return n
	}
	return ""
}

func VersionName(majorVersion string) string {
	for name, ver := range versionNames {
		if ver == majorVersion {
			return name
		}
	}
	return ""
}

// VersionNameForVersion maps a full version string like "15.8.1" or "10.15.7"
// to its codename ("sequoia", "catalina").
func VersionNameForVersion(version string) string {
	for name, prefix := range versionNames {
		if strings.HasPrefix(version, prefix+".") || version == prefix {
			return name
		}
	}
	return ""
}
