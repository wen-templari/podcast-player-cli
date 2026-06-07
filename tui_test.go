package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestCoverLinesKeepsPreviousCoverWhileLoading(t *testing.T) {
	m := model{
		loadingCover: true,
		coverANSI:    "abcd\nwxyz",
	}

	lines := coverLines(m, 80)
	if got, want := len(lines), 2; got != want {
		t.Fatalf("expected loading state to keep previous cover height %d, got %d", want, got)
	}
	if got, want := maxRenderedLineWidth(lines), 4; got != want {
		t.Fatalf("expected loading state to keep previous cover width %d, got %d", want, got)
	}
}

func TestCoverLinesUsesSizedPlaceholderWhileLoading(t *testing.T) {
	m := model{
		width:        120,
		height:       36,
		loadingCover: true,
	}

	lines := coverLines(m, 80)
	if got, want := len(lines), m.targetCoverHeight(); got != want {
		t.Fatalf("expected loading placeholder height %d, got %d", want, got)
	}
	if got, want := maxRenderedLineWidth(lines), m.targetCoverWidth(); got != want {
		t.Fatalf("expected loading placeholder width %d, got %d", want, got)
	}
}

func TestCoverLinesUsesSizedPlaceholderWhenMissing(t *testing.T) {
	m := model{
		width:  100,
		height: 30,
	}

	lines := coverLines(m, 72)
	if got, want := len(lines), m.targetCoverHeight(); got != want {
		t.Fatalf("expected empty placeholder height %d, got %d", want, got)
	}
	if got, want := maxRenderedLineWidth(lines), m.targetCoverWidth(); got != want {
		t.Fatalf("expected empty placeholder width %d, got %d", want, got)
	}
}

func TestPlaceholderCoverSizeUsesRenderedCoverFootprint(t *testing.T) {
	m := model{
		coverANSI: "abc\nxyz",
	}

	width, height := placeholderCoverSize(m)
	if width != 3 || height != 2 {
		t.Fatalf("expected placeholder to use rendered cover footprint 3x2, got %dx%d", width, height)
	}
}

func TestEnsureCoverRefreshesWhenLoadingWrongImage(t *testing.T) {
	m := model{
		zenMode:       true,
		loadingCover:  true,
		coverImageURL: "https://cdn.example.com/selected-podcast.png",
		coverCacheDir: t.TempDir(),
		httpClient:    nil,
		podcasts: []podcast{
			{
				Title:    "Selected Podcast",
				FeedURL:  "https://example.com/selected.xml",
				ImageURL: "https://cdn.example.com/selected-podcast.png",
			},
			{
				Title:    "Playing Podcast",
				FeedURL:  "https://example.com/playing.xml",
				ImageURL: "https://cdn.example.com/playing-podcast.png",
			},
		},
		selectedPodcast: 0,
		player: &audioPlayer{
			feedURL: "https://example.com/playing.xml",
			episode: episode{
				Title: "Playing Episode",
			},
		},
	}

	if cmd := m.ensureCover(); cmd == nil {
		t.Fatal("expected ensureCover to refresh when the in-flight cover is stale")
	}
}

func TestSubscriptionLastUpdatedLabel(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.Local)
	p := podcast{
		Episodes: []episode{
			{PublishedAt: now.Add(-48 * time.Hour)},
			{PublishedAt: now.Add(-10 * 24 * time.Hour)},
		},
	}

	if got := subscriptionLastUpdatedLabel(p, now); got != "2 days ago" {
		t.Fatalf("expected recent update label, got %q", got)
	}

	p.Episodes = []episode{{PublishedAt: now.Add(-12 * 24 * time.Hour)}}
	if got := subscriptionLastUpdatedLabel(p, now); got != now.Add(-12*24*time.Hour).Format("01.02") {
		t.Fatalf("expected older update date label, got %q", got)
	}

	if got := subscriptionLastUpdatedLabel(podcast{}, now); got != "Unknown update time" {
		t.Fatalf("expected unknown update label, got %q", got)
	}
}

