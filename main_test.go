package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseFeedUsesItunesImageFallbackForNamespacedCover(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" version="2.0">
  <channel>
    <title>Sample Feed</title>
    <description>Sample Description</description>
    <itunes:image href="https://cdn.example.com/show-cover.png"/>
    <item>
      <title>Episode 1</title>
      <description>Episode Summary</description>
      <enclosure url="https://cdn.example.com/episode-1.mp3" type="audio/mpeg" length="123"/>
      <guid>ep-1</guid>
    </item>
  </channel>
</rss>`)

	p, err := parseFeed("https://example.com/feed.xml", body)
	if err != nil {
		t.Fatalf("parse feed: %v", err)
	}
	if p.ImageURL != "https://cdn.example.com/show-cover.png" {
		t.Fatalf("expected itunes image fallback to be used, got %q", p.ImageURL)
	}
}

func TestParseFeedCapturesEpisodeImageAndDescription(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" version="2.0">
  <channel>
    <title>Sample Feed</title>
    <description>Sample Description</description>
    <itunes:image href="https://cdn.example.com/show-cover.png"/>
    <item>
      <title>Episode 1</title>
      <description>Full episode description</description>
      <itunes:summary>Short summary</itunes:summary>
      <itunes:image href="https://cdn.example.com/episode-cover.png"/>
      <enclosure url="https://cdn.example.com/episode-1.mp3" type="audio/mpeg" length="123"/>
      <guid>ep-1</guid>
    </item>
  </channel>
</rss>`)

	p, err := parseFeed("https://example.com/feed.xml", body)
	if err != nil {
		t.Fatalf("parse feed: %v", err)
	}
	if len(p.Episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(p.Episodes))
	}
	if p.Episodes[0].ImageURL != "https://cdn.example.com/episode-cover.png" {
		t.Fatalf("expected episode image to be parsed, got %q", p.Episodes[0].ImageURL)
	}
	if p.Episodes[0].Description != "Full episode description" {
		t.Fatalf("expected full description to be preserved, got %q", p.Episodes[0].Description)
	}
	if p.Episodes[0].DescriptionHTML != "Full episode description" {
		t.Fatalf("expected raw description HTML to be preserved, got %q", p.Episodes[0].DescriptionHTML)
	}
	if p.Episodes[0].Summary != "Short summary" {
		t.Fatalf("expected summary to prefer itunes summary, got %q", p.Episodes[0].Summary)
	}
}

func TestCleanTextRemovesInlineHTMLWithoutAddingCJKSpaces(t *testing.T) {
	raw := `<p>杭州邮电路上的<a href="https://example.com">别饮居</a>，是一家开了三十多年的杭帮菜馆。</p>`

	got := cleanText(raw)
	want := `杭州邮电路上的别饮居，是一家开了三十多年的杭帮菜馆。`
	if got != want {
		t.Fatalf("expected cleaned text %q, got %q", want, got)
	}
}

func TestFormatDescriptionTextPreservesSimpleStructure(t *testing.T) {
	raw := `<p>第一段<br>第二行</p><ul><li>其一</li><li><a href="https://example.com">其二</a></li></ul><figure><img src="https://example.com/a.png"></figure><p>结尾</p>`

	got := formatDescriptionText(raw)
	want := "第一段\n第二行\n\n• 其一\n• 其二\n\n结尾"
	if got != want {
		t.Fatalf("expected formatted description %q, got %q", want, got)
	}
}

func TestWrapTextBreaksLongCJKText(t *testing.T) {
	got := wrapText("杭州邮电路上的别饮居，是一家开了三十多年的杭帮菜馆。", 12)
	want := []string{
		"杭州邮电路上",
		"的别饮居，是",
		"一家开了三十",
		"多年的杭帮菜",
		"馆。",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected wrapped lines %v, got %v", want, got)
	}
}

func TestWrapTextKeepsMixedCJKAndLatinTogetherWhenWidthAllows(t *testing.T) {
	got := wrapText("和 Photo Reason 串台的缘起，是我们后台的共同听友真多呀！", 18)
	if len(got) < 2 {
		t.Fatalf("expected multiple wrapped lines, got %v", got)
	}
	if got[0] != "和 Photo Reason 串" {
		t.Fatalf("expected first line to keep mixed text together, got %q", got[0])
	}
	if got[1] != "台的缘起，是我们后" {
		t.Fatalf("expected second line to continue immediately after the first, got %q", got[1])
	}
}

