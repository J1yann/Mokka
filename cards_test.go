package main

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
)

func TestNowPlayingButtonsUseTrackID(t *testing.T) {
	msg := nowPlayingCard(track{id: "dQw4w9WgXcQ", cardID: "this-track", media: media{Title: "Song"}})
	container := msg.Components[0].(discord.ContainerComponent)
	row := container.Components[1].(discord.ActionRowComponent)
	for _, action := range row.Components {
		button := action.(discord.ButtonComponent)
		if !strings.HasPrefix(button.CustomID, "mokka:this-track:") {
			t.Fatalf("unexpected button ID %q", button.CustomID)
		}
	}
	canceled := false
	p := &player{current: track{cardID: "new-track"}, cancel: func() { canceled = true }}
	if got := p.controls("skip", 0, "this-track"); got != "This track has ended. Use the current playback card." || canceled {
		t.Fatalf("old button affected the current track: %q, canceled=%t", got, canceled)
	}
}
