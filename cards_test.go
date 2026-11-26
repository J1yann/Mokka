package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
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
	p := &player{current: track{cardID: "new-track"}, cancel: func(error) { canceled = true }}
	if got := p.controls("skip", 0, "this-track"); got != "This track has ended. Use the current playback card." || canceled {
		t.Fatalf("old button affected the current track: %q, canceled=%t", got, canceled)
	}
}

func TestCommandRequiresVoice(t *testing.T) {
	p := &player{
		guild:  1,
		client: &bot.Client{Caches: cache.New(cache.WithCaches(cache.FlagVoiceStates))},
	}
	const user snowflake.ID = 2
	for _, name := range []string{"play", "lyrics", "queue", "skip", "stop", "remove", "move", "shuffle", "repeat", "autoplay", "volume", "detail", "info", "help"} {
		if message, _ := p.command(name, "", user, 3); message != joinVoiceMessage {
			t.Errorf("%s outside voice: got %q", name, message)
		}
	}
	channel := snowflake.ID(4)
	p.client.Caches.AddVoiceState(discord.VoiceState{GuildID: p.guild, UserID: user, ChannelID: &channel})
	help, _ := p.command("help", "", user, 3)
	for _, command := range slashCommands() {
		if !strings.Contains(help, "`/"+command.CommandName()+"`") {
			t.Errorf("help is missing /%s", command.CommandName())
		}
	}
	p.autoplay, p.volume = true, 70
	if message, _ := p.command("detail", "", user, 3); !strings.Contains(message, "**Autoplay:** On") || !strings.Contains(message, "**Volume:** 70%") {
		t.Fatalf("details in voice: got %q", message)
	}
	if message, _ := p.command("queue", "", user, 3); message != "The queue is empty." {
		t.Fatalf("queue in voice: got %q", message)
	}
}

func TestEmptyVoicePausesAndWaitsForListener(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	audioCtx, pause := context.WithCancelCause(ctx)
	defer pause(nil)
	const channel snowflake.ID = 4
	p := &player{
		client: &bot.Client{Caches: cache.New(cache.WithCaches(cache.FlagVoiceStates))},
		guild:  1, channel: channel, pauseAudio: pause,
	}
	go p.watchVoice(ctx)
	<-audioCtx.Done()
	if cause := context.Cause(audioCtx); cause != errNoListeners {
		t.Fatalf("empty voice pause: %v", cause)
	}
	resumed := make(chan error, 1)
	go func() { resumed <- p.waitForListener(ctx, channel) }()
	select {
	case err := <-resumed:
		t.Fatalf("resumed while still empty: %v", err)
	default:
	}
	joined := channel
	p.client.Caches.AddVoiceState(discord.VoiceState{GuildID: p.guild, UserID: 2, ChannelID: &joined})
	if err := <-resumed; err != nil {
		t.Fatalf("listener joined but playback stayed paused: %v", err)
	}
}

func TestSkipBypassesRepeatAndStopCancelsLookup(t *testing.T) {
	playCtx, cancelPlay := context.WithCancelCause(context.Background())
	defer cancelPlay(nil)
	channel := snowflake.ID(4)
	p := &player{
		client: &bot.Client{Caches: cache.New(cache.WithCaches(cache.FlagVoiceStates))},
		guild:  1, channel: channel, autoplay: true, repeat: true,
		current: track{id: "song", media: media{Title: "Song"}}, cancel: cancelPlay,
	}
	p.client.Caches.AddVoiceState(discord.VoiceState{GuildID: 1, UserID: 2, ChannelID: &channel})
	if p.canAutoplay(false) {
		t.Fatal("a normally finished track should repeat")
	}
	if got := p.controls("skip", 2, ""); got != "Skipped Song. Finding the next track…" {
		t.Fatalf("skip response: %q", got)
	}
	if context.Cause(playCtx) != errTrackSkipped || !p.repeat || !p.autoplay || !p.canAutoplay(true) {
		t.Fatal("skip should allow autoplay without changing repeat or autoplay settings")
	}
	p.queue = []track{{id: "requested"}}
	if p.canAutoplay(true) {
		t.Fatal("the queued request should take priority")
	}

	lookupCtx, cancelLookup := context.WithCancelCause(context.Background())
	defer cancelLookup(nil)
	p.cancel, p.findingNext = cancelLookup, true
	if got := p.controls("skip", 2, ""); got != "Finding the next track…" || lookupCtx.Err() != nil {
		t.Fatalf("duplicate skip interrupted the lookup: %q, %v", got, lookupCtx.Err())
	}
	p.controls("stop", 2, "")
	if !p.leaving || lookupCtx.Err() == nil || len(p.queue) != 0 {
		t.Fatal("stop should cancel the lookup and clear the queue")
	}
}
