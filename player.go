package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

type track struct {
	media
	id           string
	cardID       string
	query        string
	textChannel  snowflake.ID
	autoplayPick bool
}

var (
	errNoListeners  = errors.New("no listeners in voice")
	errTrackSkipped = errors.New("track skipped")
)

type player struct {
	ctx             context.Context
	client          *bot.Client
	guild           snowflake.ID
	prefix          string
	extractor       string
	preferencesPath string
	mediaProxy      string
	searchSlot      chan struct{}
	playbackSlots   chan struct{}
	idleTimeout     time.Duration
	opusBitrate     int
	wake            chan struct{}
	responseMu      sync.Mutex
	responses       map[snowflake.ID]responseCard

	mu            sync.Mutex
	channel       snowflake.ID
	queue         []track
	current       track
	cancel        context.CancelCauseFunc
	pauseAudio    context.CancelCauseFunc
	volumeUpdates chan int
	volume        int
	running       bool
	leaving       bool
	idleExpired   bool
	repeat        bool
	autoplay      bool
	waiting       bool
	findingNext   bool
	generation    uint64
	done          chan struct{}
}

const (
	joinVoiceMessage      = "Join a voice channel to use this command."
	joinMokkaVoiceMessage = "Join Mokka’s voice channel and try again."
)

func (p *player) inVoice(user snowflake.ID) bool {
	state, ok := p.client.Caches.VoiceState(p.guild, user)
	return ok && state.ChannelID != nil
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
		return joinVoiceMessage
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
		return "Rejoin the voice channel and request the song again."
	case p.leaving:
		return "Mokka is leaving voice. Try again in a moment."
	case p.channel != 0 && p.channel != channel:
		return joinMokkaVoiceMessage
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
	title := escapeCardText(label(t.Title))
	if len(p.queue) == 1 {
		return "Added **" + title + "** to the queue. It’s next."
	}
	return fmt.Sprintf("Added **%s** to the queue at position %d.", title, len(p.queue))
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
	go p.watchVoice(watch)
	var conn voice.Conn
	var recent []string
	var picks []track
	var advanceAutoplay, autoplayAfterSkip bool
	var lastTrack track
	for {
		p.mu.Lock()
		playRecommendation := advanceAutoplay && p.canAutoplay(autoplayAfterSkip)
		advanceAutoplay = false
		if len(p.queue) == 0 && !playRecommendation && conn != nil && !p.leaving && p.ctx.Err() == nil {
			select {
			case <-p.wake:
			default:
			}
			p.mu.Unlock()
			p.waitIdle()
			continue
		}
		if (len(p.queue) == 0 && !playRecommendation) || p.leaving || p.ctx.Err() != nil {
			p.leaving = true
			p.mu.Unlock()
			p.closeVoice(conn)
			stopWatch()
			p.mu.Lock()
			expired := p.idleExpired
			p.idleExpired = false
			p.running, p.leaving, p.channel = false, false, 0
			p.repeat = false
			close(p.done)
			p.mu.Unlock()
			if expired && lastTrack.textChannel != 0 {
				p.notify(lastTrack, card("Left voice", "Mokka left after being idle. Play a song to bring it back.", ""))
			}
			return
		}
		var t track
		if playRecommendation {
			t = picks[0]
			picks = picks[1:]
			t.textChannel = lastTrack.textChannel
			t.autoplayPick = true
		} else {
			t = p.queue[0]
			p.queue[0] = track{}
			p.queue = p.queue[1:]
			if !t.autoplayPick {
				picks = nil
			}
		}
		lastTrack = t
		t.cardID = rand.Text()
		recent = append(recent, t.id)
		if len(recent) > 20 {
			recent = recent[1:]
		}
		channel := p.channel
		ctx, cancel := context.WithCancelCause(p.ctx)
		p.current, p.cancel = t, cancel
		p.mu.Unlock()

		if playRecommendation {
			go p.notify(t, card("Autoplay", "Playing "+escapeCardText(label(t.Title))+" next from recommendations.", ""))
		}
		var sent time.Duration
		var err error
		conn, sent, err = p.playQueuedTrack(ctx, conn, channel, t)
		if err != nil && ctx.Err() == nil {
			slog.Warn("playback failed", "video", t.id, "error", err)
			title := escapeCardText(label(t.Title))
			if sent > 0 {
				p.showPlaybackInterrupted(t.textChannel, title)
			} else {
				p.notify(t, card("Playback failed", "Couldn't play "+title+". Try another track or queue it again.", ""))
			}
		}
		p.mu.Lock()
		finished := err == nil && ctx.Err() == nil && !p.leaving
		skipped := context.Cause(ctx) == errTrackSkipped && !p.leaving
		noRecommendation := false
		if (finished || skipped) && p.canAutoplay(skipped) {
			picks = slices.DeleteFunc(picks, func(candidate track) bool {
				return slices.Contains(recent, candidate.id)
			})
			if len(picks) == 0 {
				lookupCtx, cancelLookup := context.WithCancelCause(p.ctx)
				p.cancel, p.findingNext = cancelLookup, true
				p.mu.Unlock()
				next, lookupErr := p.nextTracks(lookupCtx, t, recent)
				if lookupErr != nil && lookupCtx.Err() == nil {
					slog.Warn("autoplay lookup failed", "video", t.id, "error", lookupErr)
				}
				p.mu.Lock()
				active := lookupCtx.Err() == nil && !p.leaving
				cancelLookup(nil)
				p.findingNext = false
				if active && p.autoplay {
					picks = next
					noRecommendation = len(next) == 0 && p.canAutoplay(skipped)
				}
			}
			advanceAutoplay = len(picks) > 0 && p.canAutoplay(skipped)
			autoplayAfterSkip = skipped
		}
		if finished && !p.leaving && p.ctx.Err() == nil && p.repeat {
			p.queue = append([]track{t}, p.queue...)
		}
		queueFinished := (finished || skipped) && !p.leaving && p.ctx.Err() == nil && len(p.queue) == 0 && !advanceAutoplay
		cancel(nil)
		p.current, p.cancel, p.waiting = track{}, nil, false
		p.mu.Unlock()
		if queueFinished {
			p.showPlaybackFinished(t.textChannel, noRecommendation)
		}
	}
}

