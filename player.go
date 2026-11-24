package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

type track struct {
	media
	id          string
	cardID      string
	query       string
	textChannel snowflake.ID
}

type player struct {
	ctx         context.Context
	client      *bot.Client
	guild       snowflake.ID
	extractor   string
	mediaProxy  string
	searchSlot  chan struct{}
	idleTimeout time.Duration
	wake        chan struct{}
	responseMu  sync.Mutex
	responses   map[snowflake.ID]responseCard

	mu         sync.Mutex
	channel    snowflake.ID
	queue      []track
	current    track
	cancel     context.CancelFunc
	running    bool
	leaving    bool
	repeat     bool
	generation uint64
	done       chan struct{}
}

func (p *player) enqueue(user, textChannel snowflake.ID, query string) string {
	if query == "" {
		return "Enter a song title or YouTube link."
	}
	if len(query) > 512 {
		return "Use a shorter search query."
	}
	state, ok := p.client.Caches.VoiceState(p.guild, user)
	if !ok || state.ChannelID == nil {
		return "Join a voice channel first."
	}
	channel := *state.ChannelID
	p.mu.Lock()
	generation := p.generation
	p.mu.Unlock()

	t := track{id: youtubeID(query), query: query, textChannel: textChannel}
	if t.id == "" {
		select {
		case p.searchSlot <- struct{}{}:
			defer func() { <-p.searchSlot }()
		default:
			return "Mokka is already searching. Try again in a moment."
		}
		result, err := extract(p.ctx, p.extractor, p.mediaProxy, "search", query)
		if err != nil || youtubeID(result.URL) == "" {
			return "Mokka couldn't find a playable YouTube result."
		}
		t.media, t.id = result, youtubeID(result.URL)
	}
	if t.Title == "" {
		t.Title = "YouTube video " + t.id
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok = p.client.Caches.VoiceState(p.guild, user)
	switch {
	case p.ctx.Err() != nil || generation != p.generation:
		return "Playback was stopped before the track could be queued."
	case !ok || state.ChannelID == nil || *state.ChannelID != channel:
		return "Join the voice channel again and retry."
	case p.leaving:
		return "Mokka is leaving voice. Try again in a moment."
	case p.channel != 0 && p.channel != channel:
		return "Mokka is playing in another voice channel."
	case len(p.queue) >= 25:
		return "The queue is full."
	}
	p.queue = append(p.queue, t)
	p.wakeWorker()
	p.channel = channel
	if !p.running {
		p.running = true
		p.done = make(chan struct{})
		go p.run()
	}
	return "Queued " + escapeCardText(label(t.Title))
}

func (p *player) wakeWorker() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *player) run() {
	watch, stopWatch := context.WithCancel(p.ctx)
	defer stopWatch()
	if p.idleTimeout > 0 {
		go p.watchVoice(watch)
	}
	var conn voice.Conn
	for {
		p.mu.Lock()
		if len(p.queue) == 0 && conn != nil && !p.leaving && p.ctx.Err() == nil {
			select {
			case <-p.wake:
			default:
			}
			p.mu.Unlock()
			p.waitIdle()
			continue
		}
		if len(p.queue) == 0 || p.leaving || p.ctx.Err() != nil {
			p.leaving = true
			p.mu.Unlock()
			p.closeVoice(conn)
			stopWatch()
			p.mu.Lock()
			p.running, p.leaving, p.channel = false, false, 0
			p.repeat = false
			close(p.done)
			p.mu.Unlock()
			return
		}
		t := p.queue[0]
		t.cardID = rand.Text()
		p.queue[0] = track{}
		p.queue = p.queue[1:]
		channel := p.channel
		ctx, cancel := context.WithCancel(p.ctx)
		p.current, p.cancel = t, cancel
		p.mu.Unlock()

		var err error
		for attempt := 0; attempt < 2; attempt++ {
			var started bool
			conn, started, err = p.playTrack(ctx, conn, channel, t)
			if err == nil || ctx.Err() != nil {
				break
			}
			p.closeVoice(conn)
			conn = nil
			if started || attempt == 1 {
				break
			}
			slog.Info("retrying track startup", "video", t.id, "error", err)
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("playback failed", "video", t.id, "error", err)
			p.notify(t, card("Playback failed", "Couldn't play "+escapeCardText(label(t.Title))+". Try another track or queue it again.", ""))
		}
		p.mu.Lock()
		if err == nil && ctx.Err() == nil && p.repeat && !p.leaving {
			p.queue = append([]track{t}, p.queue...)
		}
		cancel()
		p.current, p.cancel = track{}, nil
		p.mu.Unlock()
	}
}

func (p *player) playTrack(ctx context.Context, conn voice.Conn, channel snowflake.ID, t track) (voice.Conn, bool, error) {
	result, err := extract(ctx, p.extractor, p.mediaProxy, "resolve", t.id)
	if err != nil {
		return conn, false, fmt.Errorf("resolve audio: %w", err)
	}
	if conn == nil {
		conn = p.client.VoiceManager.CreateConn(p.guild)
		joinCtx, stop := context.WithTimeout(ctx, 12*time.Second)
		err = conn.Open(joinCtx, channel, false, true)
		stop()
		if err != nil {
			p.closeVoice(conn)
			return nil, false, fmt.Errorf("join voice: %w", err)
		}
	}
	if result.Title != "" {
		t.Title, t.Artist = result.Title, result.Artist
		p.mu.Lock()
		p.current = t
		p.mu.Unlock()
	}
	speakCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	err = conn.SetSpeaking(speakCtx, voice.SpeakingFlagMicrophone)
	stop()
	if err != nil {
		return conn, false, fmt.Errorf("start speaking: %w", err)
	}
	started := false
	err = playAudio(ctx, conn, result.URL, p.mediaProxy, func() {
		started = true
		go p.notify(t, nowPlayingCard(t))
	})
	return conn, started, err
}

