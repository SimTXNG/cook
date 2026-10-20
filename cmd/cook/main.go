package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/dev"
	"github.com/simtxng/cook/pkg/generator"
	"github.com/simtxng/cook/pkg/orchestrator"
	"github.com/simtxng/cook/pkg/packager/windows"
	"github.com/simtxng/cook/pkg/wails"
)

const (
	CookVersion = "1.0.0"
)

var (
	cfgPath    string
	targetOS   string
	targetArch string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "cook",
		Short: "Cook - Standalone, multi-platform build and packaging engine for Go & Wails",
		Long: `Cook is a complete replacement for 'wails3 package' and complex Taskfile packaging pipelines.
It provides native multi-binary compilation, single-source-of-truth version synchronization,
lifecycle hooks, AppImage relocation and fix-scripts, pure-Go NFPM packaging (deb, rpm, archlinux),
Void Linux (.xbps) packaging, Windows PE resources (.syso), NSIS installers, and macOS styled DMGs.`,
	}

	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "path to config file (defaults to cook.toml)")
	rootCmd.PersistentFlags().StringVar(&targetOS, "os", runtime.GOOS, "target operating system (linux, windows, darwin)")
	rootCmd.PersistentFlags().StringVar(&targetArch, "arch", runtime.GOARCH, "target architecture (amd64, arm64)")

	// Subcommands
	rootCmd.AddCommand(newPackageCmd())
	rootCmd.AddCommand(newBuildCmd())
	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newDevCmd())
	rootCmd.AddCommand(newGenerateCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newSysoCmd())
	rootCmd.AddCommand(newValidateCmd())
	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newVersionCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func findConfigFile(customPath string) (string, error) {
	if customPath != "" {
		if _, err := os.Stat(customPath); err != nil {
			return "", fmt.Errorf("specified config file not found: %s", customPath)
		}
		return customPath, nil
	}

	candidates := []string{
		"cook.toml",
		"build/cook.toml",
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	// Try globbing *.cook.toml and build/*.cook.toml
	for _, pattern := range []string{"*.cook.toml", "build/*.cook.toml"} {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			return matches[0], nil
		}
	}

	return "", fmt.Errorf("no config file found (searched: %s, *.cook.toml, build/*.cook.toml). Run 'cook init' or specify with --config", strings.Join(candidates, ", "))
}

func loadCfg() (*config.Config, error) {
	configFile, err := findConfigFile(cfgPath)
	if err != nil {
		return nil, err
	}

	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	return cfg, nil
}

func newOrchestrator() *orchestrator.Orchestrator {
	o := orchestrator.NewOrchestrator()
	registerPlatformPackagers(o)
	return o
}

