package gitmeta

import "testing"

func TestNormalizeRemote(t *testing.T) {
	tests := map[string]string{
		"git@github.com:canonical/arok.git":       "https://github.com/canonical/arok",
		"https://github.com/canonical/arok.git":   "https://github.com/canonical/arok",
		"ssh://git@github.com/canonical/arok.git": "https://github.com/canonical/arok",
	}

	for input, want := range tests {
		if got := NormalizeRemote(input); got != want {
			t.Fatalf("NormalizeRemote(%q) = %q, want %q", input, got, want)
		}
	}
}
