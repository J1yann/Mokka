package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

const cardAccent = 0xc1845b

var cardMarkup = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "~", "\\~", ">", "\\>", "#", "\\#", "[", "\\[")

func escapeCardText(text string) string { return cardMarkup.Replace(text) }

func card(title, message, lyrics string) discord.MessageCreate {
	parts := []discord.ContainerSubComponent{discord.NewTextDisplay("### " + title + "\n" + message)}
	var files []*discord.File
	if lyrics != "" {
		if len(lyrics) <= 1800 {
			parts = append(parts, discord.NewTextDisplay(lyrics))
		} else {
			files = []*discord.File{discord.NewFile("lyrics.txt", "Song lyrics", strings.NewReader(lyrics))}
			parts = append(parts, discord.NewFileComponent("attachment://lyrics.txt"))
		}
	}
	container := discord.NewContainer(parts...)
	container.AccentColor = cardAccent
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{container}, Files: files,
		AllowedMentions: noMentions, Flags: discord.MessageFlagIsComponentsV2,
	}
}

func commandCard(name, message, lyrics string) discord.MessageCreate {
	title := "Playback"
	switch name {
	case "info":
		title = "About Mokka"
	case "help":
		title = "Mokka commands"
	case "queue":
		title = "Queue"
	case "detail":
		title = "Playback details"
	case "lyrics":
		title = "Lyrics"
	}
	return card(title, message, lyrics)
}

func nowPlayingCard(t track) discord.MessageCreate {
	title := "[" + escapeCardText(label(t.Title)) + "](https://www.youtube.com/watch?v=" + t.id + ")"
	if t.Artist != "" {
		title += "\n" + escapeCardText(label(t.Artist))
	}
	msg := card("Now playing", title, "")
	container := msg.Components[0].(discord.ContainerComponent)
	container.Components[0] = discord.NewSection(container.Components[0].(discord.TextDisplayComponent)).
		WithAccessory(discord.NewThumbnail("https://i.ytimg.com/vi/" + t.id + "/hqdefault.jpg").
			WithDescription("YouTube thumbnail"))
	msg.Components[0] = container
	id := "mokka:" + t.cardID + ":"
	container.Components = append(container.Components, discord.NewActionRow(
		discord.NewSecondaryButton("Show queue", id+"queue"),
		discord.NewSecondaryButton("Show lyrics", id+"lyrics"),
		discord.NewPrimaryButton("Skip track", id+"skip"),
		discord.NewDangerButton("Stop playback", id+"stop"),
	))
	msg.Components[0] = container
	return msg
}

func updateCard(client *bot.Client, app snowflake.ID, token string, msg discord.MessageCreate) error {
	_, err := client.Rest.UpdateInteractionResponse(app, token, cardUpdate(msg))
	return err
}

func cardUpdate(msg discord.MessageCreate) discord.MessageUpdate {
	return discord.MessageUpdate{
		Components: &msg.Components, Flags: &msg.Flags, Files: msg.Files, AllowedMentions: msg.AllowedMentions,
		Attachments: &[]discord.AttachmentUpdate{},
	}
}

type responseCard struct {
	messageID     snowflake.ID
	interactionID snowflake.ID
}

func (p *player) showButtonResponse(channel, interaction snowflake.ID, msg discord.MessageCreate) error {
	p.responseMu.Lock()
	defer p.responseMu.Unlock()
	entry := p.responses[channel]
	if interaction <= entry.interactionID {
		return nil
	}
	p.responses[channel] = responseCard{messageID: entry.messageID, interactionID: interaction}
	moveToBottom := false
	if entry.messageID != 0 {
		historyCtx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
		latest, err := p.client.Rest.GetMessages(channel, 0, 0, 0, 1, rest.WithCtx(historyCtx))
		cancel()
		moveToBottom = err == nil && len(latest) > 0 && latest[0].ID > entry.messageID
	}
	entry.interactionID = interaction
	return p.writeResponseCard(channel, entry, msg, moveToBottom)
}

func (p *player) showPlaybackFinished(channel snowflake.ID, noRecommendation bool) {
	p.responseMu.Lock()
	defer p.responseMu.Unlock()
	p.mu.Lock()
	idle := p.current.id == "" && len(p.queue) == 0 && !p.leaving && p.ctx.Err() == nil
	p.mu.Unlock()
	if !idle {
		return
	}
	message := "Nothing else is queued. Use `m!p <song title>` or `/play` to play another song."
	if noRecommendation {
		message = "Couldn’t find another track. Use `m!p <song title>` or `/play` to play another song."
	}
	msg := card("Playback finished", message, "")
	if err := p.writeResponseCard(channel, p.responses[channel], msg, false); err != nil && p.ctx.Err() == nil {
		slog.Warn("couldn't show playback finished card", "channel", channel, "error", err)
	}
}

func (p *player) showPlaybackInterrupted(channel snowflake.ID, title string) {
	p.responseMu.Lock()
	defer p.responseMu.Unlock()
	msg := card("Playback interrupted", "Couldn't resume "+title+". You can queue it again.", "")
	if err := p.writeResponseCard(channel, p.responses[channel], msg, false); err != nil && p.ctx.Err() == nil {
		slog.Warn("couldn't show playback interruption", "channel", channel, "error", err)
	}
}

// writeResponseCard runs with responseMu held.
func (p *player) writeResponseCard(channel snowflake.ID, entry responseCard, msg discord.MessageCreate, moveToBottom bool) error {
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	if entry.messageID != 0 && !moveToBottom {
		_, err := p.client.Rest.UpdateMessage(channel, entry.messageID, cardUpdate(msg), rest.WithCtx(ctx))
		if err == nil {
			return nil
		}
		var apiErr *rest.Error
		if !errors.As(err, &apiErr) || apiErr.Code != rest.JSONErrorCodeUnknownMessage {
			return err
		}
	}
	created, err := p.client.Rest.CreateMessage(channel, msg, rest.WithCtx(ctx))
	if err != nil {
		return err
	}
	entryCreated := entry
	entryCreated.messageID = created.ID
	p.responses[channel] = entryCreated
	if moveToBottom {
		if err := p.client.Rest.DeleteMessage(channel, entry.messageID, rest.WithCtx(ctx)); err != nil {
			var apiErr *rest.Error
			if !errors.As(err, &apiErr) || apiErr.Code != rest.JSONErrorCodeUnknownMessage {
				slog.Warn("couldn't remove previous response card", "channel", channel, "error", err)
			}
		}
	}
	return nil
}

func sendCard(ctx context.Context, client *bot.Client, channel snowflake.ID, msg discord.MessageCreate) {
	if _, err := client.Rest.CreateMessage(channel, msg, rest.WithCtx(ctx)); err != nil && ctx.Err() == nil {
		slog.Warn("couldn't send channel card", "channel", channel, "error", err)
	}
}

func (p *player) showReconnect(ctx context.Context, channel snowflake.ID, result <-chan string) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-result:
		return
	case <-timer.C:
	}
	requestCtx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	msg, err := p.client.Rest.CreateMessage(channel, card("Playback", "Reconnecting…", ""), rest.WithCtx(requestCtx))
	cancel()
	if err != nil {
		return
	}
	message := <-result
	requestCtx, cancel = context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	if _, err := p.client.Rest.UpdateMessage(channel, msg.ID, cardUpdate(card("Playback", message, "")), rest.WithCtx(requestCtx)); err != nil && requestCtx.Err() == nil {
		slog.Warn("couldn't update reconnect notice", "channel", channel, "error", err)
	}
}