func newPackageCmd() *cobra.Command {
	var skipBuild bool
	cmd := &cobra.Command{
		Use:   "package [targets...]",
		Short: "Package application for the target platform",
		Long: `Packages the application for distribution into deb, rpm, archlinux, void (xbps), appimage, nsis, dmg, etc.
Example:
  cook package
  cook package appimage
  cook package deb rpm void
  cook package --os windows --arch amd64`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}

			orch := newOrchestrator()

			ctx := context.Background()
			artifacts, err := orch.Package(ctx, cfg, targetOS, targetArch, args, skipBuild)
			if err != nil {
				return err
			}

			if len(artifacts) == 0 {
				fmt.Println("No artifacts were generated.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip binary compilation and package existing binaries in output directory")
	return cmd
}

func newBuildCmd() *cobra.Command {
	var skipFrontend bool
	cmd := &cobra.Command{
		Use:   "build [targets or binaries...]",
		Short: "Compile binaries and/or package distribution targets",
		Long: `Compiles the frontend assets and Go binaries, and optionally packages distribution targets.
Examples:
  cook build
  cook build myapp
  cook build linux.appimage
  cook build myapp myapp-cli
  cook build --skip-frontend`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}

			if skipFrontend {
				cfg.Frontend.Enabled = false
			}

			orch := newOrchestrator()
			ctx := context.Background()

			// Check if any arguments are packaging targets (e.g. linux.appimage, appimage, deb, rpm, nsis)
			var packageTargets []string
			var binaryArgs []string
			for _, arg := range args {
				if orchestrator.IsKnownPackagingTarget(arg) {
					packageTargets = append(packageTargets, arg)
				} else {
					binaryArgs = append(binaryArgs, arg)
				}
			}

			// If packaging targets were specified, orchestrator packages them.
			// When binary filters are also present, build those binaries first and
			// then package the existing artifacts so the filter is honored.
			if len(packageTargets) > 0 {
				if len(binaryArgs) > 0 {
					if _, err := orch.Build(ctx, cfg, binaryArgs, targetOS, targetArch); err != nil {
						return err
					}
				}

				artifacts, err := orch.Package(ctx, cfg, targetOS, targetArch, packageTargets, len(binaryArgs) > 0)
				if err != nil {
					return err
				}
				if len(artifacts) == 0 {
					fmt.Println("No artifacts were generated.")
				}
				return nil
			}

			binaries, err := orch.Build(ctx, cfg, binaryArgs, targetOS, targetArch)
			if err != nil {
				return err
			}

			fmt.Printf("Successfully built %d binary(ies):\n", len(binaries))
			for name, path := range binaries {
				fmt.Printf("  * %s -> %s\n", name, path)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&skipFrontend, "skip-frontend", false, "skip building frontend assets")
	return cmd
}

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration and check toolchain prerequisites",
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, err := findConfigFile(cfgPath)
			if err != nil {
				return err
			}

			fmt.Printf("Reading configuration from: %s\n", configFile)
			cfg, err := config.LoadConfig(configFile)
			if err != nil {
				return fmt.Errorf("validation error in %s: %w", configFile, err)
			}

			fmt.Println("\n--- Configuration Summary ---")
			fmt.Printf("  Package Name:       %s\n", cfg.Package.Name)
			fmt.Printf("  Product Name:       %s\n", cfg.Package.ProductName)
			fmt.Printf("  Raw Version:        %s\n", cfg.Version.Raw)
			fmt.Printf("  Debian/RPM Version: %s\n", cfg.Version.Debian)
			fmt.Printf("  Arch Linux Version: %s\n", cfg.Version.Arch)
			fmt.Printf("  Void Linux Version: %s\n", cfg.Version.Void)
			fmt.Printf("  Windows Version:    %s\n", cfg.Version.Windows)
			fmt.Printf("  Git Commit:         %s\n", cfg.Version.Commit)
			fmt.Printf("  Binaries:           %d\n", len(cfg.Binaries))
			for _, b := range cfg.Binaries {
				fmt.Printf("    - %s (GUI: %v, CGO: %v, path: %s)\n", b.Name, b.GUI, b.CGO, b.Path)
			}

			fmt.Println("\n--- Toolchain Diagnostics ---")
			var toolErr error
			if err := checkTool("go", true); err != nil {
				toolErr = err
			}
			checkTool("patchelf", false)
			checkTool("appimagetool", false)
			checkTool("makensis", false)
			checkTool("codesign", false)
			checkTool("create-dmg", false)

			if toolErr != nil {
				return toolErr
			}

			fmt.Println("\nConfiguration is valid!")
			return nil
		},
	}
}

func checkTool(name string, required bool) error {
	path, err := exec.LookPath(name)
	if err == nil {
		fmt.Printf("  [OK] %-14s -> %s\n", name, path)
		return nil
	}
	if required {
		fmt.Printf("  [FAIL] %-12s -> MISSING (strictly required)\n", name)
		return fmt.Errorf("required tool %q not found in PATH", name)
	}
	fmt.Printf("  [OPTIONAL] %-8s -> not found (install if targeting this format)\n", name)
	return nil
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new cook.toml configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "cook.toml"
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("%s already exists", target)
			}

			// Copy from cook.example.toml if exists, else write template
			example := "cook.example.toml"
			if data, err := os.ReadFile(example); err == nil {
				if err := os.WriteFile(target, data, 0644); err != nil {
					return err
				}
				fmt.Printf("Created %s (copied from %s)\n", target, example)
				return nil
			}

			return fmt.Errorf("template %s not found", example)
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print cook version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("cook version %s (%s/%s)\n", CookVersion, runtime.GOOS, runtime.GOARCH)
		},
	}
}

