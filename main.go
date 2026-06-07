package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

const feedCacheTTL = 30 * time.Minute
const appName = "podcast-player-cli"

var (
	blockTagPattern      = regexp.MustCompile(`(?i)</?(p|div|section|article|br|li|ul|ol|figure|figcaption|h[1-6]|tr|td|th|blockquote)[^>]*>`)
	htmlTagPattern       = regexp.MustCompile(`<[^>]+>`)
	figureTagPattern     = regexp.MustCompile(`(?is)<figure\b[^>]*>.*?</figure>`)
	lineBreakTagPattern  = regexp.MustCompile(`(?i)<br\s*/?>`)
	listItemTagPattern   = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	listWrapTagPattern   = regexp.MustCompile(`(?i)</?(ul|ol)\b[^>]*>`)
	blockCloseTagPattern = regexp.MustCompile(`(?i)</(p|div|section|article|blockquote|h[1-6]|tr|td|th)>`)
	blockOpenTagPattern  = regexp.MustCompile(`(?i)<(p|div|section|article|blockquote|h[1-6]|tr|td|th)\b[^>]*>`)
	whitespacePattern    = regexp.MustCompile(`[^\S\r\n]+`)
	newlineSpacePattern  = regexp.MustCompile(` *\n+ *`)
	repeatedNewlinePattn = regexp.MustCompile(`\n{3,}`)
)

type episode struct {
	Title           string
	Summary         string
	Description     string
	DescriptionHTML string
	PubDate         string
	PublishedAt     time.Time
	MediaURL        string
	MediaType       string
	GUID            string
	ImageURL        string
	LengthBytes     int64
}

type podcast struct {
	Title       string
	Description string
	Link        string
	ImageURL    string
	FeedURL     string
	Episodes    []episode
}

type audioPlayer struct {
	id                 int
	podcastTitle       string
	feedURL            string
	episode            episode
	cmd                *exec.Cmd
	stderr             *bytes.Buffer
	startedAt          time.Time
	elapsedBeforePause time.Duration
	paused             bool
	stoppingForPause   bool
}

type appPaths struct {
	RootDir           string
	CacheDir          string
	CoverCacheDir     string
	FeedCacheDir      string
	RenderCacheDir    string
	ConfigPath        string
	DurationCachePath string
	PlayStatePath     string
	SyncStatePath     string
}

type appConfig struct {
	FeedURLs []string   `json:"feed_urls"`
	ProxyURL string     `json:"proxy_url,omitempty"`
	Sync     syncConfig `json:"sync,omitempty"`
}

type playState struct {
	FeedURL      string    `json:"feed_url,omitempty"`
	EpisodeGUID  string    `json:"episode_guid,omitempty"`
	MediaURL     string    `json:"media_url,omitempty"`
	PodcastTitle string    `json:"podcast_title,omitempty"`
	EpisodeTitle string    `json:"episode_title,omitempty"`
	Position     string    `json:"position,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type rss struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string      `xml:"title"`
	Link        string      `xml:"link"`
	Description string      `xml:"description"`
	Image       rssImage    `xml:"image"`
	ItunesImage itunesImage `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	Items       []rssItem   `xml:"item"`
}

type rssImage struct {
	URL string `xml:"url"`
}

type itunesImage struct {
	Href string `xml:"href,attr"`
}

