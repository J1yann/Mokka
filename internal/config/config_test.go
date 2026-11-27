package config

import (
	"slices"
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

func TestConfiguredOpusBitrate(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  int
	}{
		{"", 96}, {" 128 ", 128}, {"6", 6}, {"510", 510},
		{"5", 0}, {"511", 0}, {"128k", 0},
	} {
		got, err := OpusBitrate(tt.input)
		if got != tt.want || (err != nil) != (tt.want == 0) {
			t.Errorf("bitrate %q: got %d, %v; want %d", tt.input, got, err, tt.want)
		}
	}
}

func TestConfiguredGuilds(t *testing.T) {
	for _, tt := range []struct {
		name, plural, single string
		want                 []snowflake.ID
	}{
		{name: "legacy", single: "42", want: []snowflake.ID{42}},
		{name: "allowlist overrides legacy and deduplicates", plural: "42, 43, 42", single: "99", want: []snowflake.ID{42, 43}},
		{name: "invalid allowlist", plural: "42, ", single: "99"},
		{name: "missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Guilds(tt.plural, tt.single)
			if !slices.Equal(got, tt.want) || (err != nil) != (tt.want == nil) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestConfiguredPrefix(t *testing.T) {
	for input, want := range map[string]string{"": "m!", " . ": ".", "!": "!", "m!": "m!", "two words": "", "/": "", "`": "", "1234567890123": ""} {
		got, err := Prefix(input)
		if got != want || (err != nil) != (want == "") {
			t.Errorf("Prefix(%q) = %q, %v, want %q", input, got, err, want)
		}
	}
}
