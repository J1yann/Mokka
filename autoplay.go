package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

const youtubeWebVersion = "2.20260120.01.00"

func (p *player) nextTrack(ctx context.Context, current track, recent []string) (track, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	body := fmt.Sprintf(`{"context":{"client":{"clientName":"WEB","clientVersion":%q,"hl":"en","gl":"US"}},"videoId":%q}`, youtubeWebVersion, current.id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.youtube.com/youtubei/v1/next?prettyPrint=false", strings.NewReader(body))
	if err != nil {
		return track{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-YouTube-Client-Name", "1")
	req.Header.Set("X-YouTube-Client-Version", youtubeWebVersion)
	req.Header.Set("Origin", "https://www.youtube.com")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := http.DefaultClient
	if p.mediaProxy != "" {
		proxy, err := url.Parse(p.mediaProxy)
		if err != nil {
			return track{}, err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport}
	}
	resp, err := client.Do(req)
	if err != nil {
		return track{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return track{}, fmt.Errorf("YouTube Up Next returned HTTP %d", resp.StatusCode)
	}
	return parseNextTrack(io.LimitReader(resp.Body, 4<<20), current.textChannel, recent)
}

type nextText struct {
	Content    string
	SimpleText string
	Runs       []struct{ Text string }
}

func (t nextText) value() string {
	if t.Content != "" {
		return t.Content
	}
	if t.SimpleText != "" {
		return t.SimpleText
	}
	var text strings.Builder
	for _, run := range t.Runs {
		text.WriteString(run.Text)
	}
	return text.String()
}

// shortcut: use the desktop sidebar's first page, add renderers or pagination if it stops yielding tracks.
func parseNextTrack(body io.Reader, channel snowflake.ID, recent []string) (track, error) {
	type item struct {
		CompactVideoRenderer struct {
			VideoID           string
			Title, LengthText nextText
		}
		LockupViewModel struct {
			ContentType, ContentID string
			Metadata               struct {
				LockupMetadataViewModel struct{ Title nextText }
			}
			ContentImage struct {
				ThumbnailViewModel struct {
					Overlays []struct {
						ThumbnailBottomOverlayViewModel struct {
							Badges []struct{ ThumbnailBadgeViewModel struct{ Text string } }
						}
					}
				}
			}
		}
	}
	var data struct {
		Contents struct {
			TwoColumnWatchNextResults struct {
				SecondaryResults struct {
					SecondaryResults struct{ Results []item }
				}
			}
		}
	}
	if err := json.NewDecoder(body).Decode(&data); err != nil {
		return track{}, err
	}
	for _, entry := range data.Contents.TwoColumnWatchNextResults.SecondaryResults.SecondaryResults.Results {
		v := entry.CompactVideoRenderer
		id, title, seconds := v.VideoID, v.Title.value(), nextDuration(v.LengthText.value())
		lockup := entry.LockupViewModel
		if lockup.ContentType == "LOCKUP_CONTENT_TYPE_VIDEO" {
			id, title = lockup.ContentID, lockup.Metadata.LockupMetadataViewModel.Title.value()
			for _, overlay := range lockup.ContentImage.ThumbnailViewModel.Overlays {
				for _, badge := range overlay.ThumbnailBottomOverlayViewModel.Badges {
					if duration := nextDuration(badge.ThumbnailBadgeViewModel.Text); duration > 0 {
						seconds = duration
					}
				}
			}
		}
		if !videoID.MatchString(id) || slices.Contains(recent, id) || seconds <= 0 || seconds > 600 || strings.TrimSpace(title) == "" {
			continue
		}
		return track{id: id, media: media{Title: title}, textChannel: channel}, nil
	}
	return track{}, nil
}

func nextDuration(raw string) int {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	seconds := 0
	for i, part := range parts {
		if len(part) == 0 || len(part) > 3 || strings.Trim(part, "0123456789") != "" {
			return 0
		}
		n, err := strconv.Atoi(part)
		if err != nil || (i > 0 && (len(part) != 2 || n >= 60)) {
			return 0
		}
		seconds = seconds*60 + n
	}
	return seconds
}
