package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

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