func TestCoverReadyMsgIgnoresStaleRequest(t *testing.T) {
	m := model{
		coverRequestID: 2,
		coverImageURL:  "https://cdn.example.com/current.png",
		loadingCover:   true,
		podcasts: []podcast{
			{
				ImageURL: "https://cdn.example.com/current.png",
			},
		},
	}

	updated, _ := m.Update(coverReadyMsg{
		requestID: 1,
		imageURL:  "https://cdn.example.com/stale.png",
		path:      "/tmp/stale.png",
	})
	got := updated.(model)
	if !got.loadingCover {
		t.Fatal("expected stale cover message to leave the active load in place")
	}
	if got.coverImageURL != "https://cdn.example.com/current.png" {
		t.Fatalf("expected stale cover message to keep current image URL, got %q", got.coverImageURL)
	}
}

func TestRenderResultMsgIgnoresStaleRequest(t *testing.T) {
	m := model{
		coverRequestID: 2,
		coverImageURL:  "https://cdn.example.com/current.png",
		coverPath:      "/tmp/current.png",
		renderingCover: true,
		podcasts: []podcast{
			{
				ImageURL: "https://cdn.example.com/current.png",
			},
		},
	}

	updated, _ := m.Update(renderResultMsg{
		requestID: 1,
		imageURL:  "https://cdn.example.com/stale.png",
		path:      "/tmp/stale.png",
		ansi:      "stale",
	})
	got := updated.(model)
	if !got.renderingCover {
		t.Fatal("expected stale render result to leave the active render in place")
	}
	if got.coverANSI != "" {
		t.Fatalf("expected stale render result to be ignored, got %q", got.coverANSI)
	}
}

func TestCoverReadyMsgTransitionsFromLoadingToRendering(t *testing.T) {
	m := model{
		coverRequestID: 3,
		coverImageURL:  "https://cdn.example.com/current.png",
		loadingCover:   true,
		podcasts: []podcast{
			{
				ImageURL: "https://cdn.example.com/current.png",
			},
		},
	}

	updated, cmd := m.Update(coverReadyMsg{
		requestID: 3,
		imageURL:  "https://cdn.example.com/current.png",
		path:      "/tmp/current.png",
	})
	got := updated.(model)
	if got.loadingCover {
		t.Fatal("expected fetch completion to clear loading state")
	}
	if !got.renderingCover {
		t.Fatal("expected fetch completion to start render state")
	}
	if got.coverPath != "/tmp/current.png" {
		t.Fatalf("expected fetched cover path to be saved, got %q", got.coverPath)
	}
	if cmd == nil {
		t.Fatal("expected fetch completion to schedule render command")
	}
}

func TestCurrentImageURLUsesPlayingEpisodeInZenMode(t *testing.T) {
	m := model{
		zenMode: true,
		podcasts: []podcast{
			{
				Title:    "Selected Podcast",
				FeedURL:  "https://example.com/selected.xml",
				ImageURL: "https://cdn.example.com/selected-podcast.png",
				Episodes: []episode{
					{Title: "Selected Episode", ImageURL: "https://cdn.example.com/selected-episode.png"},
				},
			},
			{
				Title:    "Playing Podcast",
				FeedURL:  "https://example.com/playing.xml",
				ImageURL: "https://cdn.example.com/playing-podcast.png",
				Episodes: []episode{
					{Title: "Playing Episode", ImageURL: "https://cdn.example.com/playing-episode.png"},
				},
			},
		},
		selectedPodcast: 0,
		selectedEpisode: 0,
		player: &audioPlayer{
			feedURL: "https://example.com/playing.xml",
			episode: episode{
				Title:    "Playing Episode",
				ImageURL: "https://cdn.example.com/playing-episode.png",
			},
		},
	}

	if got := m.currentImageURL(); got != "https://cdn.example.com/playing-episode.png" {
		t.Fatalf("expected zen mode to use playing episode cover, got %q", got)
	}
}

func TestCurrentImageURLFallsBackToPlayingPodcastInZenMode(t *testing.T) {
	m := model{
		zenMode: true,
		podcasts: []podcast{
			{
				Title:    "Selected Podcast",
				FeedURL:  "https://example.com/selected.xml",
				ImageURL: "https://cdn.example.com/selected-podcast.png",
			},
			{
				Title:    "Playing Podcast",
				FeedURL:  "https://example.com/playing.xml",
				ImageURL: "https://cdn.example.com/playing-podcast.png",
			},
		},
		selectedPodcast: 0,
		player: &audioPlayer{
			feedURL: "https://example.com/playing.xml",
			episode: episode{
				Title: "Playing Episode",
			},
		},
	}

	if got := m.currentImageURL(); got != "https://cdn.example.com/playing-podcast.png" {
		t.Fatalf("expected zen mode to fall back to playing podcast cover, got %q", got)
	}
}

