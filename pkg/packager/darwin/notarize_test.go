package darwin

import (
	"context"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildNotarySubmitArgs(t *testing.T) {
	args := BuildNotarySubmitArgs("/path/to/app.dmg", "AC_PASSWORD")
	joined := strings.Join(args, " ")
	expected := "notarytool submit /path/to/app.dmg --keychain-profile AC_PASSWORD --wait"
	if joined != expected {
		t.Errorf("expected: %s, got: %s", expected, joined)
	}
}

func TestBuildStaplerArgs(t *testing.T) {
	args := BuildStaplerArgs("/path/to/app.dmg")
	joined := strings.Join(args, " ")
	expected := "stapler staple /path/to/app.dmg"
	if joined != expected {
		t.Errorf("expected: %s, got: %s", expected, joined)
	}
}

func TestNotarizeArtifactDisabled(t *testing.T) {
	cfg := &config.Config{
		Darwin: config.DarwinConfig{
			Notarize: false,
		},
	}
	err := NotarizeArtifact(context.Background(), cfg, "/dummy/app.dmg")
	if err != nil {
		t.Fatalf("unexpected error when notarize is disabled: %v", err)
	}
}

func TestNotarizeArtifactEnabledRequiresProfile(t *testing.T) {
	cfg := &config.Config{
		Darwin: config.DarwinConfig{
			Notarize: true,
			// NotarizeProfile intentionally empty
		},
		Package: config.PackageConfig{Identifier: "net.simtx.app"},
	}
	err := NotarizeArtifact(context.Background(), cfg, "/dummy/app.dmg")
	if err == nil {
		t.Fatalf("expected error when notarize is enabled without a profile")
	}
	if !strings.Contains(err.Error(), "notarize_profile") {
		t.Errorf("expected error to mention notarize_profile, got: %v", err)
	}
}
