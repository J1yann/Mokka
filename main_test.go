package main

import "testing"

func TestParsePrefixCommand(t *testing.T) {
	for _, tc := range []struct{ prefix, message, name, query string }{
		{"m!", "m!p Song title", "play", "Song title"},
		{".", ".play Song title", "play", "Song title"},
		{".", ".v 30", "volume", "30"},
		{".", "m!play Song title", "", ""},
		{".", " !info ", "info", ""},
		{"!", "!play Song title", "play", "Song title"},
		{"!", "!info", "info", ""},
	} {
		name, query := parsePrefixCommand(tc.message, tc.prefix)
		if name != tc.name || query != tc.query {
			t.Errorf("parsePrefixCommand(%q, %q) = %q, %q", tc.message, tc.prefix, name, query)
		}
	}
}
