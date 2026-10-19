package config

import "testing"

func TestDefaultInstallCmd(t *testing.T) {
	tests := map[string]string{
		"bun":   "bun install",
		"pnpm":  "pnpm install",
		"yarn":  "yarn install",
		"npm":   "npm install",
		"":      "npm install",
		"weird": "npm install",
	}
	for pm, want := range tests {
		if got := DefaultInstallCmd(pm); got != want {
			t.Errorf("DefaultInstallCmd(%q) = %q, want %q", pm, got, want)
		}
	}
}

func TestDefaultBuildCmd(t *testing.T) {
	tests := map[string]string{
		"bun":   "bun run build",
		"pnpm":  "pnpm run build",
		"yarn":  "yarn build",
		"npm":   "npm run build",
		"":      "npm run build",
		"weird": "npm run build",
	}
	for pm, want := range tests {
		if got := DefaultBuildCmd(pm); got != want {
			t.Errorf("DefaultBuildCmd(%q) = %q, want %q", pm, got, want)
		}
	}
}
