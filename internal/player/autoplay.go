package player

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

const youtubeWebVersion = "2.20260120.01.00"
const autoplayBatchSize = 5

func (p *Player) nextTracks(ctx context.Context, current track, recent []track) ([]track, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	body := fmt.Sprintf(`{"context":{"client":{"clientName":"WEB","clientVersion":%q,"hl":"en","gl":"US"}},"videoId":%q}`, youtubeWebVersion, current.id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.youtube.com/youtubei/v1/next?prettyPrint=false", strings.NewReader(body))
	if err != nil {
		return nil, err
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
			return nil, err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube Up Next returned HTTP %d", resp.StatusCode)
	}
	return parseNextTracks(io.LimitReader(resp.Body, 4<<20), current.textChannel, recent)
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
func parseNextTracks(body io.Reader, channel snowflake.ID, recent []track) ([]track, error) {
	type item struct {
		CompactVideoRenderer struct {
			VideoID           string
			Title, LengthText nextText
			ShortBylineText   nextText
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
		return nil, err
	}
	picks := make([]track, 0, autoplayBatchSize)
	for _, entry := range data.Contents.TwoColumnWatchNextResults.SecondaryResults.SecondaryResults.Results {
		v := entry.CompactVideoRenderer
		artist := v.ShortBylineText.value()
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
		if !videoID.MatchString(id) || seconds <= 0 || seconds > 600 || strings.TrimSpace(title) == "" {
			continue
		}
		// Topic channels identify the artist. Other channels may simply be uploaders.
		if !strings.HasSuffix(artist, " - Topic") {
			artist = ""
		}
		candidate := track{id: id, media: media{Title: title, Artist: artist}, textChannel: channel}
		if skipAutoplayDuplicate(candidate, recent) || skipAutoplayDuplicate(candidate, picks) {
			continue
		}
		picks = append(picks, candidate)
		if len(picks) == autoplayBatchSize {
			break
		}
	}
	return picks, nil
}

func autoplaySongKey(t track) string {
	title, artist := lyricTrack(t.Title, t.Artist)
	title = strings.ToLower(strings.Join(strings.Fields(title), " "))
	artist = strings.ToLower(strings.Join(strings.Fields(artist), " "))
	if title == "" || artist == "" {
		return ""
	}
	return artist + "\x00" + title
}

func skipAutoplayDuplicate(candidate track, tracks []track) bool {
	key := autoplaySongKey(candidate)
	return slices.ContainsFunc(tracks, func(previous track) bool {
		reason := ""
		switch {
		case candidate.id == previous.id:
			reason = "video_id"
		case key != "" && key == autoplaySongKey(previous):
			reason = "song"
		default:
			return false
		}
		slog.Info("autoplay skipped duplicate", "video", candidate.id, "matched_video", previous.id, "reason", reason)
		return true
	})
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