// canAutoplay runs with the player's mutex held.
func (p *player) canAutoplay(skipped bool) bool {
	return p.autoplay && (skipped || !p.repeat) && len(p.queue) == 0 && p.hasListener(p.channel)
}

func (p *player) playQueuedTrack(ctx context.Context, conn voice.Conn, channel snowflake.ID, t track) (voice.Conn, time.Duration, error) {
	var sent time.Duration
	var pauseNotice chan struct{}
	onResume := func() {
		if pauseNotice == nil {
			return
		}
		posted := pauseNotice
		pauseNotice = nil
		go func() {
			<-posted
			p.notify(t, card("Playback resumed", "Picking up where the song paused.", ""))
		}()
	}
	for {
		if err := p.waitForListener(ctx, channel); err != nil {
			return conn, sent, err
		}
		audioCtx, cancel := context.WithCancelCause(ctx)
		p.mu.Lock()
		p.pauseAudio = cancel
		if !p.hasListener(channel) {
			cancel(errNoListeners)
		}
		p.mu.Unlock()

		var err error
		conn, sent, err = p.playWhileListening(audioCtx, conn, channel, t, sent, onResume)
		p.mu.Lock()
		p.pauseAudio = nil
		p.mu.Unlock()
		paused := context.Cause(audioCtx) == errNoListeners
		cancel(nil)
		if err == nil || !paused || ctx.Err() != nil {
			return conn, sent, err
		}
		if sent > 0 && pauseNotice == nil {
			posted := make(chan struct{})
			pauseNotice = posted
			go func() {
				defer close(posted)
				p.notify(t, card("Playback paused", "The voice channel is empty. Join to pick up where the song left off.", ""))
			}()
		}
	}
}

