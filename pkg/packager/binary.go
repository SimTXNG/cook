package packager

import (
	"os"
	"path/filepath"
)

// LookupBinary finds an existing regular file for name. It checks the binaries
// map by name (and name+".exe" when exe), then outDir/name (and .exe variant).
// Returns "" when name is empty or nothing exists.
func LookupBinary(binaries map[string]string, outDir, name string, exe bool) string {
	if name == "" {
		return ""
	}
	if path := binaries[name]; path != "" && isRegularFile(path) {
		return path
	}
	if exe {
		if path := binaries[name+".exe"]; path != "" && isRegularFile(path) {
			return path
		}
	}
	if outDir != "" {
		if path := filepath.Join(outDir, name); isRegularFile(path) {
			return path
		}
		if exe {
			if path := filepath.Join(outDir, name+".exe"); isRegularFile(path) {
				return path
			}
		}
	}
	return ""
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
