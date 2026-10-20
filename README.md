# Cook

**Cook** is a standalone, multi-platform build and packaging engine written in Go. It completely replaces `wails3 package` and brittle Taskfile/shell script pipelines for building and distributing Go/Wails desktop applications.

---

## Highlights & Features

- **Pure-Go Packaging Engine**:
  - **Linux**: Embedded NFPM generator creates `.deb`, `.rpm`, and Arch Linux (`.pkg.tar.zst`) packages directly in Go with automatic post-install scripts and EVR versioning. A native, pure-Go generator also produces Void Linux `.xbps` packages (no `xbps-create` required), with `props.plist`/`files.plist` metadata, SHA-256 file hashes, and optional maintainer scripts.
  - **Windows**: Pure-Go PE resource compiler via `winres` generates `.syso` COFF objects containing high-DPI manifests (`permonitorv2`), version info, and icons without requiring external MinGW/windres tools. Pure-Go MSIX packaging via `go-msix`.
  - **macOS**: Full `.app` bundle assembly bundling both GUI and companion CLI executables into `Contents/MacOS/`, with styled `.dmg` disk image creation.
- **Single-Source-of-Truth Versioning**:
  - Automatically extracts semver/prerelease strings from Go source code (e.g. `AppVersion = "0.1.0-alpha-12"`) and extracts Git commit hashes.
  - Automatically translates versions to meet strict platform package manager requirements:
    - **Debian / RPM**: Translates hyphens to tildes (`0.1.0~alpha12-1`) so pre-releases sort strictly before final releases.
    - **Arch Linux**: Strips illegal hyphens (`0.1.0alpha12-1`).
    - **Windows / NSIS / MSIX**: Strips alphanumeric suffixes to format a strict 4-part numeric version (`0.1.0.0`).
- **Native Multi-Binary Orchestration**:
  - Compiles multiple binaries defined in configuration (e.g. desktop GUI binary, companion headless CLI, server daemon).
  - Supports custom CGO flags, build tags, linker flags, Git commit injection, and UPX compression.
- **Integrated Lifecycle Hooks & Fix-Script Runner**:
  - Directly runs fix scripts (such as `scripts/fix-appimage.sh`) as pre-run, post-run, or standalone commands.
- **AppImage Packaging & Linuxdeploy Integration**:
  - Stages AppDir structure and deploys dependencies via `linuxdeploy` and `appimagetool`.
  - Integrates with project fix scripts (e.g. `scripts/fix-appimage.sh`) for WebKitGTK relocation, permission restoration, and rpath patching.
  - Generates `.zsync` delta-update descriptors and `.sha256` checksums.
- **Hardened macOS Entitlements & Audio Fix**:
  - Generates hardened runtime `entitlements.plist` including `com.apple.security.device.audio-input`.
  - Injects `NSMicrophoneUsageDescription` into `Info.plist` to prevent macOS microphone capture crashes.
  - Supports ad-hoc and Developer ID signing and Apple notarization.

---

## Installation & Build

Build the `cook` binary:

```bash
cd cook
go build -o bin/cook ./cmd/cook
```

Optionally install into your `PATH`:
```bash
go install ./cmd/cook
```

---

## CLI Usage

### 1. Validate Environment & Configuration
Inspects your TOML configuration and checks for available toolchains:

```bash
cook validate
# Or specify a custom configuration file:
cook validate -c cook.toml
```

### 2. Compile Binaries & Targets (`build`)
Builds frontend assets and compiles defined Go binaries, or directly packages distribution targets:

```bash
# Build all configured binaries
cook build

# Build specific binaries
cook build myapp myapp-cli

# Build and package a distribution target directly (e.g. AppImage)
cook build linux.appimage

# Build without frontend compilation
cook build --skip-frontend

# Compile multiple binaries in parallel and skip unchanged phases
cook build --jobs 4 --skip-unchanged

# Force a full rebuild despite --skip-unchanged
cook build --skip-unchanged --force
```

`--skip-unchanged` stores input fingerprints in `.cook-cache.json` at the project
root. It skips bindings, frontend, and Go compile/link phases when nothing that
affects them changed. Add `.cook-cache.json` to your project's `.gitignore`.

Per-binary `upx_level` controls UPX compression effort (1 fastest, 9 smallest;
default 6). UPX already-packed outputs are detected and skipped.

With `--jobs 0` (the default), binaries compile concurrently once the Go build
cache is warm; on a cold cache Cook builds them sequentially so the first build
warms shared dependencies instead of compiling them twice.

### 3. Package for Distribution (`package`)
Builds and packages targets for the specified operating system and architecture:

```bash
# Package all targets for host OS
cook package

# Package specific Linux formats (deb, rpm, archlinux, void, appimage)
cook package deb rpm archlinux void
cook package linux.appimage

# Package using pre-built binaries (skips Go compilation phase)
cook package --skip-build deb rpm

# Cross-package Windows formats
cook package nsis msix --os windows --arch amd64

# Package macOS .app bundle and DMG
cook package app dmg --os darwin --arch amd64
```

