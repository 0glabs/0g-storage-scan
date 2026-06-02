package sync

import "testing"

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"http://1.2.3.4:5678":    "1.2.3.4",
		"https://example.com:80": "example.com",
		"http://example.com":     "example.com",
		"1.2.3.4:5678":           "1.2.3.4", // scheme-less host:port
		"example.com":            "example.com",
		"":                       "",
	}
	for in, want := range cases {
		if got := hostFromURL(in); got != want {
			t.Errorf("hostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
