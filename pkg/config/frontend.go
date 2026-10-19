package config

// DefaultInstallCmd returns the dependency install command for the given
// package manager, falling back to npm for an unknown or empty value.
func DefaultInstallCmd(pm string) string {
	switch pm {
	case "bun":
		return "bun install"
	case "pnpm":
		return "pnpm install"
	case "yarn":
		return "yarn install"
	default:
		return "npm install"
	}
}

// DefaultBuildCmd returns the production build command for the given package
// manager, falling back to npm for an unknown or empty value.
func DefaultBuildCmd(pm string) string {
	switch pm {
	case "bun":
		return "bun run build"
	case "pnpm":
		return "pnpm run build"
	case "yarn":
		return "yarn build"
	default:
		return "npm run build"
	}
}
