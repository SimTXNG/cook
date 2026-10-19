package darwin

import (
	"fmt"
	"os"
	"path/filepath"

	"howett.net/plist"
)

const (
	DefaultMicrophoneUsageDescription = "Microphone access is required for audio streaming and transmission."
)

// Entitlements represents the security entitlements plist structure for macOS.
type Entitlements struct {
	AllowJIT                      bool `plist:"com.apple.security.cs.allow-jit"`
	AllowUnsignedExecutableMemory bool `plist:"com.apple.security.cs.allow-unsigned-executable-memory"`
	DisableLibraryValidation      bool `plist:"com.apple.security.cs.disable-library-validation"`
	AudioInput                    bool `plist:"com.apple.security.device.audio-input"`
	NetworkClient                 bool `plist:"com.apple.security.network.client"`
	NetworkServer                 bool `plist:"com.apple.security.network.server"`
}

// GenerateEntitlements produces an entitlements.plist file containing hardened runtime entitlements
// and com.apple.security.device.audio-input for microphone capture.
func GenerateEntitlements(targetPath string) error {
	ent := Entitlements{
		AllowJIT:                      true,
		AllowUnsignedExecutableMemory: true,
		DisableLibraryValidation:      true,
		AudioInput:                    true,
		NetworkClient:                 true,
		NetworkServer:                 true,
	}

	data, err := plist.MarshalIndent(ent, plist.XMLFormat, "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal entitlements: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for entitlements: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write entitlements to %s: %w", targetPath, err)
	}

	return nil
}
