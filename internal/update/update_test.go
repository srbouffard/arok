package update

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current   string
		candidate string
		want      bool
	}{
		{"v0.1.3", "v0.2.0", true},
		{"v0.2.0", "v0.1.3", false},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.0", "v0.2.1", true},
		{"v1.0.0", "v2.0.0", true},
		{"v0.1.3-1-gabc123-dirty", "v0.2.0", true},  // dev build → newer release
		{"v0.2.0", "v0.1.3-1-gabc123-dirty", false}, // release → older dev build
		{"dev", "v0.2.0", false},                    // unparseable current
		{"v0.2.0", "dev", false},                    // unparseable candidate
	}
	for _, tc := range tests {
		got := IsNewer(tc.current, tc.candidate)
		if got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}