type rssItem struct {
	Title         string       `xml:"title"`
	Description   string       `xml:"description"`
	ItunesSummary string       `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	ItunesImage   itunesImage  `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
	PubDate       string       `xml:"pubDate"`
	GUID          string       `xml:"guid"`
	Enclosure     rssEnclosure `xml:"enclosure"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type feedImageFallbacks struct {
	channel string
	items   []string
}

func loadFeeds(client *http.Client, feedURLs []string, feedCacheDir string) ([]podcast, error) {
	var podcasts []podcast
	for _, feedURL := range feedURLs {
		p, err := loadFeed(client, feedURL, feedCacheDir)
		if err != nil {
			return nil, err
		}
		podcasts = append(podcasts, p)
	}
	return podcasts, nil
}

func loadFeed(client *http.Client, feedURL, feedCacheDir string) (podcast, error) {
	if cached, ok := readFreshFeedCache(feedCacheDir, feedURL); ok {
		return parseFeed(feedURL, cached)
	}

	req, err := http.NewRequest(http.MethodGet, feedURL, nil)
	if err != nil {
		return podcast{}, err
	}
	req.Header.Set("User-Agent", appName+"/0.1")

	resp, err := client.Do(req)
	if err != nil {
		return podcast{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if cached, ok := readAnyFeedCache(feedCacheDir, feedURL); ok {
			return parseFeed(feedURL, cached)
		}
		return podcast{}, fmt.Errorf("feed request failed: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return podcast{}, err
	}
	if err := writeFeedCache(feedCacheDir, feedURL, body); err != nil {
		return podcast{}, err
	}
	return parseFeed(feedURL, body)
}

func parseFeed(feedURL string, body []byte) (podcast, error) {
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return podcast{}, err
	}
	imageFallbacks, err := extractFeedImageFallbacks(body)
	if err != nil {
		return podcast{}, err
	}

	channel := feed.Channel
	p := podcast{
		Title:       strings.TrimSpace(channel.Title),
		Description: cleanText(channel.Description),
		Link:        strings.TrimSpace(channel.Link),
		ImageURL:    strings.TrimSpace(channel.Image.URL),
		FeedURL:     feedURL,
	}
	if p.ImageURL == "" {
		p.ImageURL = strings.TrimSpace(channel.ItunesImage.Href)
	}
	if p.ImageURL == "" {
		p.ImageURL = strings.TrimSpace(imageFallbacks.channel)
	}

	for i, item := range channel.Items {
		pubDate := strings.TrimSpace(item.PubDate)
		var publishedAt time.Time
		if pubDate != "" {
			if t, err := parsePubDate(pubDate); err == nil {
				publishedAt = t
				pubDate = t.Local().Format("01.02")
			}
		}
		summary := item.ItunesSummary
		if summary == "" {
			summary = item.Description
		}
		imageURL := strings.TrimSpace(item.ItunesImage.Href)
		if imageURL == "" && i < len(imageFallbacks.items) {
			imageURL = strings.TrimSpace(imageFallbacks.items[i])
		}
		p.Episodes = append(p.Episodes, episode{
			Title:           cleanText(item.Title),
			Summary:         cleanText(summary),
			Description:     cleanText(item.Description),
			DescriptionHTML: strings.TrimSpace(item.Description),
			PubDate:         pubDate,
			PublishedAt:     publishedAt,
			MediaURL:        strings.TrimSpace(item.Enclosure.URL),
			MediaType:       strings.TrimSpace(item.Enclosure.Type),
			GUID:            strings.TrimSpace(item.GUID),
			ImageURL:        imageURL,
			LengthBytes:     item.Enclosure.Length,
		})
	}

	return p, nil
}

func extractFeedImageFallbacks(body []byte) (feedImageFallbacks, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var fallbacks feedImageFallbacks
	var inChannel bool
	var inItem bool

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return fallbacks, nil
		}
		if err != nil {
			return feedImageFallbacks{}, err
		}

		switch tok := token.(type) {
		case xml.StartElement:
			switch tok.Name.Local {
			case "channel":
				inChannel = true
			case "item":
				inItem = true
			case "image":
				href := imageHrefAttr(tok.Attr)
				if href == "" {
					continue
				}
				if inItem {
					fallbacks.items = append(fallbacks.items, href)
					continue
				}
				if inChannel && fallbacks.channel == "" {
					fallbacks.channel = href
				}
			}
		case xml.EndElement:
			switch tok.Name.Local {
			case "item":
				inItem = false
			case "channel":
				inChannel = false
			}
		}
	}
}

func imageHrefAttr(attrs []xml.Attr) string {
	for _, attr := range attrs {
		if attr.Name.Local == "href" {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func parsePubDate(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported pubDate format: %s", value)
}

func resolveAppPaths() (appPaths, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return appPaths{}, err
	}

	rootDir := filepath.Join(homeDir, "."+appName)
	cacheDir := filepath.Join(rootDir, "cache")
	return appPaths{
		RootDir:           rootDir,
		CacheDir:          cacheDir,
		CoverCacheDir:     filepath.Join(cacheDir, "covers"),
		FeedCacheDir:      filepath.Join(cacheDir, "feeds"),
		RenderCacheDir:    filepath.Join(cacheDir, "renders"),
		ConfigPath:        filepath.Join(rootDir, "config.json"),
		DurationCachePath: filepath.Join(cacheDir, "durations.json"),
		PlayStatePath:     filepath.Join(rootDir, "play-state.json"),
		SyncStatePath:     filepath.Join(rootDir, "sync-state.json"),
	}, nil
}

func ensureAppPaths(paths appPaths) error {
	for _, dir := range []string{paths.RootDir, paths.CacheDir, paths.CoverCacheDir, paths.FeedCacheDir, paths.RenderCacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func loadOrCreateConfig(path string) (appConfig, error) {
	defaultConfig := appConfig{FeedURLs: []string{}}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := saveConfig(path, defaultConfig); err != nil {
			return appConfig{}, err
		}
		return defaultConfig, nil
	} else if err != nil {
		return appConfig{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return appConfig{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if err := saveConfig(path, defaultConfig); err != nil {
			return appConfig{}, err
		}
		return defaultConfig, nil
	}

	cfg, err := parseAppConfig(data)
	if err != nil {
		return appConfig{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func parseAppConfig(data []byte) (appConfig, error) {
	var cfg appConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return appConfig{}, err
	}
	if cfg.FeedURLs == nil {
		cfg.FeedURLs = []string{}
	}
	return cfg, nil
}

func saveConfig(path string, cfg appConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func loadDurationCache(path string) (map[string]time.Duration, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return map[string]time.Duration{}, nil
	} else if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]time.Duration{}, nil
	}

	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse duration cache %s: %w", path, err)
	}

	cache := make(map[string]time.Duration, len(raw))
	for mediaURL, value := range raw {
		duration, err := time.ParseDuration(value)
		if err != nil {
			continue
		}
		cache[mediaURL] = duration
	}
	return cache, nil
}

func saveDurationCache(path string, durations map[string]time.Duration) error {
	raw := make(map[string]string, len(durations))
	for mediaURL, duration := range durations {
		if mediaURL == "" || duration <= 0 {
			continue
		}
		raw[mediaURL] = duration.String()
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func loadPlayState(path string) (*playState, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	state, err := parsePlayState(data)
	if err != nil {
		return nil, fmt.Errorf("parse play state %s: %w", path, err)
	}
	return state, nil
}

func parsePlayState(data []byte) (*playState, error) {
	var state playState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if strings.TrimSpace(state.MediaURL) == "" {
		return nil, nil
	}
	return &state, nil
}

func savePlayState(path string, state playState) error {
	if strings.TrimSpace(state.MediaURL) == "" {
		return clearPlayState(path)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func clearPlayState(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func feedCachePath(cacheDir, feedURL string) string {
	return filepath.Join(cacheDir, cacheKey(feedURL)+".xml")
}

func coverCachePath(cacheDir, imageURL string) (string, error) {
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return "", err
	}

	ext := filepath.Ext(parsed.Path)
	if ext == "" {
		ext = ".img"
	}
	return filepath.Join(cacheDir, cacheKey(imageURL)+ext), nil
}

func renderCachePath(cacheDir, imagePath string, width, height int) (string, error) {
	info, err := os.Stat(imagePath)
	if err != nil {
		return "", err
	}

	key := fmt.Sprintf("%s:%d:%d:%d:%d", imagePath, info.Size(), info.ModTime().UnixNano(), width, height)
	return filepath.Join(cacheDir, cacheKey(key)+".ansi"), nil
}

func readFreshFeedCache(cacheDir, feedURL string) ([]byte, bool) {
	path := feedCachePath(cacheDir, feedURL)
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > feedCacheTTL {
		return nil, false
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func readAnyFeedCache(cacheDir, feedURL string) ([]byte, bool) {
	path := feedCachePath(cacheDir, feedURL)
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func writeFeedCache(cacheDir, feedURL string, body []byte) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(feedCachePath(cacheDir, feedURL), body, 0o644)
}

func readRenderCache(cacheDir, imagePath string, width, height int) (string, bool) {
	path, err := renderCachePath(cacheDir, imagePath, width, height)
	if err != nil {
		return "", false
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return strings.TrimRight(string(data), "\n"), true
}

func writeRenderCache(cacheDir, imagePath string, width, height int, ansi string) error {
	path, err := renderCachePath(cacheDir, imagePath, width, height)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(ansi+"\n"), 0o644)
}

func fetchCover(client *http.Client, imageURL, cacheDir string) (string, error) {
	if imageURL == "" {
		return "", errors.New("missing image URL")
	}

	path, err := coverCachePath(cacheDir, imageURL)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}

	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", appName+"/0.1")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cover request failed: %s", resp.Status)
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", err
	}

	return path, nil
}

func renderCover(path, cacheDir string, width, height int) (string, error) {
	if path == "" {
		return "", errors.New("missing image path")
	}
	if cached, ok := readRenderCache(cacheDir, path, width, height); ok {
		return cached, nil
	}
	if _, err := exec.LookPath("chafa"); err != nil {
		return "", errors.New("chafa is not installed; install it and try again")
	}

	args := []string{
		"--format", "symbols",
		"--size", fmt.Sprintf("%dx%d", maxInt(width, 10), maxInt(height, 10)),
		path,
	}

	cmd := exec.Command("chafa", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}

	ansi := strings.TrimRight(stdout.String(), "\n")
	if err := writeRenderCache(cacheDir, path, width, height, ansi); err != nil {
		return "", err
	}
	return ansi, nil
}

func configurePlaybackCommand(cmd *exec.Cmd, proxyURL string) {
	cmd.Stdout = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if proxyURL != "" {
		cmd.Env = append(os.Environ(),
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"ALL_PROXY="+proxyURL,
		)
	}
}

func startPlayback(id int, proxyURL, podcastTitle, feedURL string, ep episode, startOffset time.Duration) (*audioPlayer, error) {
	ffplayPath, err := exec.LookPath("ffplay")
	if err != nil {
		return nil, errors.New("ffplay is not installed; install ffmpeg to enable playback")
	}

	// Favor lower-latency playback so SIGSTOP leaves less queued audio behind.
	args := []string{
		"-nodisp",
		"-autoexit",
		"-loglevel", "error",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-vn",
	}
	if startOffset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.0f", startOffset.Seconds()))
	}
	args = append(args, ep.MediaURL)
	cmd := exec.Command(ffplayPath, args...)
	configurePlaybackCommand(cmd, proxyURL)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &audioPlayer{
		id:                 id,
		podcastTitle:       podcastTitle,
		feedURL:            feedURL,
		episode:            ep,
		cmd:                cmd,
		stderr:             &stderr,
		startedAt:          time.Now(),
		elapsedBeforePause: startOffset,
	}, nil
}

func stopPlayback(player *audioPlayer) {
	if player == nil || player.cmd == nil || player.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-player.cmd.Process.Pid, syscall.SIGKILL)
}

func pausePlayback(player *audioPlayer) error {
	if player == nil || player.paused {
		return nil
	}
	player.elapsedBeforePause = playerElapsed(player)
	player.paused = true
	player.stoppingForPause = true
	if player.cmd == nil || player.cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-player.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		player.paused = false
		player.stoppingForPause = false
		return err
	}
	return nil
}

func resumePlayback(player *audioPlayer, proxyURL string) error {
	if player == nil || !player.paused {
		return nil
	}
	ffplayPath, err := exec.LookPath("ffplay")
	if err != nil {
		return errors.New("ffplay is not installed; install ffmpeg to enable playback")
	}
	args := []string{
		"-nodisp",
		"-autoexit",
		"-loglevel", "error",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-vn",
	}
	if player.elapsedBeforePause > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.0f", player.elapsedBeforePause.Seconds()))
	}
	args = append(args, player.episode.MediaURL)
	cmd := exec.Command(ffplayPath, args...)
	configurePlaybackCommand(cmd, proxyURL)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	if err := cmd.Start(); err != nil {
		return err
	}
	player.cmd = cmd
	player.stderr = &stderr
	player.startedAt = time.Now()
	player.paused = false
	player.stoppingForPause = false
	return nil
}

func playerElapsed(player *audioPlayer) time.Duration {
	if player == nil {
		return 0
	}
	if player.paused {
		return player.elapsedBeforePause
	}
	return player.elapsedBeforePause + time.Since(player.startedAt)
}

func probeDuration(mediaURL string) (time.Duration, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		mediaURL,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return 0, err
	}
	value := strings.TrimSpace(stdout.String())
	if value == "" || value == "N/A" {
		return 0, errors.New("duration unavailable")
	}
	seconds, err := time.ParseDuration(value + "s")
	if err != nil {
		return 0, err
	}
	return seconds, nil
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = blockTagPattern.ReplaceAllString(s, "\n")
	s = htmlTagPattern.ReplaceAllString(s, "")
	s = whitespacePattern.ReplaceAllString(s, " ")
	s = newlineSpacePattern.ReplaceAllString(s, "\n")
	s = repeatedNewlinePattn.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func formatDescriptionText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	s = html.UnescapeString(s)
	s = figureTagPattern.ReplaceAllString(s, "\n")
	s = lineBreakTagPattern.ReplaceAllString(s, "\n")
	s = listItemTagPattern.ReplaceAllString(s, "• ")
	s = strings.ReplaceAll(s, "</li>", "\n")
	s = listWrapTagPattern.ReplaceAllString(s, "\n")
	s = blockCloseTagPattern.ReplaceAllString(s, "\n\n")
	s = blockOpenTagPattern.ReplaceAllString(s, "")
	s = htmlTagPattern.ReplaceAllString(s, "")
	s = whitespacePattern.ReplaceAllString(s, " ")
	s = repeatedNewlinePattn.ReplaceAllString(s, "\n\n")

	lines := strings.Split(s, "\n")
	normalized := make([]string, 0, len(lines))
	lastBlank := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !lastBlank {
				normalized = append(normalized, "")
			}
			lastBlank = true
			continue
		}
		normalized = append(normalized, line)
		lastBlank = false
	}

	return strings.TrimSpace(strings.Join(normalized, "\n"))
}

func newHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, nil
}

func main() {
	proxyURL := flag.String("proxy", "", "optional HTTP proxy URL for feed and cover requests")
	flag.Parse()

	paths, err := resolveAppPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "app setup error: %s\n", err)
		os.Exit(1)
	}
	if err := ensureAppPaths(paths); err != nil {
		fmt.Fprintf(os.Stderr, "app setup error: %s\n", err)
		os.Exit(1)
	}

	cfg, err := loadOrCreateConfig(paths.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %s\n", err)
		os.Exit(1)
	}

	resolvedProxyURL := strings.TrimSpace(*proxyURL)
	if resolvedProxyURL == "" {
		resolvedProxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	if args := flag.Args(); len(args) > 0 && args[0] == "sync" {
		if err := runSyncCommand(paths, cfg, resolvedProxyURL, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "sync error: %s\n", err)
			os.Exit(1)
		}
		return
	}

	feedURLs := flag.Args()
	if len(feedURLs) == 0 {
		feedURLs = cfg.FeedURLs
	}

	durationCache, err := loadDurationCache(paths.DurationCachePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "duration cache error: %s\n", err)
		os.Exit(1)
	}

	savedPlayState, err := loadPlayState(paths.PlayStatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "play state error: %s\n", err)
		os.Exit(1)
	}

	client, err := newHTTPClient(resolvedProxyURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid proxy: %s\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(newModel(
		feedURLs,
		resolvedProxyURL,
		paths.CoverCacheDir,
		paths.FeedCacheDir,
		paths.RenderCacheDir,
		paths.DurationCachePath,
		paths.PlayStatePath,
		client,
		durationCache,
		savedPlayState,
	))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "player error: %s\n", err)
		os.Exit(1)
	}
}
