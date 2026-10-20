package config

import "testing"

func TestEffectiveUPXLevel(t *testing.T) {
	tests := []struct {
		level int
		want  int
	}{
		{0, 6},
		{-1, 6},
		{10, 6},
		{1, 1},
		{9, 9},
	}
	for _, tt := range tests {
		b := BinaryConfig{UPXLevel: tt.level}
		if got := b.EffectiveUPXLevel(); got != tt.want {
			t.Errorf("UPXLevel %d: EffectiveUPXLevel() = %d, want %d", tt.level, got, tt.want)
		}
	}
}
