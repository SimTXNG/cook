package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/packager"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		raw     string
		wantOS  string
		wantPkg string
	}{
		{"deb", "linux", "deb"},
		{"LINUX.AppImage", "linux", "appimage"},
		{"linux:appimage", "linux", "appimage"},
		{"linux/appimage", "linux", "appimage"},
		{"linux.foo", "linux", "foo"},
		{"linux.aur", "linux", "archlinux"},
		{"aur", "linux", "archlinux"},
		{"archlinux", "linux", "archlinux"},
		{"windows.nsis", "windows", "nsis"},
		{"nsis", "windows", "nsis"},
		{"msix", "windows", "msix"},
		{"syso", "windows", "syso"},
		{"darwin.dmg", "darwin", "dmg"},
		{"app", "darwin", "app"},
		{"  deb  ", "linux", "deb"},
		{"unknown", "", "unknown"},
		{"unknown.pkg", "", "unknown.pkg"},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			gotOS, gotPkg := ParseTarget(tt.raw)
			if gotOS != tt.wantOS || gotPkg != tt.wantPkg {
				t.Fatalf("ParseTarget(%q) = (%q, %q), want (%q, %q)", tt.raw, gotOS, gotPkg, tt.wantOS, tt.wantPkg)
			}
		})
	}
}

func TestIsKnownPackagingTarget(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"deb", true},
		{"linux.appimage", true},
		{"archlinux", true},
		{"aur", true},
		{"linux.aur", true},
		{"nsis", true},
		{"windows.msix", true},
		{"syso", true},
		{"app", true},
		{"darwin.dmg", true},
		{"linux.foo", false},
		{"linux", false},
		{"unknown", false},
		{"unknown.pkg", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := IsKnownPackagingTarget(tt.raw); got != tt.want {
				t.Fatalf("IsKnownPackagingTarget(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestRegisteredPackagersSorted(t *testing.T) {
	o := &Orchestrator{Packagers: map[string]packager.Packager{
		"windows": nil,
		"linux":   nil,
		"darwin":  nil,
	}}
	want := []string{"darwin", "linux", "windows"}
	if got := o.registeredPackagers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registeredPackagers() = %v, want %v", got, want)
	}
}

func TestDiscoverBinaries(t *testing.T) {
	tests := []struct {
		name     string
		targetOS string
		binary   config.BinaryConfig
		files    []string
		dirs     []string
		want     string
	}{
		{
			name:     "windows gui prefers exe",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "app", GUI: true},
			files:    []string{"app", "app.exe"},
			want:     "app.exe",
		},
		{
			name:     "windows gui accepts only match",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "app", GUI: true},
			files:    []string{"app"},
			want:     "app",
		},
		{
			name:     "windows cli accepts extensionless",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "cli"},
			files:    []string{"cli"},
			want:     "cli",
		},
		{
			name:     "windows cli prefers exe",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "cli"},
			files:    []string{"cli", "cli.exe"},
			want:     "cli.exe",
		},
		{
			name:     "windows explicit exe has no fallback",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "tool.exe", GUI: true},
			files:    []string{"tool"},
			want:     "",
		},
		{
			name:     "windows ambiguous exe path",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "app", GUI: true},
			files:    []string{"app"},
			dirs:     []string{"app.exe"},
			want:     "",
		},
		{
			name:     "windows missing",
			targetOS: "windows",
			binary:   config.BinaryConfig{Name: "app", GUI: true},
			want:     "",
		},
		{
			name:     "linux extensionless",
			targetOS: "linux",
			binary:   config.BinaryConfig{Name: "app", GUI: true},
			files:    []string{"app"},
			want:     "app",
		},
		{
			name:     "linux does not borrow exe",
			targetOS: "linux",
			binary:   config.BinaryConfig{Name: "app", GUI: true},
			files:    []string{"app.exe"},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, file), []byte("binary"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, sub := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			cfg := &config.Config{
				RootDir:  dir,
				Binaries: []config.BinaryConfig{tt.binary},
			}
			got, err := discoverBinaries(cfg, tt.targetOS)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("discoverBinaries() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("discoverBinaries() error: %v", err)
			}
			if gotPath := got[tt.binary.Name]; gotPath != filepath.Join(dir, tt.want) {
				t.Fatalf("discoverBinaries()[%q] = %q, want %q", tt.binary.Name, gotPath, filepath.Join(dir, tt.want))
			}
		})
	}
}