func newSysoCmd() *cobra.Command {
	var iconPath, manifestPath, infoJSONPath, outPath, archFlag string

	cmd := &cobra.Command{
		Use:   "syso",
		Short: "Compile Windows PE resources (.syso) using pure-Go winres",
		Long: `Compiles icon, manifest, and version info into a Windows COFF object (.syso) matching 'wails3 generate syso'.
Example:
  cook syso
  cook syso --icon build/windows/icon.ico --manifest build/windows/wails.exe.manifest --info build/windows/info.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, err := findConfigFile(cfgPath)
			var cfg *config.Config
			switch {
			case err == nil:
				cfg, err = config.LoadConfig(configFile)
				if err != nil {
					return fmt.Errorf("failed to load configuration: %w", err)
				}
			case cfgPath != "":
				return err
			default:
				cfg = &config.Config{
					Package: config.PackageConfig{
						Name: "app",
					},
					Version: &config.VersionInfo{
						Raw:     "1.0.0",
						Windows: "1.0.0.0",
					},
				}
			}

			if iconPath != "" {
				cfg.Windows.Syso.Icon = iconPath
			}
			if manifestPath != "" {
				cfg.Windows.Syso.Manifest = manifestPath
			}
			if infoJSONPath != "" {
				cfg.Windows.Syso.InfoJSON = infoJSONPath
			}
			if outPath != "" {
				cfg.Windows.Syso.Out = outPath
			}

			arch := archFlag
			if arch == "" {
				arch = targetArch
			}
			if arch == "" {
				arch = "amd64"
			}

			out, err := windows.CompileSyso(cfg, arch)
			if err != nil {
				return err
			}
			fmt.Printf("Generated Windows syso COFF object: %s\n", out)
			return nil
		},
	}

	cmd.Flags().StringVar(&iconPath, "icon", "", "path to icon (.ico or .png)")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "path to application manifest (.xml or .manifest)")
	cmd.Flags().StringVar(&infoJSONPath, "info", "", "path to info.json version metadata")
	cmd.Flags().StringVar(&outPath, "out", "", "output path for .syso file")
	cmd.Flags().StringVar(&archFlag, "arch", "", "target architecture (amd64, arm64, 386)")
	return cmd
}

func newGenerateCmd() *cobra.Command {
	var (
		templatesDir string
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:     "generate [filter]",
		Aliases: []string{"gen"},
		Short:   "Generate project packaging and build files from templates",
		Long: `Renders files from templates (by default in build/templates/) using single-source-of-truth metadata from cook.toml and version files.
Allows gitignoring generated packaging files (NSIS, desktop files, info.json, AppRun, Info.plist) and version-controlling only templates.

Examples:
  cook generate
  cook generate nsis
  cook generate desktop
  cook generate --templates build/templates
  cook generate --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}

			if templatesDir != "" {
				cfg.Templates.Dir = templatesDir
			}

			target := ""
			if len(args) > 0 {
				target = args[0]
			}

			gen := generator.NewGenerator()
			files, err := gen.Generate(cmd.Context(), cfg, target, dryRun)
			if err != nil {
				return err
			}

			if len(files) == 0 {
				fmt.Println("No template files found to generate.")
				return nil
			}

			fmt.Printf("Generated %d file(s) from templates:\n", len(files))
			for _, f := range files {
				relDest, err := filepath.Rel(cfg.RootDir, f.DestPath)
				if err != nil {
					relDest = f.DestPath
				}
				relSrc, err := filepath.Rel(cfg.RootDir, f.TemplatePath)
				if err != nil {
					relSrc = f.TemplatePath
				}
				status := "created"
				if !f.Created {
					if f.Changed {
						status = "updated"
					} else {
						status = "up-to-date"
					}
				}
				if dryRun {
					status = "dry-run"
				}
				fmt.Printf("  * [%s] %s -> %s\n", status, relSrc, relDest)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&templatesDir, "templates", "t", "", "path to templates directory (defaults to build/templates)")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "d", false, "simulate generation without writing to disk")
	return cmd
}

func newConfigCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:     "config [generate]",
		Aliases: []string{"wails-config"},
		Short:   "Generate or manage project configuration files",
		Long: `Generates or manages project configuration files such as build/config.yml for Wails v3.

Examples:
  cook config
  cook config generate -o ./build/config.yml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}

			written, err := wails.WriteConfigFile(cfg, outPath)
			if err != nil {
				return fmt.Errorf("failed to generate Wails config: %w", err)
			}
			fmt.Printf("==> Generated Wails v3 config -> %s\n", written)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output path for generated config (defaults to build/config.yml)")
	return cmd
}

func newDevCmd() *cobra.Command {
	var (
		port         int
		host         string
		secure       bool
		skipBindings bool
		skipFrontend bool
		skipBackend  bool
		frontendOnly bool
		debounceMs   int
		tags         []string
		legacyWails  bool
	)
	cmd := &cobra.Command{
		Use:   "dev [flags]",
		Short: "Lightning-fast live development mode with Vite HMR and Go hot reload",
		Long: `Replaces wails3 dev and complex Taskfiles with a 24,000x faster, zero-dependency dev environment.
It concurrently starts the frontend dev server (Vite), detects readiness instantaneously (<150ms),
performs fast incremental Go builds, attaches dev tools, and hot-reloads backend and frontend changes
with sub-millisecond file watching and automatic process supervision.

Interactive shortcuts while running:
  [r] Rebuild & restart backend
  [b] Regenerate Wails TypeScript bindings
  [f] Restart frontend dev server
  [c] Clear terminal screen
  [q] Cleanly exit

