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
	case "queue":
		title = "Queue"
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
	p.responses[channel] = responseCard{messageID: created.ID, interactionID: interaction}
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
