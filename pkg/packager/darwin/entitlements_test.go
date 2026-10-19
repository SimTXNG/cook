package darwin

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"
)

func TestGenerateEntitlements(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-entitlements-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	entPath := filepath.Join(tmpDir, "entitlements.plist")
	if err := GenerateEntitlements(entPath); err != nil {
		t.Fatalf("GenerateEntitlements failed: %v", err)
	}

	data, err := os.ReadFile(entPath)
	if err != nil {
		t.Fatal(err)
	}

	var ent Entitlements
	decoder := plist.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&ent); err != nil {
		t.Fatalf("failed to parse generated entitlements: %v", err)
	}

	// Verify com.apple.security.device.audio-input is true
	if !ent.AudioInput {
		t.Errorf("expected AudioInput (com.apple.security.device.audio-input) to be true")
	}

	// Verify hardened runtime flags
	if !ent.AllowJIT {
		t.Errorf("expected AllowJIT to be true")
	}
	if !ent.AllowUnsignedExecutableMemory {
		t.Errorf("expected AllowUnsignedExecutableMemory to be true")
	}
	if !ent.DisableLibraryValidation {
		t.Errorf("expected DisableLibraryValidation to be true")
	}
	if !ent.NetworkClient {
		t.Errorf("expected NetworkClient to be true")
	}
}