Examples:
  cook dev
  cook dev -p 9245
  cook dev --skip-backend
  cook dev --skip-bindings
  cook dev -c build/simtx.cook.toml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}

			if legacyWails {
				written, err := wails.WriteConfigFile(cfg, "")
				if err != nil {
					return fmt.Errorf("failed to ensure Wails config: %w", err)
				}
				wails3Path, err := exec.LookPath("wails3")
				if err != nil {
					return fmt.Errorf("wails3 executable not found in PATH: %w", err)
				}
				devArgs := []string{"dev", "-config", written}
				if port > 0 {
					devArgs = append(devArgs, "-port", fmt.Sprintf("%d", port))
				}
				if secure {
					devArgs = append(devArgs, "-s")
				}
				devArgs = append(devArgs, args...)
				proc := exec.CommandContext(cmd.Context(), wails3Path, devArgs...)
				proc.Dir = cfg.RootDir
				proc.Stdout = os.Stdout
				proc.Stderr = os.Stderr
				proc.Stdin = os.Stdin
				return proc.Run()
			}

			debounce := 150 * time.Millisecond
			if debounceMs > 0 {
				debounce = time.Duration(debounceMs) * time.Millisecond
			}

			if frontendOnly {
				skipBackend = true
			}

			devOpts := dev.DevOptions{
				Port:         port,
				Host:         host,
				Secure:       secure,
				Debounce:     debounce,
				SkipBindings: skipBindings,
				SkipFrontend: skipFrontend,
				SkipBackend:  skipBackend,
				ExtraTags:    tags,
			}

			return dev.Run(cmd.Context(), cfg, devOpts)
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 9245, "frontend dev server port")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host interface for frontend server")
	cmd.Flags().BoolVarP(&secure, "secure", "s", false, "enable HTTPS")
	cmd.Flags().BoolVar(&skipBindings, "skip-bindings", false, "skip Wails bindings generation")
	cmd.Flags().BoolVar(&skipFrontend, "skip-frontend", false, "skip frontend dev server (backend only)")
	cmd.Flags().BoolVar(&skipBackend, "skip-backend", false, "skip backend application (frontend dev server only)")
	cmd.Flags().BoolVar(&frontendOnly, "frontend-only", false, "deprecated alias for --skip-backend")
	_ = cmd.Flags().MarkDeprecated("frontend-only", "use --skip-backend instead")
	cmd.Flags().IntVar(&debounceMs, "debounce", 150, "watcher debounce interval in milliseconds")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "additional Go build tags")
	cmd.Flags().BoolVar(&legacyWails, "legacy-wails", false, "fallback to legacy wails3 dev command")
	return cmd
}

func newRunCmd() *cobra.Command {
	var skipBuild bool
	cmd := &cobra.Command{
		Use:   "run [args...]",
		Short: "Build and run the application binary",
		Long: `Compiles the application (if not already compiled) and executes it directly.

Examples:
  cook run
  cook run --skip-build
  cook run -- --my-app-flag`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg()
			if err != nil {
				return err
			}

			outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)

			var binName string
			for _, b := range cfg.Binaries {
				if b.GUI {
					binName = b.Name
					break
				}
			}
			if binName == "" {
				switch len(cfg.Binaries) {
				case 1:
					binName = cfg.Binaries[0].Name
				case 0:
					configFile, _ := findConfigFile(cfgPath)
					return fmt.Errorf("no binaries configured in %s", configFile)
				default:
					names := make([]string, 0, len(cfg.Binaries))
					for _, b := range cfg.Binaries {
						names = append(names, b.Name)
					}
					return fmt.Errorf("multiple binaries configured and none marked as GUI: %s", strings.Join(names, ", "))
				}
			}

			runOS := targetOS
			if runOS == "" {
				runOS = runtime.GOOS
			}

			binPath := filepath.Join(outDir, binName)
			if runOS == "windows" && !strings.HasSuffix(binPath, ".exe") {
				binPath += ".exe"
			}

			if !skipBuild {
				orch := newOrchestrator()
				if _, err := orch.Build(cmd.Context(), cfg, nil, targetOS, targetArch); err != nil {
					return fmt.Errorf("build failed: %w", err)
				}
			}

			if _, err := os.Stat(binPath); err != nil {
				return fmt.Errorf("executable not found at %s: %w", binPath, err)
			}

			proc := exec.CommandContext(cmd.Context(), binPath, args...)
			proc.Dir = cfg.RootDir
			proc.Stdout = os.Stdout
			proc.Stderr = os.Stderr
			proc.Stdin = os.Stdin
			return proc.Run()
		},
	}
	cmd.Flags().BoolVar(&skipBuild, "skip-build", false, "skip compilation phase and run existing binary")
	return cmd
}
