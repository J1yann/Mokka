package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseNextTrack(t *testing.T) {
	body := `{"contents":{"twoColumnWatchNextResults":{"secondaryResults":{"secondaryResults":{"results":[
		{"compactVideoRenderer":{"videoId":"Os4WFOsJE_I","title":{"simpleText":"Already played"},"lengthText":{"simpleText":"3:00"}}},
		{"compactVideoRenderer":{"videoId":"11ddlw70Bwc","title":{"simpleText":"Long mix"},"lengthText":{"simpleText":"1:00:00"}}},
		{"compactVideoRenderer":{"videoId":"FGBhQbmPwH8","title":{"simpleText":"Unknown length"}}},
		{"lockupViewModel":{"contentType":"LOCKUP_CONTENT_TYPE_VIDEO","contentId":"c1gtzwJydhM",
			"metadata":{"lockupMetadataViewModel":{"title":{"content":"Alabang"}}},
			"contentImage":{"thumbnailViewModel":{"overlays":[{"thumbnailBottomOverlayViewModel":{"badges":[{"thumbnailBadgeViewModel":{"text":"4:36"}}]}}]}}}},
		{"compactVideoRenderer":{"videoId":"XymqZCAUS1c","title":{"simpleText":"Later result"},"lengthText":{"simpleText":"3:39"}}}
	]}}}}}`
	next, err := parseNextTracks(strings.NewReader(body), 42, []track{{id: "Os4WFOsJE_I"}})
	if err != nil || len(next) != 2 || next[0].id != "c1gtzwJydhM" || next[0].Title != "Alabang" || next[0].textChannel != 42 || next[1].id != "XymqZCAUS1c" {
		t.Fatalf("recommendation: %+v, error: %v", next, err)
	}
}

func TestNextDuration(t *testing.T) {
	for input, want := range map[string]int{"10:00": 600, "10:01": 601, "1:16:01": 4561, "LIVE": 0, "1:60": 0, "": 0} {
		if got := nextDuration(input); got != want {
			t.Errorf("nextDuration(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestAutoplayBatchLimitAndDuplicates(t *testing.T) {
	var entries []string
	for _, id := range []string{"00000000001", "00000000001", "00000000002", "00000000003", "00000000004", "00000000005", "00000000006"} {
		entries = append(entries, fmt.Sprintf(`{"compactVideoRenderer":{"videoId":%q,"title":{"simpleText":"Song"},"lengthText":{"simpleText":"3:00"}}}`, id))
	}
	body := `{"contents":{"twoColumnWatchNextResults":{"secondaryResults":{"secondaryResults":{"results":[` + strings.Join(entries, ",") + `]}}}}}`
	picks, err := parseNextTracks(strings.NewReader(body), 42, nil)
	if err != nil || len(picks) != 5 {
		t.Fatalf("recommendations: %+v, error: %v", picks, err)
	}
	for i, pick := range picks {
		if want := fmt.Sprintf("%011d", i+1); pick.id != want {
			t.Errorf("pick %d: got %s, want %s", i, pick.id, want)
		}
	}
}

func TestAutoplayDeduplicatesSongs(t *testing.T) {
	var entries []string
	for i, title := range []string{
		"The Chainsmokers - Closer (Official Music Video)",
		"Closer (Lyrics)",
		"Ne-Yo - Closer (Official Video)",
		"The Chainsmokers - Closer (Live)",
		"The Chainsmokers - Closer (Live) [Official Video]",
	} {
		entries = append(entries, fmt.Sprintf(`{"compactVideoRenderer":{"videoId":"%011d","title":{"simpleText":%q},"shortBylineText":{"simpleText":"Unrelated uploader"},"lengthText":{"simpleText":"3:00"}}}`, i+1, title))
	}
	body := `{"contents":{"twoColumnWatchNextResults":{"secondaryResults":{"secondaryResults":{"results":[` + strings.Join(entries, ",") + `]}}}}}`
	recent := []track{{id: "abcdefghijk", media: media{Title: "Closer (Lyrics)", Artist: "The Chainsmokers - Topic"}}}
	picks, err := parseNextTracks(strings.NewReader(body), 42, recent)
	if err != nil || len(picks) != 3 || picks[0].id != "00000000002" || picks[1].id != "00000000003" || picks[2].id != "00000000004" {
		t.Fatalf("recommendations: %+v, error: %v", picks, err)
	}
}
