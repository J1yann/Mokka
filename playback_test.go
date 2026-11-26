package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
	"github.com/disgoorg/snowflake/v2"
)

func TestSendOpusContinuedPacket(t *testing.T) {
	var stream bytes.Buffer
	writePage := func(segments []byte, payload []byte) {
		header := [27]byte{'O', 'g', 'g', 'S'}
		header[26] = byte(len(segments))
		stream.Write(header[:])
		stream.Write(segments)
		stream.Write(payload)
	}
	writePage([]byte{8, 8}, []byte("OpusHeadOpusTags"))
	frame := bytes.Repeat([]byte{0x6c}, 256)
	writePage([]byte{255}, frame[:255])
	writePage([]byte{1}, frame[255:])

	var received bytes.Buffer
	if err := sendOpus(context.Background(), &stream, &received); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received.Bytes(), frame) {
		t.Fatalf("received %d bytes, expected %d", received.Len(), len(frame))
	}
}

type testVoiceConn struct {
	voice.Conn
	udp voice.UDPConn
}

func (c testVoiceConn) DAVE() godave.Session { return nil }
func (c testVoiceConn) UDP() voice.UDPConn   { return c.udp }

type testVoiceUDP struct {
	voice.UDPConn
	err error
}

func (u *testVoiceUDP) Write(frame []byte) (int, error) {
	if u.err != nil {
		return 0, u.err
	}
	return len(frame), nil
}

func TestVoiceDisconnectKeepsLastSentPosition(t *testing.T) {
	udp := &testVoiceUDP{}
	writer := daveWriter{conn: testVoiceConn{udp: udp}}
	if _, err := writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	udp.err = net.ErrClosed
	var voiceErr voiceWriteError
	if _, err := writer.Write([]byte{2}); !errors.As(err, &voiceErr) {
		t.Fatalf("UDP disconnect was not classified as a voice error: %v", err)
	}
	if writer.sent != opusFrameDuration {
		t.Fatalf("sent position is %s, want %s", writer.sent, opusFrameDuration)
	}
}

func TestClosedVoiceSessionCannotDisconnectItsReplacement(t *testing.T) {
	var updates int
	conn := newManagedVoiceConn(42, 43, func(context.Context, snowflake.ID, *snowflake.ID, bool, bool) error {
		updates++
		return nil
	}, func() {}).(*managedVoiceConn)
	t.Cleanup(func() { conn.Close(context.Background()) })
	conn.Close(context.Background())
	if err := conn.updateWhileOpen(context.Background(), 42, nil, false, false); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("old session could still update voice: %v", err)
	}
	if updates != 1 {
		t.Fatalf("sent %d voice updates, want one explicit disconnect", updates)
	}
}

func TestCheckAudioDuration(t *testing.T) {
	expected := 10 * time.Minute
	if err := checkAudioDuration(expected-42*time.Second, expected); err == nil {
		t.Fatal("accepted a cut-off track as finished")
	}
	if err := checkAudioDuration(expected-time.Second, expected); err != nil {
		t.Fatalf("allowed rounding difference was rejected: %v", err)
	}
}

func TestFFmpegDiagnosticsOmitMediaURL(t *testing.T) {
	var diagnostics ffmpegDiagnostics
	_, _ = diagnostics.Write([]byte("https://example.invalid/?sig=secret: Stream ends prematurely"))
	if category := diagnostics.category(); category != "FFmpeg input ended early" {
		t.Fatalf("unexpected diagnostic category: %q", category)
	}
}

func TestLivePlaybackVolume(t *testing.T) {
	output, err := exec.Command("ffmpeg", "-hide_banner", "-h", "filter=azmq").Output()
	if err != nil || !strings.HasPrefix(string(output), "Filter azmq") {
		t.Skip("FFmpeg with azmq is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	socketPath := t.TempDir() + "/volume.sock"
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-re", "-f", "lavfi",
		"-i", "sine=sample_rate=48000", "-t", "1.5", "-af", volumeFilter(socketPath, 30), "-f", "s16le", "pipe:1")
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	before := make([]byte, 4800*2)
	if _, err := io.ReadFull(pipe, before); err != nil {
		t.Fatal(err)
	}
	change := make(chan error, 1)
	go func() { change <- setPlaybackVolume(ctx, socketPath, 70, nil) }()
	rest, err := io.ReadAll(pipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-change; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("FFmpeg: %v: %s", err, diagnostics.String())
	}
	if len(before)+len(rest) != 72000*2 {
		t.Fatalf("expected 72000 consecutive samples, got %d", (len(before)+len(rest))/2)
	}
	peak := func(audio []byte) int {
		var value int
		for len(audio) >= 2 {
			value = max(value, int(int16(binary.LittleEndian.Uint16(audio))))
			audio = audio[2:]
		}
		return value
	}
	if low, high := peak(before), peak(rest[len(rest)-len(before):]); low < 1200 || low > 1250 || high < 2800 || high > 2900 {
		t.Fatalf("unexpected peak levels before and after change: %d, %d", low, high)
	}
}

type recoveryGateway struct {
	gateway.Gateway
	status atomic.Int32
}

func (g *recoveryGateway) Status() gateway.Status {
	return gateway.Status(g.status.Load())
}

func TestWaitForGatewayRecovery(t *testing.T) {
	shard := &recoveryGateway{}
	shard.status.Store(int32(gateway.StatusConnecting))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- waitForGateway(ctx, shard) }()
	select {
	case err := <-result:
		t.Fatalf("recovery continued before the gateway was ready: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	shard.status.Store(int32(gateway.StatusReady))
	if err := <-result; err != nil {
		t.Fatalf("ready gateway did not release recovery: %v", err)
	}

	cancelled, stop := context.WithCancelCause(context.Background())
	shard.status.Store(int32(gateway.StatusConnecting))
	go func() { result <- waitForGateway(cancelled, shard) }()
	stop(errTrackSkipped)
	if err := <-result; !errors.Is(err, errTrackSkipped) {
		t.Fatalf("skip did not cancel recovery: %v", err)
	}
}
