package builder

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setupGoModule(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/systest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "cmd", "foo", "main.go"), "package main\n\nfunc main() {}\n")
	return root
}

func TestCleanSysoFilesRootAndPackageDir(t *testing.T) {
	root := t.TempDir()

	rootSyso := filepath.Join(root, "root.syso")
	pkgSyso := filepath.Join(root, "cmd", "foo", "pkg.syso")
	parentSyso := filepath.Join(root, "cmd", "parent.syso")
	otherSyso := filepath.Join(root, "other", "other.syso")
	for _, path := range []string{rootSyso, pkgSyso, parentSyso, otherSyso} {
		writeFile(t, path, "not a real object")
	}

	if err := cleanSysoFiles(root, "./cmd/foo"); err != nil {
		t.Fatalf("cleanSysoFiles: %v", err)
	}

	for _, path := range []string{rootSyso, pkgSyso} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s was not removed", path)
		}
	}
	for _, path := range []string{parentSyso, otherSyso} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s should not have been removed: %v", path, err)
		}
	}
}

func TestCleanSysoFilesFromGoFile(t *testing.T) {
	root := t.TempDir()
	mainFile := filepath.Join(root, "cmd", "foo", "main.go")
	syso := filepath.Join(root, "cmd", "foo", "pkg.syso")
	writeFile(t, mainFile, "package main\n")
	writeFile(t, syso, "not a real object")

	if err := cleanSysoFiles(root, "./cmd/foo/main.go"); err != nil {
		t.Fatalf("cleanSysoFiles: %v", err)
	}
	if _, err := os.Stat(syso); !os.IsNotExist(err) {
		t.Errorf("%s was not removed", syso)
	}
}

func TestCleanSysoFilesMissingPathIsIgnored(t *testing.T) {
	root := t.TempDir()
	if err := cleanSysoFiles(root, "./does/not/exist"); err != nil {
		t.Fatalf("cleanSysoFiles on missing path: %v", err)
	}
}

func TestGoBuildArgs(t *testing.T) {
	cfg := &config.Config{
		RootDir: t.TempDir(),
		Package: config.PackageConfig{OutputDir: "bin"},
		Version: &config.VersionInfo{Commit: "abc123"},
	}

	t.Run("builder options", func(t *testing.T) {
		b := &config.BinaryConfig{
			Name:      "myapp",
			Path:      "./cmd/myapp",
			Tags:      []string{"netgo", "production"},
			Ldflags:   "-s -w",
			CommitVar: "main.version",
			GUI:       true,
		}
		args := GoBuildArgs(cfg, b, "windows", GoBuildOptions{Trimpath: true, Ldflags: true})
		want := []string{
			"build", "-trimpath", "-buildvcs=false",
			"-tags", "netgo,production",
			"-ldflags=-s -w -H windowsgui -X main.version=abc123",
			"-o", filepath.Join(cfg.RootDir, "bin", "myapp.exe"),
			"./cmd/myapp",
		}
		if strings.Join(args, "|") != strings.Join(want, "|") {
			t.Fatalf("args mismatch:\n got %v\nwant %v", args, want)
		}
	})

	t.Run("dev options strip tags and skip release ldflags", func(t *testing.T) {
		b := &config.BinaryConfig{
			Name:      "myapp",
			Tags:      []string{"netgo", "production"},
			Ldflags:   "-s -w",
			CommitVar: "main.version",
			GUI:       true,
		}
		args := GoBuildArgs(cfg, b, "linux", GoBuildOptions{
			ExcludeTags: []string{"production"},
			ExtraTags:   []string{"devmode"},
			GCFlags:     "all=-l",
		})
		want := []string{
			"build", "-buildvcs=false", "-gcflags=all=-l",
			"-tags", "netgo,devmode",
			"-ldflags=-X main.version=abc123",
			"-o", filepath.Join(cfg.RootDir, "bin", "myapp"),
			".",
		}
		if strings.Join(args, "|") != strings.Join(want, "|") {
			t.Fatalf("args mismatch:\n got %v\nwant %v", args, want)
		}
	})
}

func TestGoOutputPathWindowsSuffix(t *testing.T) {
	cfg := &config.Config{RootDir: "/root", Package: config.PackageConfig{OutputDir: "bin"}}
	if got := GoOutputPath(cfg, &config.BinaryConfig{Name: "app.exe"}, "windows"); got != filepath.Join("/root", "bin", "app.exe") {
		t.Errorf("windows path = %q", got)
	}
	if got := GoOutputPath(cfg, &config.BinaryConfig{Name: "app"}, "linux"); got != filepath.Join("/root", "bin", "app") {
		t.Errorf("linux path = %q", got)
	}
}

func TestBuildBinaryCleansSysoBeforeCompilation(t *testing.T) {
	root := setupGoModule(t)
	writeFile(t, filepath.Join(root, "stale.syso"), "not a real object")
	writeFile(t, filepath.Join(root, "cmd", "foo", "stale.syso"), "not a real object")
	parentSyso := filepath.Join(root, "cmd", "parent.syso")
	writeFile(t, parentSyso, "not a real object")

	cfg := &config.Config{
		RootDir: root,
		Package: config.PackageConfig{OutputDir: "bin"},
	}
	bCfg := &config.BinaryConfig{
		Name:      "foo",
		Path:      "./cmd/foo",
		CleanSyso: true,
	}

	out, err := BuildBinary(context.Background(), cfg, bCfg, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("BuildBinary: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("built binary missing: %v", err)
	}
	if _, err := os.Stat(parentSyso); err != nil {
		t.Errorf("parent package syso should not have been removed: %v", err)
	}
}

func TestBuildBinaryUPXFailureReturnsError(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("UPX is skipped on darwin and the fake script is POSIX-only")
	}

	root := setupGoModule(t)

	binDir := t.TempDir()
	upx := filepath.Join(binDir, "upx")
	writeFile(t, upx, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(upx, 0o755); err != nil {
		t.Fatalf("chmod upx: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.Config{
		RootDir: root,
		Package: config.PackageConfig{OutputDir: "bin"},
	}
	bCfg := &config.BinaryConfig{
		Name: "foo",
		Path: "./cmd/foo",
		UPX:  true,
	}

	_, err := BuildBinary(context.Background(), cfg, bCfg, runtime.GOOS, runtime.GOARCH)
	if err == nil {
		t.Fatal("expected UPX failure to be reported")
	}
	if !strings.Contains(err.Error(), "UPX") {
		t.Errorf("error does not mention UPX: %v", err)
	}
}
