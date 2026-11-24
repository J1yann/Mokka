package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/voice"
)

const opusFrameDuration = 20 * time.Millisecond

type voiceWriteError struct{ error }

type daveWriter struct {
	ctx     context.Context
	conn    voice.Conn
	onStart func()
	sent    time.Duration
}

func (w *daveWriter) Write(frame []byte) (int, error) {
	if state, ok := w.conn.DAVE().(interface{ ShouldHoldFrames() bool }); ok && state.ShouldHoldFrames() {
		ctx, cancel := context.WithTimeout(w.ctx, 12*time.Second)
		defer cancel()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for state.ShouldHoldFrames() {
			select {
			case <-ctx.Done():
				return 0, voiceWriteError{fmt.Errorf("waiting for Discord voice encryption: %w", ctx.Err())}
			case <-ticker.C:
			}
		}
	}
	n, err := w.conn.UDP().Write(frame)
	if err != nil {
		return n, voiceWriteError{err}
	}
	if n != len(frame) {
		return n, voiceWriteError{io.ErrShortWrite}
	}
	w.sent += opusFrameDuration
	if w.onStart != nil {
		w.onStart()
		w.onStart = nil
	}
	return n, err
}

// FFmpeg writes Ogg pages. A lacing value below 255 marks the end of an Opus packet.
func sendOpus(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	var header [27]byte
	var segments [255]byte
	var packet [65536]byte
	var size, headers int
	var next time.Time
	for {
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			if err == io.EOF && headers == 2 && size == 0 && !next.IsZero() {
				return nil
			}
			return errors.New("incomplete Opus stream")
		}
		if string(header[:4]) != "OggS" || header[4] != 0 {
			return errors.New("invalid Ogg page")
		}
		lacing := segments[:header[26]]
		if _, err := io.ReadFull(reader, lacing); err != nil {
			return err
		}
		for _, length := range lacing {
			end := size + int(length)
			if end > len(packet) {
				return errors.New("Opus packet too large")
			}
			if _, err := io.ReadFull(reader, packet[size:end]); err != nil {
				return err
			}
			size = end
			if length == 255 {
				continue
			}
			frame := packet[:size]
			size = 0
			if headers < 2 {
				expected := [2]string{"OpusHead", "OpusTags"}
				if len(frame) < 8 || string(frame[:8]) != expected[headers] {
					return errors.New("invalid Opus header")
				}
				headers++
				continue
			}
			if delay := time.Until(next); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := output.Write(frame); err != nil {
				return err
			}
			next = time.Now().Add(opusFrameDuration)
		}
	}
}

func playAudio(ctx context.Context, conn voice.Conn, stream, proxy string, start, expected time.Duration, bitrate int, onStart func()) (time.Duration, error) {
	u, err := url.Parse(stream)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".googlevideo.com") {
		return 0, errors.New("unexpected stream URL")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"-nostdin", "-loglevel", "error", "-xerror", "-threads", "1", "-rw_timeout", "10000000",
		"-reconnect", "1", "-reconnect_on_network_error", "1", "-reconnect_delay_max", "2"}
	protocols := "https,tls,tcp"
	if proxy != "" {
		args = append(args, "-http_proxy", proxy)
		protocols = "https,http,httpproxy,tls,tcp"
	}
	args = append(args, "-protocol_whitelist", protocols,
		"-analyzeduration", "2000000", "-probesize", "1000000")
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start.Seconds(), 'f', 3, 64))
	}
	args = append(args,
		"-i", stream, "-map", "0:a:0", "-vn", "-ac", "2", "-ar", "48000",
		"-c:a", "libopus", "-threads", "1", "-frame_duration", "20", "-b:a", strconv.Itoa(bitrate)+"k",
		"-page_duration", "20000", "-f", "ogg", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	// FFmpeg errors can include the full signed media URL.
	var diagnostics ffmpegDiagnostics
	cmd.Stderr = &diagnostics
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	writer := &daveWriter{ctx: ctx, conn: conn, onStart: onStart}
	err = sendOpus(ctx, pipe, writer)
	if err != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	var voiceErr voiceWriteError
	if errors.As(err, &voiceErr) {
		return writer.sent, err
	}
	if err != nil || waitErr != nil {
		if category := diagnostics.category(); category != "" {
			return writer.sent, errors.New(category)
		}
		if err != nil {
			return writer.sent, err
		}
		return writer.sent, fmt.Errorf("FFmpeg: %w", waitErr)
	}
	return writer.sent, checkAudioDuration(start+writer.sent, expected)
}

func checkAudioDuration(sent, expected time.Duration) error {
	if expected > 0 && expected-sent > 2*time.Second {
		return fmt.Errorf("audio ended early: sent %s of %s", sent.Round(time.Millisecond), expected.Round(time.Millisecond))
	}
	return nil
}

type ffmpegDiagnostics struct{ output []byte }

func (d *ffmpegDiagnostics) Write(data []byte) (int, error) {
	d.output = append(d.output, data[:min(len(data), 4096-len(d.output))]...)
	return len(data), nil
}

func (d ffmpegDiagnostics) category() string {
	text := strings.ToLower(string(d.output))
	switch {
	case strings.Contains(text, "prematurely"), strings.Contains(text, "partial file"):
		return "FFmpeg input ended early"
	case strings.Contains(text, "timed out"):
		return "FFmpeg input timed out"
	case strings.Contains(text, "http error"), strings.Contains(text, "server returned"):
		return "FFmpeg media server rejected the request"
	case len(text) != 0:
		return "FFmpeg reported an audio input error"
	default:
		return ""
	}
}
