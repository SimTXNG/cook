package builder

import (
	"context"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildUnknownBinaryFilterReturnsError(t *testing.T) {
	cfg := &config.Config{
		Binaries: []config.BinaryConfig{
			{Name: "app"},
			{Name: "cli"},
		},
	}
	b := NewBuilder(cfg)

	built, err := b.Build(context.Background(), []string{"app", "missing"}, "", "")
	if err == nil {
		t.Fatal("expected unknown binary filter error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error does not name the unknown filter: %v", err)
	}
	if built != nil {
		t.Errorf("built = %v, want nil", built)
	}
}