func TestRestoreSavedEpisodeSelectionFocusesSavedEpisode(t *testing.T) {
	m := model{
		podcasts: []podcast{
			{
				Title:   "Podcast 1",
				FeedURL: "https://example.com/one.xml",
				Episodes: []episode{
					{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/ep-1.mp3"},
				},
			},
			{
				Title:   "Podcast 2",
				FeedURL: "https://example.com/two.xml",
				Episodes: []episode{
					{Title: "Episode 2", GUID: "ep-2", MediaURL: "https://example.com/ep-2.mp3"},
				},
			},
		},
		savedPlayState: &playState{
			FeedURL:     "https://example.com/two.xml",
			EpisodeGUID: "ep-2",
			MediaURL:    "https://example.com/ep-2.mp3",
		},
	}

	if !m.restoreSavedEpisodeSelection() {
		t.Fatal("expected restoreSavedEpisodeSelection to find the saved episode")
	}
	if m.selectedPodcast != 1 {
		t.Fatalf("expected selected podcast 1, got %d", m.selectedPodcast)
	}
	if m.selectedEpisode != 0 {
		t.Fatalf("expected selected episode 0, got %d", m.selectedEpisode)
	}
	if m.focus != focusEpisodes {
		t.Fatalf("expected focusEpisodes after restore, got %v", m.focus)
	}
}

func TestPlayerLinesShowRestoredEpisodeWhenNoPlayerIsRunning(t *testing.T) {
	m := model{
		podcasts: []podcast{
			{
				Title:   "Podcast 1",
				FeedURL: "https://example.com/one.xml",
				Episodes: []episode{
					{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/ep-1.mp3"},
				},
			},
		},
		selectedPodcast: 0,
		selectedEpisode: 0,
		episodeDurations: map[string]time.Duration{
			"https://example.com/ep-1.mp3": 45 * time.Minute,
		},
		savedPlayState: &playState{
			FeedURL:     "https://example.com/one.xml",
			EpisodeGUID: "ep-1",
			MediaURL:    "https://example.com/ep-1.mp3",
			Position:    "2m30s",
		},
	}

	lines := m.playerLines(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Episode 1") {
		t.Fatalf("expected restored player pane to show episode title, got:\n%s", joined)
	}
	if !strings.Contains(joined, "Ready to resume") {
		t.Fatalf("expected restored player pane to show resume state, got:\n%s", joined)
	}
	if !strings.Contains(joined, "45:00") {
		t.Fatalf("expected restored player pane to show total duration, got:\n%s", joined)
	}
	if !strings.Contains(joined, "02:30") {
		t.Fatalf("expected restored player pane to show saved position, got:\n%s", joined)
	}
	if !strings.Contains(joined, "space") {
		t.Fatalf("expected restored player pane to advertise space resume, got:\n%s", joined)
	}
}

func TestPlayerLinesKeepShowingRestoredEpisodeAfterSelectionMoves(t *testing.T) {
	m := model{
		podcasts: []podcast{
			{
				Title:   "Podcast 1",
				FeedURL: "https://example.com/one.xml",
				Episodes: []episode{
					{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/ep-1.mp3"},
					{Title: "Episode 2", GUID: "ep-2", MediaURL: "https://example.com/ep-2.mp3"},
				},
			},
		},
		selectedPodcast: 0,
		selectedEpisode: 1,
		episodeDurations: map[string]time.Duration{
			"https://example.com/ep-1.mp3": 45 * time.Minute,
		},
		savedPlayState: &playState{
			FeedURL:     "https://example.com/one.xml",
			EpisodeGUID: "ep-1",
			MediaURL:    "https://example.com/ep-1.mp3",
			Position:    "2m30s",
		},
	}

	lines := m.playerLines(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Episode 1") {
		t.Fatalf("expected player pane to keep the restored episode title, got:\n%s", joined)
	}
	if strings.Contains(joined, "Episode 2") {
		t.Fatalf("expected player pane to ignore the current selection, got:\n%s", joined)
	}
}

func TestHandleKeyQDoesNotQuit(t *testing.T) {
	m := model{}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	got := updated.(model)

	if got.quitting {
		t.Fatal("expected q to no longer quit")
	}
	if cmd != nil {
		t.Fatal("expected q to have no quit command")
	}
}

func TestHandleKeyCtrlCQuits(t *testing.T) {
	m := model{}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	got := updated.(model)

	if !got.quitting {
		t.Fatal("expected ctrl+c to quit")
	}
	if cmd == nil {
		t.Fatal("expected ctrl+c to return quit command")
	}
}

func TestHandleKeyEscExitsZenMode(t *testing.T) {
	m := model{
		zenMode: true,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
			},
		},
	}

	updated, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	got := updated.(model)

	if got.zenMode {
		t.Fatal("expected esc to exit zen mode")
	}
	if got.status != "Zen mode disabled" {
		t.Fatalf("expected zen exit status, got %q", got.status)
	}
}

func TestHandleKeyLDoesNotPlayFromEpisodeDetail(t *testing.T) {
	m := model{
		focus: focusEpisodeDetail,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{Title: "Episode 1", MediaURL: "https://example.com/episode.mp3"},
				},
			},
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "l", Code: 'l'}))
	got := updated.(model)

	if cmd != nil {
		t.Fatal("expected l to do nothing in episode detail")
	}
	if got.player != nil {
		t.Fatal("expected l to not start playback in episode detail")
	}
}

