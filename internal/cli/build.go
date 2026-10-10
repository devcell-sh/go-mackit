package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	mackit "github.com/devcell-sh/go-mackit"
	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/build/buildopts"
	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/installer"
	"github.com/devcell-sh/go-mackit/internal/config"
	"github.com/devcell-sh/go-mackit/media"
)

var buildAccels = []string{"hvf", "kvm", "tcg"}

func newBuildCmd() *cobra.Command {
	var (
		configFile      string
		recoveryFlag    bool
		fromFlag        string
		accel           string
		vncDisplay      int
		noCache         bool
		cacheDir        string
		force           bool
		openCoreVersion string
	)

	cmd := &cobra.Command{
		Use:   "build [dest]",
		Short: "Build a bootable macOS image",
		Long: "Builds a bootable macOS image. Configuration is loaded from\n" +
			"mackit.yaml (or -f path), with CLI flags as overrides.\n\n" +
			"Build modes:\n" +
			"  (default)    full disk install (~40GB qcow2, persistent OS)\n" +
			"  --recovery   macOS Recovery environment only",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			debug, _ := cmd.Flags().GetBool("debug")
			logger := newLogger(debug)

			cfg, cfgPath, err := loadBuildConfig(configFile)
			if err != nil {
				return err
			}
			if cfgPath != "" {
				logger.Debug("loaded config", "path", cfgPath)
			}

			if cmd.Flags().Changed("from") {
				cfg.From = fromFlag
			}
			if cmd.Flags().Changed("recovery") {
				cfg.Recovery = recoveryFlag
			}
			if cmd.Flags().Changed("opencore-version") {
				if cfg.OpenCore == nil {
					cfg.OpenCore = &config.OpenCoreConfig{}
				}
				cfg.OpenCore.Version = openCoreVersion
			}

			if err := cfg.Validate(); err != nil {
				return err
			}

			opts, err := cfg.ToBuildOpts()
			if err != nil {
				return err
			}

			if accel != "" {
				valid := false
				base := accel
				if i := strings.IndexByte(base, ','); i >= 0 {
					base = base[:i]
				}
				for _, a := range buildAccels {
					if a == base {
						valid = true
						break
					}
				}
				if !valid {
					return fmt.Errorf("accelerator %q is not valid (want %s)",
						accel, strings.Join(buildAccels, ", "))
				}
			}

			displayType := ""
			if vncDisplay >= 0 {
				displayType = fmt.Sprintf("vnc=:%d", vncDisplay)
				logger.Info("VNC server enabled", "display", vncDisplay, "port", 5900+vncDisplay)
			}

			stage := string(opts.Stage())

			dest := "./"
			if len(args) == 1 {
				dest = args[0]
			}
			if info, statErr := os.Stat(dest); (statErr == nil && info.IsDir()) || strings.HasSuffix(dest, "/") {
				dest = filepath.Join(dest, "mackit-"+stage+".qcow2")
			}

			if _, statErr := os.Stat(dest); statErr == nil && !force {
				return fmt.Errorf("%s exists; use --force to overwrite", dest)
			}

			if cacheDir == "" {
				cacheDir = cache.Dir()
			}

			// OpenCore boot requires x86_64 guest.
			guestArch := "x86_64"

			var installerDMG string
			fromKind := buildopts.DetectFrom(opts.From)
			switch fromKind {
			case buildopts.FromDMG:
				installerDMG = opts.From
				if err := installer.PrepareInstall(installerDMG); err != nil {
					return err
				}
			case buildopts.FromIPSW:
				extracted, err := installer.ExtractFromIPSW(opts.From, cacheDir)
				if err != nil {
					return err
				}
				installerDMG = extracted
			default:
				version, _, err := media.ParseFrom(opts.From)
				if err != nil {
					return err
				}
				res, err := media.FetchInstallerDMG(cmd.Context(), media.FetchOptions{
					CacheDir: cacheDir,
					Version:  version,
					Arch:     guestArch,
					Logger:   logger,
				})
				if err != nil {
					return err
				}
				installerDMG = res.Path
			}

			openCoreImg, err := installer.EnsureOpenCore(cmd.Context(), installer.OpenCoreConfig{
				Version:  opts.OpenCoreVersion,
				Arch:     guestArch,
				CacheDir: cacheDir,
				Logger:   logger,
			})
			if err != nil {
				return err
			}

			workDir, err := os.MkdirTemp("", "mackit-build-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(workDir)

			buildCfg := build.Config{
				Dest:         dest,
				CacheDir:     cacheDir,
				InstallerDMG: installerDMG,
				OpenCoreImg:  openCoreImg,
				WorkDir:      workDir,
				Logger:       logger,
				NoCache:      noCache,
				Accel:        accel,
				DisplayType:  displayType,
				GuestArch:    guestArch,
				Opts:         opts,
			}

			if err := mackit.Build(cmd.Context(), buildCfg); err != nil {
				return err
			}

			info, _ := os.Stat(dest)
			sz := float64(0)
			if info != nil {
				sz = float64(info.Size()) / (1024 * 1024)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%.0f MB) -- boot with: mackit start %s\n", dest, sz, dest)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configFile, "file", "f", "", "config file path (default: ./mackit.yaml)")
	cmd.Flags().BoolVar(&recoveryFlag, "recovery", false, "macOS Recovery environment only")
	cmd.Flags().StringVar(&fromFlag, "from", "", "media source: macos/<version>[-<arch>], ./path.dmg, or ./path.ipsw")
	cmd.Flags().StringVar(&accel, "accel", "", "QEMU accelerator: hvf, kvm, or tcg")
	cmd.Flags().IntVar(&vncDisplay, "vnc", -1, "start a VNC server on display :N (port 5900+N)")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "redownload cached media")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: "+cache.DirEnv+")")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing image without asking")
	cmd.Flags().StringVar(&openCoreVersion, "opencore-version", "", "OpenCore EFI version (default: "+installer.DefaultOpenCoreVersion+")")
	return cmd
}

func loadBuildConfig(path string) (*config.Config, string, error) {
	if path != "" {
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			resolved, discoverErr := config.Discover(path)
			if discoverErr != nil {
				return nil, "", discoverErr
			}
			path = resolved
		}
		cfg, err := config.Load(path)
		if err != nil {
			return nil, "", err
		}
		return cfg, path, nil
	}

	discovered, err := config.Discover(".")
	if err != nil {
		return &config.Config{}, "", nil
	}
	cfg, err := config.Load(discovered)
	if err != nil {
		return nil, "", err
	}
	return cfg, discovered, nil
}

func archFromFrom(from string) string {
	if strings.Contains(from, "x86_64") || strings.Contains(from, "amd64") {
		return "x86_64"
	}
	return "arm64"
}
