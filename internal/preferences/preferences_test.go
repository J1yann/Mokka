package preferences

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreferencesValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.json")
	defaults := Settings{Volume: 100}
	if got, err := Load(path); err != nil || got != defaults {
		t.Fatalf("missing preferences: %+v, %v", got, err)
	}
	for _, data := range []string{
		`{"autoplay":false,"autoplay":true,"volume":70}`,
		`{"autoplay":true,"volume":101}`,
		`{"autoplay":true,"volume":null}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := Load(path); err == nil || got != defaults {
			t.Errorf("invalid preferences %q: %+v, %v", data, got, err)
		}
	}
}
