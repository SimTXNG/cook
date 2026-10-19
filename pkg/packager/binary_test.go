package packager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupBinary(t *testing.T) {
	tmpDir := t.TempDir()
	plain := filepath.Join(tmpDir, "simtx")
	exe := filepath.Join(tmpDir, "simtx.exe")
	outDir := filepath.Join(tmpDir, "out")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	outCandidate := filepath.Join(outDir, "simtx")
	for _, p := range []string{plain, exe, outCandidate} {
		if err := os.WriteFile(p, []byte("bin"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name     string
		binaries map[string]string
		name2    string
		exe      bool
		want     string
	}{
		{"empty name", map[string]string{"simtx": plain}, "", false, ""},
		{"map hit", map[string]string{"simtx": plain}, "simtx", false, plain},
		{"map exe hit", map[string]string{"simtx.exe": exe}, "simtx", true, exe},
		{"exe ignored when disabled", map[string]string{"simtx.exe": exe}, "simtx", false, outCandidate},
		{"outDir candidate", map[string]string{}, "simtx", false, outCandidate},
		{"missing", map[string]string{"gone": filepath.Join(tmpDir, "gone")}, "gone", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LookupBinary(tc.binaries, outDir, tc.name2, tc.exe)
			if got != tc.want {
				t.Errorf("LookupBinary(%v, %q, %q, %v) = %q, want %q", tc.binaries, outDir, tc.name2, tc.exe, got, tc.want)
			}
		})
	}
}