func (p *player) waitForListener(ctx context.Context, channel snowflake.ID) error {
	if p.hasListener(channel) {
		return ctx.Err()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for !p.hasListener(channel) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return ctx.Err()
}

func (p *player) playWhileListening(ctx context.Context, conn voice.Conn, channel snowflake.ID, t track, sent time.Duration, onResume func()) (voice.Conn, time.Duration, error) {
	select {
	case p.playbackSlots <- struct{}{}:
	default:
		p.mu.Lock()
		p.waiting = true
		p.mu.Unlock()
		select {
		case p.playbackSlots <- struct{}{}:
		case <-ctx.Done():
			p.mu.Lock()
			p.waiting = false
			p.mu.Unlock()
			return conn, sent, ctx.Err()
		}
		p.mu.Lock()
		p.waiting = false
		p.mu.Unlock()
	}
	defer func() { <-p.playbackSlots }()
	if err := ctx.Err(); err != nil {
		return conn, sent, err
	}
	mediaRetried := false
	conn, played, err := p.playTrack(ctx, conn, channel, t, sent, onResume)
	sent += played
	for err != nil && ctx.Err() == nil {
		var voiceErr voiceWriteError
		if errors.As(err, &voiceErr) {
			var resumed bool
			slog.Info("recovering voice", "video", t.id, "resume_at", sent, "error", err)
			conn, sent, err, resumed = p.recoverVoicePlayback(ctx, conn, channel, t, sent, onResume)
			if !resumed {
				break
			}
			continue
		}
		if mediaRetried {
			break
		}
		mediaRetried = true
		slog.Info("retrying playback", "video", t.id, "resume_at", sent, "error", err)
		conn, played, err = p.playTrack(ctx, conn, channel, t, sent, onResume)
		sent += played
	}
	return conn, sent, err
}

func (p *player) recoverVoicePlayback(ctx context.Context, conn voice.Conn, channel snowflake.ID, t track, sent time.Duration, onResume func()) (voice.Conn, time.Duration, error, bool) {
	recovery, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(30*time.Second, func() { cancel(context.DeadlineExceeded) })
	defer timer.Stop()

	result := make(chan string, 1)
	go p.showReconnect(ctx, t.textChannel, result)
	resumed := false
	defer func() {
		if !resumed {
			message := "Playback couldn’t reconnect."
			if ctx.Err() != nil {
				message = "Reconnect canceled."
			}
			result <- message
		}
	}()
	onAudio := func() {
		// Once audio is flowing, the recovery deadline must not stop the song.
		if timer.Stop() && recovery.Err() == nil {
			resumed = true
			slog.Info("voice playback recovered", "video", t.id, "resume_at", sent)
			result <- "Playback resumed."
			onResume()
		}
	}
	shard, err := p.client.Shard(p.guild)
	if err != nil {
		p.closeVoice(conn)
		return nil, sent, err, false
	}
	delay := time.Second
	for {
		p.closeVoice(conn)
		conn = nil
		if err := waitForGateway(recovery, shard); err != nil {
			return nil, sent, fmt.Errorf("reconnect to Discord: %w", err), false
		}
		var played time.Duration
		conn, played, err = p.playTrack(recovery, nil, channel, t, sent, onAudio)
		sent += played
		var voiceErr voiceWriteError
		if resumed || err == nil || !errors.As(err, &voiceErr) {
			if cause := context.Cause(recovery); cause != nil {
				err = fmt.Errorf("reconnect to Discord: %w", cause)
			}
			return conn, sent, err, resumed
		}
		slog.Info("voice rejoin failed", "video", t.id, "retry_in", delay, "error", err)
		select {
		case <-recovery.Done():
		case <-time.After(delay):
			delay = min(2*delay, 4*time.Second)
		}
	}
}

func waitForGateway(ctx context.Context, shard gateway.Gateway) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for shard.Status() != gateway.StatusReady {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
	return context.Cause(ctx)
}

func (p *player) playTrack(ctx context.Context, conn voice.Conn, channel snowflake.ID, t track, start time.Duration, onResume func()) (voice.Conn, time.Duration, error) {
	result, err := extract(ctx, p.extractor, p.mediaProxy, "resolve", t.id)
	if err != nil {
		return conn, 0, fmt.Errorf("resolve audio: %w", err)
	}
	if conn == nil {
		conn = p.client.VoiceManager.CreateConn(p.guild)
		joinCtx, stop := context.WithTimeout(ctx, 12*time.Second)
		err = conn.Open(joinCtx, channel, false, true)
		stop()
		if err != nil {
			p.closeVoice(conn)
			return nil, 0, voiceWriteError{fmt.Errorf("join voice: %w", err)}
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
		return conn, 0, voiceWriteError{fmt.Errorf("start speaking: %w", err)}
	}
	onStart := onResume
	if start == 0 {
		onStart = func() {
			onResume()
			go p.notify(t, nowPlayingCard(t))
		}
	}
	updates := make(chan int, 1)
	p.mu.Lock()
	volume := p.volume
	p.volumeUpdates = updates
	p.mu.Unlock()
	played, err := playAudio(ctx, conn, result.URL, p.mediaProxy, start, result.duration(), p.opusBitrate, volume, updates, onStart)
	p.mu.Lock()
	p.volumeUpdates = nil
	p.mu.Unlock()
	return conn, played, err
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
		if len(p.queue) == 0 && !p.leaving {
			p.idleExpired = true
			p.leaveLocked()
		}
		p.mu.Unlock()
	}
}

func (p *player) watchVoice(ctx context.Context) {
	interval := time.Second
	if p.idleTimeout > 0 {
		interval = min(p.idleTimeout, interval)
	}
	ticker := time.NewTicker(interval)
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
			} else {
				if p.pauseAudio != nil {
					p.pauseAudio(errNoListeners)
				}
				if emptySince.IsZero() {
					emptySince = now
				} else if p.idleTimeout > 0 && now.Sub(emptySince) >= p.idleTimeout {
					p.idleExpired = true
					p.leaveLocked()
					p.mu.Unlock()
					return
				}
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
		p.cancel(nil)
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
			status := "Now playing "
			if p.waiting {
				status = "Waiting for a playback slot · "
			}
			lines = append(lines, status+escapeCardText(label(p.current.Title)))
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
		return joinMokkaVoiceMessage
	}
	if command == "skip" && p.cancel == nil {
		return "Wait for the next track to start."
	}
	if command == "skip" {
		if p.findingNext {
			return "Finding the next track…"
		}
		p.cancel(errTrackSkipped)
		message := "Skipped " + escapeCardText(label(p.current.Title)) + "."
		if p.canAutoplay(true) {
			message += " Finding the next track…"
		}
		return message
	}
	p.leaveLocked()
	return "Stopped playback and cleared the queue."
}

func (p *player) playbackCard(name, cardID string, user snowflake.ID) discord.MessageCreate {
	if !p.inVoice(user) {
		return commandCard(name, joinVoiceMessage, "")
	}
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
		return "No lyrics found. Try `" + p.prefix + "lyrics <song title> <artist>`.", ""
	default:
		return "Lyrics for " + escapeCardText(label(result.Artist+" · "+result.Title)), result.Text
	}
}