func TestEpisodeDetailFooterUsesPAndSpaceOnly(t *testing.T) {
	m := model{focus: focusEpisodeDetail}

	if got := m.footerText(); got != "j/k episodes • p play selected • h back to list • z zen • esc back/exit zen • space pause/resume • r refresh • ctrl+c quit" {
		t.Fatalf("unexpected detail footer text: %q", got)
	}
}

func TestHandleKeyPDoesNothingForCurrentlyPlayingSelectedEpisode(t *testing.T) {
	m := model{
		focus: focusEpisodeDetail,
		podcasts: []podcast{
			{
				Title:   "Test Podcast",
				FeedURL: "https://example.com/feed.xml",
				Episodes: []episode{
					{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/episode.mp3"},
				},
			},
		},
		player: &audioPlayer{
			id:      7,
			feedURL: "https://example.com/feed.xml",
			episode: episode{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/episode.mp3"},
			paused:  false,
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	got := updated.(model)

	if cmd != nil {
		t.Fatal("expected p to do nothing for the currently playing selected episode")
	}
	if got.player == nil || got.player.id != 7 {
		t.Fatal("expected current playback to remain unchanged")
	}
}

func TestHandleKeySpaceDoesNothingWithoutActivePlayer(t *testing.T) {
	m := model{
		focus: focusEpisodeDetail,
		podcasts: []podcast{
			{
				Title:   "Test Podcast",
				FeedURL: "https://example.com/feed.xml",
				Episodes: []episode{
					{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/episode.mp3"},
				},
			},
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: " ", Code: tea.KeySpace}))
	got := updated.(model)

	if cmd != nil {
		t.Fatal("expected space to do nothing when nothing is playing")
	}
	if got.player != nil {
		t.Fatal("expected space to not start playback")
	}
}

func TestPlayerPlaybackProgressUsesRestoredEpisodeState(t *testing.T) {
	m := model{
		podcasts: []podcast{
			{
				Title:   "Podcast 1",
				FeedURL: "https://example.com/one.xml",
				Episodes: []episode{
					{Title: "Episode 1", GUID: "ep-1", MediaURL: "https://example.com/ep-1.mp3"},
				},
			},
		},
		episodeDurations: map[string]time.Duration{
			"https://example.com/ep-1.mp3": 10 * time.Minute,
		},
		savedPlayState: &playState{
			FeedURL:     "https://example.com/one.xml",
			EpisodeGUID: "ep-1",
			MediaURL:    "https://example.com/ep-1.mp3",
			Position:    "2m30s",
		},
	}

	if got := m.playerPlaybackProgress(); got != 0.25 {
		t.Fatalf("expected restored progress 0.25, got %v", got)
	}
}

func TestHandleKeyJScrollsEpisodeDetailWithoutChangingSelection(t *testing.T) {
	m := model{
		width:           100,
		height:          24,
		focus:           focusEpisodeDetail,
		selectedEpisode: 1,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{Title: "Episode 1", Description: "short"},
					{Title: "Episode 2", Description: strings.Repeat("long description ", 80)},
				},
			},
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	got := updated.(model)

	if cmd != nil {
		t.Fatal("expected detail scroll to not schedule a command")
	}
	if got.selectedEpisode != 1 {
		t.Fatalf("expected selected episode to stay on index 1, got %d", got.selectedEpisode)
	}
	if got.detailScrollOffset <= 0 {
		t.Fatalf("expected detail scroll offset to increase, got %d", got.detailScrollOffset)
	}
}

func TestHandleKeyHReturnsToEpisodeListWithSelectedEpisodeVisible(t *testing.T) {
	episodes := make([]episode, 10)
	for i := range episodes {
		episodes[i] = episode{
			Title:       fmt.Sprintf("Episode %d", i+1),
			Summary:     "summary",
			Description: "description",
			PubDate:     "2026-06-07",
		}
	}

	m := model{
		width:              100,
		height:             20,
		focus:              focusEpisodeDetail,
		selectedEpisode:    8,
		detailScrollOffset: 5,
		podcasts: []podcast{
			{
				Title:       "Test Podcast",
				Description: "Podcast description",
				Episodes:    episodes,
			},
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "h", Code: 'h'}))
	got := updated.(model)

	if got.focus != focusEpisodes {
		t.Fatalf("expected focus to return to episodes, got %v", got.focus)
	}
	if cmd != nil {
		t.Fatal("expected no command when leaving detail without a cover to refresh")
	}

	panel := got.renderDetailPanel(56, 14)
	if !strings.Contains(panel, "Episode 9") {
		t.Fatalf("expected selected episode to be visible after returning to list, panel was:\n%s", panel)
	}
}

func TestClampTextLinesKeepsCJKPreviewToTwoVisualLines(t *testing.T) {
	summary := "2024\n年森泽奖欧文组有一个令人难忘的名字——包揽金、银、佳作三大奖项——王乃谦。本期节目我们有幸请到王乃谦，与听众分享多元化的设计求学经历，以\n及富于反思的创作理念及工作方法。…"

	lines := clampTextLines(summary, 20, 2)

	if got := len(lines); got != 2 {
		t.Fatalf("expected exactly 2 preview lines, got %d: %#v", got, lines)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > 20 {
			t.Fatalf("expected line %d width <= 20, got %d for %q", i, width, line)
		}
	}
	if !strings.HasSuffix(lines[len(lines)-1], "…") {
		t.Fatalf("expected final preview line to end with ellipsis, got %q", lines[len(lines)-1])
	}
}

func TestRenderListCardKeepsEpisodePreviewAtFixedHeightForCJKText(t *testing.T) {
	description := clampTextLines("六年前，「上海活字」项目在几个人的不期而遇中诞生。今天，项目发起人厉致谦做客嘉宾，与我们分享「上海活字」发掘的字体设计与前辈设计师们的故事。", episodePreviewWidth(20), 2)

	card := renderListCard("Episode Title", description, "2026-06-07", 20, false)
	lines := strings.Split(card, "\n")

	if got := len(lines); got != episodeItemHeight {
		t.Fatalf("expected card height %d, got %d:\n%s", episodeItemHeight, got, card)
	}
	for i, line := range lines[:len(lines)-1] {
		if width := lipgloss.Width(line); width > 20 {
			t.Fatalf("expected rendered line %d width <= 20, got %d for %q", i, width, line)
		}
	}
}

func TestEpisodeDetailLinesUseStructuredDescriptionFormatting(t *testing.T) {
	m := model{
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{
						Title:           "Episode 1",
						Description:     "第一段 第二段 其一 其二",
						DescriptionHTML: "<p>第一段</p><p>第二段</p><ul><li>其一</li><li>其二</li></ul>",
					},
				},
			},
		},
	}

	lines := m.episodeDetailLines(24)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "第一段\n\n第二段") {
		t.Fatalf("expected paragraph break in detail view, got:\n%s", joined)
	}
	if !strings.Contains(joined, "• 其一") || !strings.Contains(joined, "• 其二") {
		t.Fatalf("expected bullet list in detail view, got:\n%s", joined)
	}
}

