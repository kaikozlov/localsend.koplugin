package localsend

import "testing"

func TestDeviceTypeNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mobile", "mobile"},
		{"desktop", "desktop"},
		{"web", "web"},
		{"headless", "headless"},
		{"server", "server"},
		{"unknown", "desktop"},
		{"", "desktop"},
	}

	for _, tt := range tests {
		got := normalizeDeviceType(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeDeviceType(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
