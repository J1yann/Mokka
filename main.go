package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

	"mokka/internal/config"
	"mokka/internal/player"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Mokka stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("mokka.conf")
	if err != nil {
		return err
	}
	token := cfg.Value("DISCORD_TOKEN")
	if token == "" {
		return errors.New("set DISCORD_TOKEN in mokka.conf or the environment")
	}
	guilds, err := config.Guilds(cfg.Value("MOKKA_GUILD_IDS"), cfg.Value("MOKKA_GUILD_ID"))
	if err != nil {
		return err
	}
	extractor := cfg.Value("MOKKA_EXTRACTOR")
	if extractor == "" {
		extractor = "mokka-extractor"
	}
	extractor, err = exec.LookPath(extractor)
	if err != nil {
		return errors.New("set MOKKA_EXTRACTOR to the path of the compiled extractor")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("FFmpeg must be installed. On Debian or Ubuntu, run `sudo apt update && sudo apt install ffmpeg`")
	}
	if output, err := exec.Command("ffmpeg", "-hide_banner", "-h", "filter=azmq").Output(); err != nil || !strings.HasPrefix(string(output), "Filter azmq") {
		return errors.New("FFmpeg must include the azmq filter (libzmq). On Debian or Ubuntu, run `sudo apt update && sudo apt install ffmpeg` and make sure the packaged ffmpeg is first on PATH")
	}
	opusBitrate, err := config.OpusBitrate(cfg.Value("MOKKA_OPUS_BITRATE_KBPS"))
	if err != nil {
		return err
	}
	mediaProxy := cfg.Value("MOKKA_MEDIA_PROXY")
	if mediaProxy != "" {
		u, err := url.Parse(mediaProxy)
		if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("MOKKA_MEDIA_PROXY must be an HTTP proxy URL")
		}
	}
	idleTimeout := time.Hour
	if value := cfg.Value("MOKKA_IDLE_TIMEOUT"); value != "" {
		idleTimeout, err = time.ParseDuration(value)
		if err != nil || (idleTimeout != 0 && idleTimeout < time.Second) {
			return errors.New("MOKKA_IDLE_TIMEOUT must be at least 1s, or 0 to disable")
		}
	}

	prefix, err := config.Prefix(cfg.Value("MOKKA_PREFIX"))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stateDir := cfg.Value("MOKKA_STATE_DIR")
	if stateDir == "" {
		stateDir = "mokka-state"
	}
	players := make(map[snowflake.ID]*player.Player, len(guilds))
	findPlayer := func(guild *snowflake.ID) *player.Player {
		if guild == nil {
			return nil
		}
		return players[*guild]
	}
	slots := make(chan struct{}, 8)
	client, err := disgo.New(token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds, gateway.IntentGuildMessages, gateway.IntentMessageContent, gateway.IntentGuildVoiceStates)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagVoiceStates)),
		bot.WithVoiceManagerConfigOpts(
			voice.WithDaveSessionCreateFunc(session.CreateFunc()),
			voice.WithConnCreateFunc(player.NewManagedVoiceConn),
		),
		bot.WithEventListenerFunc(func(e *events.GuildJoin) {
			if players[e.GuildID] != nil {
				go registerGuildCommands(ctx, e.Client(), e.GuildID)
			}
		}),
		bot.WithEventListenerFunc(func(e *events.MessageCreate) {
			p := findPlayer(e.GuildID)
			if p == nil || e.Message.Author.Bot {
				return
			}
			name, query := parsePrefixCommand(e.Message.Content, prefix)
			switch name {
			case "play", "lyrics", "queue", "skip", "stop", "remove", "move", "shuffle", "repeat", "autoplay", "volume", "detail", "info":
			default:
				return
			}
			slog.Info("prefix command received", "command", name, "channel", e.ChannelID)
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					message, text := p.Command(name, strings.TrimSpace(query), e.Message.Author.ID, e.ChannelID)
					player.SendCard(ctx, e.Client(), e.ChannelID, player.CommandCard(name, message, text))
				}()
			default:
			}
		}),
		bot.WithEventListenerFunc(func(e *events.ApplicationCommandInteractionCreate) {
			data, ok := e.Data.(discord.SlashCommandInteractionData)
			p := findPlayer(e.GuildID())
			if !ok || p == nil {
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
					case "volume":
						if level, ok := data.OptInt("level"); ok {
							query = strconv.Itoa(level)
						}
					case "repeat", "autoplay":
						query, _ = data.OptString("mode")
					}
					slog.Info("slash command received", "command", data.CommandName(), "channel", e.Channel().ID())
					message, text := p.Command(data.CommandName(), strings.TrimSpace(query), e.User().ID, e.Channel().ID())
					err := player.UpdateCard(e.Client(), e.ApplicationID(), e.Token(), player.CommandCard(data.CommandName(), message, text))
					if err != nil {
						slog.Warn("couldn't update command response", "command", data.CommandName())
					}
				}()
			default:
				go func() {
					_ = e.CreateMessage(player.BusyCard())
				}()
			}
		}),
		bot.WithEventListenerFunc(func(e *events.ComponentInteractionCreate) {
			data, ok := e.Data.(discord.ButtonInteractionData)
			p := findPlayer(e.GuildID())
			if !ok || p == nil {
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
					msg := p.PlaybackCard(name, cardID, e.User().ID)
					if err := p.ShowButtonResponse(e.Channel().ID(), e.ID(), msg); err != nil {
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
	options := player.Options{
		Prefix: prefix, Extractor: extractor, MediaProxy: mediaProxy,
		IdleTimeout: idleTimeout, OpusBitrate: opusBitrate, PlaybackSlots: make(chan struct{}, 3),
	}
	for _, guild := range guilds {
		path := filepath.Join(stateDir, guild.String()+".json")
		players[guild] = player.New(ctx, client, guild, path, options)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client.Close(closeCtx)
	}()

	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := client.OpenGateway(startCtx); err != nil {
		return err
	}
	slog.Info("Mokka connected", "allowed_servers", len(guilds))
	go func() {
		for _, guild := range guilds {
			if ctx.Err() != nil {
				return
			}
			registerGuildCommands(ctx, client, guild)
		}
	}()
	<-ctx.Done()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancelShutdown()
	for _, p := range players {
		if done := p.Done(); done != nil {
			select {
			case <-done:
			case <-shutdown.Done():
				return nil
			}
		}
	}
	return nil
}

func registerGuildCommands(ctx context.Context, client *bot.Client, guild snowflake.ID) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.Rest.SetGuildCommands(client.ApplicationID, guild, player.SlashCommands(), rest.WithCtx(ctx)); err != nil && ctx.Err() == nil {
		slog.Warn("couldn't register slash commands", "guild", guild, "error", err)
	}
}

func parsePrefixCommand(content, prefix string) (string, string) {
	if strings.TrimSpace(content) == "!info" {
		return "info", ""
	}
	raw, ok := strings.CutPrefix(content, prefix)
	if !ok {
		return "", ""
	}
	name, query, _ := strings.Cut(strings.Join(strings.Fields(raw), " "), " ")
	switch name {
	case "p":
		name = "play"
	case "v":
		name = "volume"
	}
	return name, query
}
