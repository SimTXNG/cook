package packager

import (
	"context"

	"github.com/simtxng/cook/pkg/config"
)

// Packager defines the interface for platform packaging engines.
type Packager interface {
	Name() string
	SupportedTargets() []string
	Package(ctx context.Context, cfg *config.Config, target, arch string, binaries map[string]string) ([]string, error)
}
