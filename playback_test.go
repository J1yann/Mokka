package main

import (
	"bytes"
	"context"
	"testing"
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