func TestEpisodeListPreviewClampingIgnoresStructuredDescriptionHTML(t *testing.T) {
	m := model{
		focus: focusEpisodes,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{
						Title:           "Episode 1",
						Summary:         strings.Repeat("preview text ", 12),
						Description:     "plain description",
						DescriptionHTML: "<p>第一段</p><ul><li>其一</li><li>其二</li></ul>",
						PubDate:         "2026-06-07",
					},
				},
			},
		},
	}

	lines := m.detailLines(24)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "• 其一") {
		t.Fatalf("expected episode list preview to ignore detail-only HTML formatting, got:\n%s", joined)
	}
	if count := strings.Count(joined, "preview"); count == 0 {
		t.Fatalf("expected episode list preview to still render summary text, got:\n%s", joined)
	}
}

func TestHandleKeyGGScrollsEpisodeDetailToTop(t *testing.T) {
	m := model{
		width:              100,
		height:             24,
		focus:              focusEpisodeDetail,
		selectedEpisode:    1,
		detailScrollOffset: 7,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{Title: "Episode 1", Description: "short"},
					{Title: "Episode 2", Description: strings.Repeat("long description ", 80)},
				},
			},
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "g", Code: 'g'}))
	got := updated.(model)
	if cmd != nil {
		t.Fatal("expected first g to not schedule a command")
	}
	if !got.pendingTopJump {
		t.Fatal("expected first g to arm top jump")
	}
	if got.detailScrollOffset != 7 {
		t.Fatalf("expected first g to keep offset, got %d", got.detailScrollOffset)
	}

	updated, cmd = got.handleKey(tea.KeyPressMsg(tea.Key{Text: "g", Code: 'g'}))
	got = updated.(model)
	if cmd != nil {
		t.Fatal("expected second g to not schedule a command")
	}
	if got.pendingTopJump {
		t.Fatal("expected second g to clear top jump")
	}
	if got.detailScrollOffset != 0 {
		t.Fatalf("expected gg to jump to top, got %d", got.detailScrollOffset)
	}
	if got.selectedEpisode != 1 {
		t.Fatalf("expected selected episode to stay on index 1, got %d", got.selectedEpisode)
	}
}

