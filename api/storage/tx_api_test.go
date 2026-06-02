package storage

import "testing"

func TestNormalizeStorageClass(t *testing.T) {
	cases := map[string]string{
		"":         "standard", // unclassified rows default to standard
		"hot":      "hot",
		"standard": "standard",
	}
	for in, want := range cases {
		if got := normalizeStorageClass(in); got != want {
			t.Errorf("normalizeStorageClass(%q) = %q, want %q", in, got, want)
		}
	}
}
