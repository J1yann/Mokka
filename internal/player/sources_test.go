package player

import "testing"

func TestLyricTrackFromYouTubeTitle(t *testing.T) {
	title, artist := lyricTrack("juan karlos - Shot Puno (Official Music Video)", "JuanKarlosLabajoVEVO")
	if title != "Shot Puno" || artist != "juan karlos" {
		t.Fatalf("got %q by %q", title, artist)
	}
	title, artist = lyricTrack("One More Time (Official Video)", "Daft Punk - Topic")
	if title != "One More Time" || artist != "Daft Punk" {
		t.Fatalf("got %q by %q", title, artist)
	}
}
