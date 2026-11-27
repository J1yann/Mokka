package player

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type media struct {
	URL             string
	Title           string
	Artist          string
	DurationSeconds float64 `json:"duration_seconds"`
}

func (m media) duration() time.Duration {
	seconds := m.DurationSeconds
	if u, err := url.Parse(m.URL); err == nil {
		if fromURL, err := strconv.ParseFloat(u.Query().Get("dur"), 64); err == nil && fromURL > 0 && !math.IsInf(fromURL, 0) {
			seconds = fromURL
		}
	}
	if seconds <= 0 || math.IsNaN(seconds) || seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
var videoTitleSuffix = regexp.MustCompile(`(?i)\s*[\[(](official(?: music)? video|official audio|music video|lyric video|lyrics|visualizer|audio|video)[\])]\s*$`)
var lyricClient = &http.Client{Timeout: 6 * time.Second}

func lyricTrack(title, uploader string) (string, string) {
	title = strings.TrimSpace(title)
	for {
		clean := videoTitleSuffix.ReplaceAllString(title, "")
		if clean == title {
			break
		}
		title = strings.TrimSpace(clean)
	}
	artist := strings.TrimSpace(strings.TrimSuffix(uploader, " - Topic"))
	for _, separator := range []string{" - ", " – ", " — "} {
		if by, song, ok := strings.Cut(title, separator); ok && by != "" && song != "" {
			return strings.TrimSpace(song), strings.TrimSpace(by)
		}
	}
	return title, artist
}

func youtubeID(input string) string {
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	var id string
	switch strings.ToLower(u.Hostname()) {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
		path := strings.Split(strings.Trim(u.Path, "/"), "/")
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else if len(path) == 2 && (path[0] == "shorts" || path[0] == "live" || path[0] == "embed") {
			id = path[1]
		}
	}
	if videoID.MatchString(id) {
		return id
	}
	return ""
}

func extract(ctx context.Context, binary, proxy, command, input string) (media, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result media
	cmd := exec.CommandContext(ctx, binary, command, input)
	if proxy != "" {
		cmd.Env = append(cmd.Environ(), "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy,
			"http_proxy="+proxy, "https_proxy="+proxy, "NO_PROXY=", "no_proxy=")
	}
	data, err := cmd.Output()
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(data, &result)
	return result, err
}

type lyrics struct {
	Title  string `json:"trackName"`
	Artist string `json:"artistName"`
	Text   string `json:"plainLyrics"`
}

func findLyrics(ctx context.Context, title, artist, query string) (lyrics, error) {
	params := url.Values{}
	if artist != "" && title != "" {
		params.Set("track_name", title)
		params.Set("artist_name", artist)
	} else {
		params.Set("q", query)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://lrclib.net/api/search?"+params.Encode(), nil)
	if err != nil {
		return lyrics{}, err
	}
	req.Header.Set("User-Agent", "Mokka/0.1")
	resp, err := lyricClient.Do(req)
	if err != nil {
		return lyrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return lyrics{}, errors.New("lyrics service unavailable")
	}
	var matches []lyrics
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(&matches); err != nil {
		return lyrics{}, err
	}
	for _, match := range matches {
		if strings.TrimSpace(match.Text) != "" {
			return match, nil
		}
	}
	return lyrics{}, nil
}