func TestHandleKeyGScrollsEpisodeDetailToBottom(t *testing.T) {
	m := model{
		width:           100,
		height:          24,
		focus:           focusEpisodeDetail,
		selectedEpisode: 1,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{Title: "Episode 1", Description: "short"},
					{Title: "Episode 2", Description: strings.Repeat("long description ", 120)},
				},
			},
		},
	}

	maxOffset := m.maxEpisodeDetailOffset()
	if maxOffset <= 0 {
		t.Fatalf("expected test fixture to produce a scrollable detail view, got %d", maxOffset)
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "G", Code: 'G'}))
	got := updated.(model)
	if cmd != nil {
		t.Fatal("expected G to not schedule a command")
	}
	if got.detailScrollOffset != maxOffset {
		t.Fatalf("expected G to jump to bottom offset %d, got %d", maxOffset, got.detailScrollOffset)
	}
	if got.selectedEpisode != 1 {
		t.Fatalf("expected selected episode to stay on index 1, got %d", got.selectedEpisode)
	}
}

func TestHandleKeyGShowsLastDetailLine(t *testing.T) {
	m := model{
		width:           100,
		height:          24,
		focus:           focusEpisodeDetail,
		selectedEpisode: 0,
		podcasts: []podcast{
			{
				Title: "Test Podcast",
				Episodes: []episode{
					{
						Title:           "Episode 1",
						DescriptionHTML: "<p>intro</p><p>" + strings.Repeat("long section ", 140) + "</p><p>THE END MARKER</p>",
					},
				},
			},
		},
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg(tea.Key{Text: "G", Code: 'G'}))
	got := updated.(model)
	if cmd != nil {
		t.Fatal("expected G to not schedule a command")
	}

	_, width := paneWidths(got.width)
	height := paneHeight(got.height)
	detailHeight, _ := got.rightPaneHeights(width, height)
	panel := got.renderDetailPanel(width, detailHeight)
	if !strings.Contains(panel, "THE END MARKER") {
		t.Fatalf("expected bottom jump to reveal the final detail line, panel was:\n%s", panel)
	}
}
