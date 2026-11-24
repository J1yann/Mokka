package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/thomas-vilte/dave-go/session"
)

var noMentions = &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}}

func main() {
	if err := run(); err != nil {
		slog.Error("Mokka stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig("mokka.conf")
	if err != nil {
		return err
	}
	token := cfg.value("DISCORD_TOKEN")
	guild, err := snowflake.Parse(cfg.value("MOKKA_GUILD_ID"))
	if err != nil || guild == 0 || token == "" {
		return errors.New("set DISCORD_TOKEN and MOKKA_GUILD_ID in mokka.conf or the environment")
	}
	extractor := cfg.value("MOKKA_EXTRACTOR")
	if extractor == "" {
		extractor = "mokka-extractor"
	}
	extractor, err = exec.LookPath(extractor)
	if err != nil {
		return errors.New("set MOKKA_EXTRACTOR to the path of the compiled extractor")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("FFmpeg must be installed")
	}
	mediaProxy := cfg.value("MOKKA_MEDIA_PROXY")
	if mediaProxy != "" {
		u, err := url.Parse(mediaProxy)
		if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("MOKKA_MEDIA_PROXY must be an HTTP proxy URL")
		}
	}
	idleTimeout := time.Hour
	if value := cfg.value("MOKKA_IDLE_TIMEOUT"); value != "" {
		idleTimeout, err = time.ParseDuration(value)
		if err != nil || (idleTimeout != 0 && idleTimeout < time.Second) {
			return errors.New("MOKKA_IDLE_TIMEOUT must be at least 1s, or 0 to disable")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := &player{ctx: ctx, guild: guild, extractor: extractor, mediaProxy: mediaProxy, searchSlot: make(chan struct{}, 1),
		responses: make(map[snowflake.ID]responseCard), idleTimeout: idleTimeout, wake: make(chan struct{}, 1)}
	slots := make(chan struct{}, 8)
	client, err := disgo.New(token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds, gateway.IntentGuildMessages, gateway.IntentMessageContent, gateway.IntentGuildVoiceStates)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagVoiceStates)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(session.CreateFunc())),
		bot.WithEventListenerFunc(func(e *events.MessageCreate) {
			if e.GuildID == nil || *e.GuildID != guild || e.Message.Author.Bot {
				return
			}
			raw, ok := strings.CutPrefix(e.Message.Content, "m!")
			if !ok {
				return
			}
			name, query, _ := strings.Cut(strings.Join(strings.Fields(raw), " "), " ")
			if name == "p" {
				name = "play"
			}
			switch name {
			case "play", "lyrics", "queue", "skip", "stop", "remove", "move", "shuffle", "repeat":
			default:
				return
			}
			slog.Info("prefix command received", "command", name, "channel", e.ChannelID)
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					message, text := p.command(name, strings.TrimSpace(query), e.Message.Author.ID, e.ChannelID)
					sendCard(ctx, e.Client(), e.ChannelID, commandCard(name, message, text))
				}()
			default:
			}
		}),
		bot.WithEventListenerFunc(func(e *events.ApplicationCommandInteractionCreate) {
			data, ok := e.Data.(discord.SlashCommandInteractionData)
			if !ok || e.GuildID() == nil || *e.GuildID() != guild {
				return
			}
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					if err := e.DeferCreateMessage(false); err != nil {
						slog.Warn("couldn't acknowledge command", "command", data.CommandName())
						return
					}
					query, _ := data.OptString("query")
					switch data.CommandName() {
					case "remove":
						query = strconv.Itoa(data.Int("position"))
					case "move":
						query = strconv.Itoa(data.Int("from")) + " " + strconv.Itoa(data.Int("to"))
					case "repeat":
						query, _ = data.OptString("mode")
					}
					slog.Info("slash command received", "command", data.CommandName(), "channel", e.Channel().ID())
					message, text := p.command(data.CommandName(), strings.TrimSpace(query), e.User().ID, e.Channel().ID())
					err := updateCard(e.Client(), e.ApplicationID(), e.Token(), commandCard(data.CommandName(), message, text))
					if err != nil {
						slog.Warn("couldn't update command response", "command", data.CommandName())
					}
				}()
			default:
				go func() {
					_ = e.CreateMessage(card("Playback", "Mokka is busy. Try again shortly.", ""))
				}()
			}
		}),
		bot.WithEventListenerFunc(func(e *events.ComponentInteractionCreate) {
			data, ok := e.Data.(discord.ButtonInteractionData)
			if !ok || e.GuildID() == nil || *e.GuildID() != guild {
				return
			}
			id, ok := strings.CutPrefix(data.CustomID(), "mokka:")
			if !ok {
				return
			}
			cardID, name, ok := strings.Cut(id, ":")
			if !ok || cardID == "" {
				return
			}
			switch name {
			case "queue", "lyrics", "skip", "stop":
			default:
				return
			}
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					if err := e.DeferUpdateMessage(); err != nil {
						slog.Warn("couldn't acknowledge playback button", "command", name)
						return
					}
					msg := p.playbackCard(name, cardID, e.User().ID)
					if err := p.showButtonResponse(e.Channel().ID(), e.ID(), msg); err != nil {
						slog.Warn("couldn't update playback button response", "command", name)
					}
				}()
			default:
				go func() { _ = e.DeferUpdateMessage() }()
			}
		}),
	)
	if err != nil {
		return err
	}
	p.client = client
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client.Close(closeCtx)
	}()

	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := client.Rest.SetGuildCommands(client.ApplicationID, guild, slashCommands(), rest.WithCtx(startCtx)); err != nil {
		return err
	}
	if err := client.OpenGateway(startCtx); err != nil {
		return err
	}
	slog.Info("Mokka connected", "guild", guild)
	<-ctx.Done()
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(6 * time.Second):
		}
	}
	return nil
}

func slashCommands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "play", Description: "Play a title or YouTube link",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: "query", Description: "Song title or YouTube link", Required: true}}},
		discord.SlashCommandCreate{Name: "lyrics", Description: "Find lyrics for a title or the current song",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: "query", Description: "Song title and optional artist"}}},
		discord.SlashCommandCreate{Name: "queue", Description: "Show the music queue"},
		discord.SlashCommandCreate{Name: "skip", Description: "Skip the current track"},
		discord.SlashCommandCreate{Name: "stop", Description: "Stop playback and clear the queue"},
		discord.SlashCommandCreate{Name: "remove", Description: "Remove a waiting track", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "position", Description: "Position in the queue", Required: true},
		}},
		discord.SlashCommandCreate{Name: "move", Description: "Move a waiting track", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "from", Description: "Current position", Required: true},
			discord.ApplicationCommandOptionInt{Name: "to", Description: "New position", Required: true},
		}},
		discord.SlashCommandCreate{Name: "shuffle", Description: "Shuffle the waiting tracks"},
		discord.SlashCommandCreate{Name: "repeat", Description: "Set repeat one or toggle it", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "mode", Description: "On or off"},
		}},
	}
}

func (p *player) command(name, query string, user, channel snowflake.ID) (string, string) {
	switch name {
	case "play":
		return p.enqueue(user, channel, query), ""
	case "lyrics":
		return p.lookupLyrics(query, "")
	case "queue", "skip", "stop":
		return p.controls(name, user, ""), ""
	case "remove", "move", "shuffle", "repeat":
		return p.editQueue(name, query, user), ""
	default:
		return "Unknown command.", ""
	}
}

func label(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if chars := []rune(text); len(chars) > 72 {
		return string(chars[:72]) + "…"
	}
	return text
}
