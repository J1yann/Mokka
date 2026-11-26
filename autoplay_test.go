package main

import (
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
	next, err := parseNextTrack(strings.NewReader(body), 42, []string{"Os4WFOsJE_I"})
	if err != nil || next.id != "c1gtzwJydhM" || next.Title != "Alabang" || next.textChannel != 42 {
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
