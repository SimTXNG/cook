package dev

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestDetectPackageManager(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"bun.lock", "bun"},
		{"bun.lockb", "bun"},
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte("x"), 0644); err != nil {
				t.Fatalf("setup: %v", err)
			}
			if got := detectPackageManager(dir); got != tc.want {
				t.Errorf("detectPackageManager(%s) = %q, want %q", tc.file, got, tc.want)
			}
		})
	}

	if got := detectPackageManager(t.TempDir()); got != "npm" {
		t.Errorf("detectPackageManager(empty) = %q, want npm", got)
	}
}

func TestIsServiceOrEventFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"internal/app/app_service.go", true},
		{"internal/events/events.go", true},
		{"pkg/model/user.go", true},
		{"internal/backend/main.go", false},
		{"backend.go", false},
		{"internal/service/service_test.go", false},
		{"internal/app/app_service.ts", false},
		{"internal/generated/bindings/service.ts", false},
	}

	for _, tc := range cases {
		if got := isServiceOrEventFile(tc.path); got != tc.want {
			t.Errorf("isServiceOrEventFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestNewBackendSupervisorPrefersNamedBinary(t *testing.T) {
	cfg := &config.Config{
		Package: config.PackageConfig{Name: "myapp-cli"},
		Binaries: []config.BinaryConfig{
			{Name: "myapp-cli", Path: "./cmd/myapp-cli"},
			{Name: "myapp", Path: ".", GUI: true},
		},
	}

	s := NewBackendSupervisor(cfg)
	if s.binaryName != "myapp-cli" {
		t.Fatalf("binaryName = %q, want myapp-cli", s.binaryName)
	}
}

func TestCompileUnknownBinaryFails(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Package:  config.PackageConfig{Name: "ghost", OutputDir: "bin"},
		Binaries: []config.BinaryConfig{{Name: "real", Path: "."}},
		RootDir:  dir,
	}

	s := NewBackendSupervisor(cfg)
	if _, err := s.Compile(context.Background(), nil); err == nil {
		t.Fatal("expected error for unconfigured binary, got nil")
	}
}
