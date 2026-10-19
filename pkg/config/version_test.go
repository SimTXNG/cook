package config

import (
	"testing"
)

func TestVersionFormatting(t *testing.T) {
	tests := []struct {
		input       string
		wantDeb     string
		wantRPM     string
		wantArch    string
		wantWindows string
	}{
		{
			input:       "0.1.0-alpha-12",
			wantDeb:     "0.1.0~alpha12",
			wantRPM:     "0.1.0~alpha12",
			wantArch:    "0.1.0alpha12",
			wantWindows: "0.1.0.0",
		},
		{
			input:       "1.2.3",
			wantDeb:     "1.2.3",
			wantRPM:     "1.2.3",
			wantArch:    "1.2.3",
			wantWindows: "1.2.3.0",
		},
		{
			input:       "2.0.0-beta.1",
			wantDeb:     "2.0.0~beta.1",
			wantRPM:     "2.0.0~beta.1",
			wantArch:    "2.0.0beta.1",
			wantWindows: "2.0.0.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			gotDeb := FormatDebianVersion(tc.input)
			if gotDeb != tc.wantDeb {
				t.Errorf("FormatDebianVersion(%q) = %q, want %q", tc.input, gotDeb, tc.wantDeb)
			}
			gotRPM := FormatRPMVersion(tc.input)
			if gotRPM != tc.wantRPM {
				t.Errorf("FormatRPMVersion(%q) = %q, want %q", tc.input, gotRPM, tc.wantRPM)
			}
			gotArch := FormatArchVersion(tc.input)
			if gotArch != tc.wantArch {
				t.Errorf("FormatArchVersion(%q) = %q, want %q", tc.input, gotArch, tc.wantArch)
			}
			gotWin := FormatWindowsVersion(tc.input)
			if gotWin != tc.wantWindows {
				t.Errorf("FormatWindowsVersion(%q) = %q, want %q", tc.input, gotWin, tc.wantWindows)
			}
		})
	}
}
