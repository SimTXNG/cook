package darwin

import (
	"context"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildAdHocCodesignArgs(t *testing.T) {
	args := BuildAdHocCodesignArgs("/path/to/App.app")
	joined := strings.Join(args, " ")
	if joined != "--force --deep --sign - /path/to/App.app" {
		t.Errorf("unexpected ad-hoc args: %s", joined)
	}
}

func TestBuildDeveloperIDCodesignArgs(t *testing.T) {
	args := BuildDeveloperIDCodesignArgs("Developer ID Application: SimTX", "/path/to/entitlements.plist", "/path/to/App.app")
	joined := strings.Join(args, " ")
	expected := "--force --deep --options runtime --entitlements /path/to/entitlements.plist --sign Developer ID Application: SimTX /path/to/App.app"
	if joined != expected {
		t.Errorf("expected: %s\ngot: %s", expected, joined)
	}
}

func TestBuildVerifyCodesignArgs(t *testing.T) {
	args := BuildVerifyCodesignArgs("/path/to/App.app")
	joined := strings.Join(args, " ")
	expected := "--verify --deep --strict --verbose=2 /path/to/App.app"
	if joined != expected {
		t.Errorf("expected: %s\ngot: %s", expected, joined)
	}
}

func TestSignAppBundleSkip(t *testing.T) {
	cfg := &config.Config{
		Darwin: config.DarwinConfig{
			Codesign: "skip",
		},
	}
	err := SignAppBundle(context.Background(), cfg, "/dummy/App.app", "")
	if err != nil {
		t.Fatalf("unexpected error when codesign is set to skip: %v", err)
	}
}

func TestSignAppBundleDeveloperIDRequiresIdentity(t *testing.T) {
	cfg := &config.Config{
		Darwin: config.DarwinConfig{
			Codesign:         "developer_id",
			CodesignIdentity: "",
			BundleID:         "net.simtx.app",
		},
	}
	err := SignAppBundle(context.Background(), cfg, "/dummy/App.app", "")
	if err == nil {
		t.Fatalf("expected error when developer_id identity is empty")
	}
	if !strings.Contains(err.Error(), "codesign_identity") {
		t.Errorf("expected error to mention codesign_identity, got: %v", err)
	}
}
