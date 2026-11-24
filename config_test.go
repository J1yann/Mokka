package main

import (
	"slices"
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

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
			got, err := configuredGuilds(tt.plural, tt.single)
			if !slices.Equal(got, tt.want) || (err != nil) != (tt.want == nil) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}
