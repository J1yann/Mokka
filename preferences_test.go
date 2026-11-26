package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestConcurrentPreferenceChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "1.json")
	channel := snowflake.ID(3)
	p := &player{
		guild: 1, channel: channel, preferencesPath: path, volume: 100,
		client: &bot.Client{Caches: cache.New(cache.WithCaches(cache.FlagVoiceStates))},
	}
	p.client.Caches.AddVoiceState(discord.VoiceState{GuildID: 1, UserID: 2, ChannelID: &channel})

	var changes sync.WaitGroup
	changes.Go(func() {
		if got := p.editQueue("volume", "70", 2); got != "Volume set to 70%." {
			t.Error(got)
		}
	})
	changes.Go(func() {
		if got := p.editQueue("autoplay", "on", 2); got != "Autoplay is on." {
			t.Error(got)
		}
	})
	changes.Wait()

	want := preferences{Autoplay: true, Volume: 70}
	if got, err := loadPreferences(path); err != nil || got != want {
		t.Fatalf("reloaded preferences: %+v, %v", got, err)
	}
	// A regular file cannot be used as the parent of a new preferences file.
	p.preferencesPath = filepath.Join(path, "blocked.json")
	p.volumeUpdates = make(chan int, 1)
	if got := p.editQueue("volume", "30", 2); got != preferenceSaveMessage {
		t.Fatalf("failed save response: %q", got)
	}
	if p.volume != want.Volume || len(p.volumeUpdates) != 0 {
		t.Fatal("failed save changed playback volume")
	}
	if got, err := loadPreferences(path); err != nil || got != want {
		t.Fatalf("preferences after failed save: %+v, %v", got, err)
	}
}

func TestLoadPreferencesValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.json")
	defaults := preferences{Volume: 100}
	if got, err := loadPreferences(path); err != nil || got != defaults {
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
		if got, err := loadPreferences(path); err == nil || got != defaults {
			t.Errorf("invalid preferences %q: %+v, %v", data, got, err)
		}
	}
}