Void `.xbps` packages are emitted unsigned. Before distributing them, index the
output directory with `xbps-rindex -a <pkg>.xbps` (and sign with
`xbps-rindex -S --privkey <key> <pkg>.xbps`, which writes the detached `.sig2`)
or publish them through an existing signed repository.

Dependencies in `[linux.void]` are written as `run_depends`/`conflicts`/
`replaces` metadata. libxbps only accepts a fully qualified `name-version_revision`
or a versioned pattern such as `gtk4>=0`; a bare name fails with "can't guess
pkgname". Cook therefore normalizes bare names by appending `>=0`, so
`depends = ["gtk4", "libwebkitgtk60"]` is valid. Use Void package names (the
WebKitGTK runtime is `libwebkitgtk60` for GTK4/6.0, or `libwebkit2gtk41` for
GTK3/4.1), not library file names.

### 4. Windows Resource Compilation (`syso`)
Generates a `.syso` COFF resource object in pure Go:

```bash
cook syso

# Or specify custom assets:
cook syso --icon build/windows/icon.ico \
          --manifest build/windows/wails.exe.manifest \
          --info build/windows/info.json \
          --out wails_windows_amd64.syso
```

### 5. Native Live Development Mode (`dev`)
Replaces `wails3 dev` and Taskfiles with a 24,000x faster, zero-dependency development environment:
- **Instant Concurrent Startup**: Boots Vite dev server in parallel with fast incremental Go backend compilation.
- **Microsecond Readiness Probing**: Probes the frontend socket every 25ms, launching the application window in <600ms (vs 5-8s in wails3).
- **Sub-Millisecond File Watcher**: High-performance recursive Go file watcher with 150ms debouncing (ignoring `.git`, `node_modules`, `dist`, `bin`, etc.).
- **Vite HMR**: UI updates instantly in the webview via Vite HMR without recompiling Go.
- **Zero-Downtime Swap**: When backend Go code changes, recompiles to a staging binary; if compilation fails, displays compiler diagnostics and keeps your running app alive! If compilation succeeds, restarts backend in <200ms.
- **Process Group Isolation**: Guarantees zero zombie processes or orphaned ports on exit.
- **Interactive Keyboard Controls**:
  - `[r]` Force rebuild & restart backend
  - `[b]` Regenerate Wails TypeScript bindings
  - `[f]` Restart frontend dev server
  - `[c]` Clear terminal screen
  - `[q]` Cleanly exit

```bash
# Start live development mode
cook dev

# Specify custom Vite port:
cook dev -p 9245

# Backend only (skipping frontend dev server):
cook dev --skip-frontend

# Reuse unchanged build results and use debugger-friendly compiler flags:
cook dev --skip-unchanged --gcflags "all=-N -l"
```

### 6. Run Application (`run`)
Compiles (if needed) and executes the application binary:

```bash
cook run
cook run --skip-build
cook run -- --my-app-flag
```

### 7. Generate Wails v3 `config.yml` (`config`)
Generates standard `build/config.yml` for external tools:

```bash
cook config
```

---

## Configuration Reference (`cook.toml`)

```toml
[package]
name = "myapp"
product_name = "My Application"
company = "Acme Corp"
identifier = "com.acme.myapp"
description = "High performance cross-platform application"
version_file = "pkg/version/version.go"
version_regex = 'AppVersion\s*=\s*"([^"]+)"'
output_dir = "bin"
icon = "build/appicon.png"
root_dir = "."

[frontend]
enabled = true
dir = "frontend"
package_manager = "npm"
build_cmd = "npm run build"
dist_dir = "frontend/dist"

[[binaries]]
name = "myapp"
path = "."
gui = true
cgo = true
tags = ["production"]
ldflags = "-w -s"
commit_var = "main.AppCommit"

[[binaries]]
name = "myapp-cli"
path = "./cmd/myapp-cli"
gui = false
cgo = true
upx = true

# Lifecycle hooks & fix scripts
[hooks]
fix_scripts = { appimage = "scripts/fix-appimage.sh" }

# Linux targets: deb, rpm, archlinux, void, appimage
[linux]
targets = ["deb", "rpm", "archlinux", "void", "appimage"]
categories = "Development;"
bundled_cli = "myapp-cli"

# Void Linux (.xbps) packaging: package name, revision and dependencies.
[linux.void]
depends = ["gtk4", "libwebkitgtk60", "hicolor-icon-theme"]
revision = 1

[linux.appimage]
fix_script = "scripts/fix-appimage.sh"
update_info = "zsync|https://updates.example.com/appimage-zsync"
generate_sha256 = true

# Windows targets: syso, nsis, msix
[windows]
targets = ["syso", "nsis"]
bundled_cli = "myapp-cli.exe"

[windows.nsis]
script = "build/windows/nsis/project.nsi"
install_scope = "machine"
auto_download_webview2 = true

# macOS targets: app, dmg
[darwin]
targets = ["app", "dmg"]
bundle_id = "com.acme.myapp"
microphone_usage_description = "MyApp requires microphone access."
codesign = "adhoc"
```
