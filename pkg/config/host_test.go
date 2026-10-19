package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeOS(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", runtime.GOOS},
		{"macos", "darwin"},
		{"darwin", "darwin"},
		{"win", "windows"},
		{"windows", "windows"},
		{"linux", "linux"},
		{"Solaris", "solaris"},
		{" Plan9 ", "plan9"},
	}

	for _, tt := range tests {
		if got := NormalizeOS(tt.in); got != tt.want {
			t.Errorf("NormalizeOS(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeArch(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", runtime.GOARCH},
		{"x86_64", "amd64"},
		{"amd64", "amd64"},
		{"aarch64", "arm64"},
		{"arm64", "arm64"},
		{"i686", "386"},
		{"386", "386"},
		{"RISC-V64", "risc-v64"},
		{" arm64 ", "arm64"},
	}

	for _, tt := range tests {
		if got := NormalizeArch(tt.in); got != tt.want {
			t.Errorf("NormalizeArch(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolvePath(t *testing.T) {
	root := filepath.FromSlash("/srv/project")
	tests := []struct {
		name string
		root string
		path string
		want string
	}{
		{"empty path", root, "", ""},
		{"absolute path", root, filepath.FromSlash("/tmp/./foo/../bar"), filepath.FromSlash("/tmp/bar")},
		{"relative with root", root, filepath.FromSlash("build/./linux/../app"), filepath.FromSlash("/srv/project/build/app")},
		{"relative with empty root", "", filepath.FromSlash("build/./linux/../app"), filepath.FromSlash("build/app")},
		{"dot", "", ".", "."},
		{"parent traversal", root + "/sub", "..", root},
		{"clean under empty root", "", filepath.FromSlash("a//b/./c/.."), filepath.FromSlash("a/b")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvePath(tt.root, tt.path); got != tt.want {
				t.Errorf("ResolvePath(%q, %q) = %q, want %q", tt.root, tt.path, got, tt.want)
			}
		})
	}
}