func (p *player) waitIdle() {
	if p.idleTimeout == 0 {
		select {
		case <-p.ctx.Done():
		case <-p.wake:
		}
		return
	}
	timer := time.NewTimer(p.idleTimeout)
	defer timer.Stop()
	select {
	case <-p.ctx.Done():
	case <-p.wake:
	case <-timer.C:
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.leaveLocked()
		}
		p.mu.Unlock()
	}
}

func (p *player) watchVoice(ctx context.Context) {
	ticker := time.NewTicker(min(p.idleTimeout, time.Second))
	defer ticker.Stop()
	var emptySince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			p.mu.Lock()
			if ctx.Err() != nil || p.leaving {
				p.mu.Unlock()
				return
			}
			if p.hasListener(p.channel) {
				emptySince = time.Time{}
			} else if emptySince.IsZero() {
				emptySince = now
			} else if now.Sub(emptySince) >= p.idleTimeout {
				p.leaveLocked()
				p.mu.Unlock()
				return
			}
			p.mu.Unlock()
		}
	}
}

func (p *player) hasListener(channel snowflake.ID) bool {
	for state := range p.client.Caches.VoiceStates(p.guild) {
		if state.UserID != p.client.ID() && state.ChannelID != nil && *state.ChannelID == channel {
			return true
		}
	}
	return false
}

func (p *player) leaveLocked() {
	p.leaving = true
	p.generation++
	p.queue = nil
	p.repeat = false
	if p.cancel != nil {
		p.cancel()
	}
	p.wakeWorker()
}

func (p *player) closeVoice(conn voice.Conn) {
	if conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		conn.Close(ctx)
	}
}

func (p *player) notify(t track, message discord.MessageCreate) {
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	sendCard(ctx, p.client, t.textChannel, message)
}

func (p *player) controls(command string, user snowflake.ID, cardID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if command == "queue" {
		var lines []string
		if p.current.id != "" {
			lines = append(lines, "Now playing "+escapeCardText(label(p.current.Title)))
		}
		for i, t := range p.queue {
			if i == 10 {
				lines = append(lines, fmt.Sprintf("And %d more", len(p.queue)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, escapeCardText(label(t.Title))))
		}
		if len(lines) == 0 {
			return "The queue is empty."
		}
		return strings.Join(lines, "\n")
	}
	if cardID != "" && p.current.cardID != cardID {
		return "This track has ended. Use the current playback card."
	}
	if p.channel == 0 || (command == "skip" && p.cancel == nil && len(p.queue) == 0) {
		return "Nothing is playing."
	}
	state, ok := p.client.Caches.VoiceState(p.guild, user)
	if !ok || state.ChannelID == nil || *state.ChannelID != p.channel {
		return "Join Mokka's voice channel first."
	}
	if command == "skip" && p.cancel == nil {
		return "Wait for the next track to start."
	}
	if command == "skip" {
		p.cancel()
		return "Skipped " + escapeCardText(label(p.current.Title))
	}
	p.leaveLocked()
	return "Stopped playback and cleared the queue."
}

func (p *player) playbackCard(name, cardID string, user snowflake.ID) discord.MessageCreate {
	p.mu.Lock()
	t := p.current
	p.mu.Unlock()
	if t.cardID != cardID {
		return card("Track ended", "Use the current playback card.", "")
	}
	var message, lyrics string
	if name == "lyrics" {
		message, lyrics = p.lookupLyrics("", cardID)
	} else {
		message = p.controls(name, user, cardID)
	}
	return commandCard(name, message, lyrics)
}

func (p *player) lookupLyrics(query, cardID string) (string, string) {
	var title, artist string
	if query == "" {
		p.mu.Lock()
		t := p.current
		p.mu.Unlock()
		if cardID != "" && t.cardID != cardID {
			return "This track has ended. Use the current playback card.", ""
		}
		if t.id == "" {
			return "Nothing is playing. Enter a song title to look up lyrics.", ""
		}
		title, artist = lyricTrack(t.Title, t.Artist)
		if t.query != "" && youtubeID(t.query) == "" {
			query = t.query
		}
	}
	if len(query) > 512 {
		return "Use a shorter search query.", ""
	}
	ctx, cancel := context.WithTimeout(p.ctx, 12*time.Second)
	defer cancel()
	search := strings.TrimSpace(artist + " " + title)
	if search == "" {
		search = query
	}
	result, err := findLyrics(ctx, title, artist, search)
	if err == nil && result.Text == "" && title != "" && artist != "" {
		result, err = findLyrics(ctx, "", "", search)
	}
	if err == nil && result.Text == "" && query != "" && !strings.EqualFold(query, search) {
		result, err = findLyrics(ctx, "", "", query)
	}
	switch {
	case err != nil:
		return "The lyrics service didn't respond. Try again shortly.", ""
	case result.Text == "":
		return "No lyrics found. Try `m!lyrics <song title> <artist>`.", ""
	default:
		return "Lyrics for " + escapeCardText(label(result.Artist+" · "+result.Title)), result.Text
	}
}