func TestBuildSyncGistFilesUsesNormalizedPlayState(t *testing.T) {
	dir := t.TempDir()
	playStatePath := filepath.Join(dir, "play-state.json")
	if err := os.WriteFile(playStatePath, []byte("{\n  \"media_url\": \"https://example.com/ep.mp3\",\n  \"position\": \"12s\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write play state: %v", err)
	}

	files, err := buildSyncGistFiles(appConfig{
		FeedURLs: []string{"https://example.com/feed.xml"},
		Sync:     syncConfig{GistID: "gist123"},
	}, playStatePath)
	if err != nil {
		t.Fatalf("build sync files: %v", err)
	}

	if files[syncConfigFilename].Content == "" {
		t.Fatal("expected config content to be present")
	}
	if files[syncPlayStateFilename].Content == emptySyncPlayStateJSON {
		t.Fatal("expected non-empty play state content")
	}
	if files[syncManifestFilename].Content == "" {
		t.Fatal("expected manifest content to be present")
	}
}

func TestApplyPulledFilesBootstrapsConfigAndClearsEmptyPlayState(t *testing.T) {
	dir := t.TempDir()
	paths := appPaths{
		ConfigPath:    filepath.Join(dir, "config.json"),
		PlayStatePath: filepath.Join(dir, "play-state.json"),
	}
	if err := os.WriteFile(paths.PlayStatePath, []byte(`{"media_url":"https://old.example/ep.mp3","position":"1m"}`), 0o644); err != nil {
		t.Fatalf("seed local play state: %v", err)
	}

	configContent := "{\n  \"feed_urls\": [\"https://example.com/feed.xml\"]\n}\n"
	manifestContent := "{\n  \"schema_version\": 1,\n  \"app_id\": \"podcast-player-cli\",\n  \"last_writer_at\": \"2026-06-07T00:00:00Z\"\n}\n"
	cfg, err := applyPulledFiles(paths, "gist123", gistResponse{
		ID: "gist123",
		Files: map[string]gistFile{
			syncConfigFilename:    {Filename: syncConfigFilename, Content: configContent},
			syncManifestFilename:  {Filename: syncManifestFilename, Content: manifestContent},
			syncPlayStateFilename: {Filename: syncPlayStateFilename, Content: "{}\n"},
		},
	})
	if err != nil {
		t.Fatalf("apply pulled files: %v", err)
	}
	if cfg.Sync.GistID != "gist123" {
		t.Fatalf("expected gist id to be set, got %q", cfg.Sync.GistID)
	}
	if _, err := os.Stat(paths.PlayStatePath); !os.IsNotExist(err) {
		t.Fatalf("expected play state to be removed, stat err=%v", err)
	}
}

func TestPullAndPushWarnings(t *testing.T) {
	dir := t.TempDir()
	playStatePath := filepath.Join(dir, "play-state.json")
	cfg := appConfig{FeedURLs: []string{"https://example.com/feed.xml"}}
	if err := os.WriteFile(playStatePath, []byte(`{"media_url":"https://example.com/ep.mp3","position":"10s"}`), 0o644); err != nil {
		t.Fatalf("write play state: %v", err)
	}

	cfgHash, err := configHash(cfg)
	if err != nil {
		t.Fatalf("config hash: %v", err)
	}
	playHash, err := playStateHashFromFile(playStatePath)
	if err != nil {
		t.Fatalf("play hash: %v", err)
	}

	meta := syncState{
		LastConfigHash:    cfgHash,
		LastPlayStateHash: playHash,
		LastSyncAt:        time.Now(),
	}
	if pullShouldWarn(meta, cfg, playStatePath) {
		t.Fatal("did not expect pull warning for unchanged files")
	}

	if err := os.WriteFile(playStatePath, []byte(`{"media_url":"https://example.com/ep.mp3","position":"30s"}`), 0o644); err != nil {
		t.Fatalf("rewrite play state: %v", err)
	}
	if !pullShouldWarn(meta, cfg, playStatePath) {
		t.Fatal("expected pull warning after local change")
	}

	meta.LastRemoteUpdatedAt = "2026-06-07T00:00:00Z"
	if !pushShouldWarn(meta, time.Date(2026, 6, 7, 0, 1, 0, 0, time.UTC)) {
		t.Fatal("expected push warning when remote is newer")
	}
}

func TestLoadOrCreateConfigCreatesEmptyFeedListWhenMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg, err := loadOrCreateConfig(configPath)
	if err != nil {
		t.Fatalf("load or create config: %v", err)
	}
	if len(cfg.FeedURLs) != 0 {
		t.Fatalf("expected no default feeds, got %v", cfg.FeedURLs)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	want := "{\n  \"feed_urls\": [],\n  \"sync\": {}\n}\n"
	if string(data) != want {
		t.Fatalf("expected config contents %q, got %q", want, string(data))
	}
}

func TestParseAppConfigLeavesEmptyFeedListEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{\n  \"feed_urls\": []\n}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadOrCreateConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.FeedURLs) != 0 {
		t.Fatalf("expected empty feed list, got %v", cfg.FeedURLs)
	}
}
