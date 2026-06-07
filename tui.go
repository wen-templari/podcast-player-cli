package main

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	appStyle = lipgloss.NewStyle().
			Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F2C14E"))

	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C7C7C"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4A4A4A")).
			Padding(1, 2)

	playerPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#4A4A4A")).
				Padding(0, 1)

	activePanelStyle = panelStyle.Copy().
				BorderForeground(lipgloss.Color("#F2C14E"))

	listItemTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#D8D8D8")).
				Padding(0, 0, 0, 2)

	listItemMetaStyle = subtleStyle.Copy().
				Padding(0, 0, 0, 2)

	selectedListItemTitleStyle = listItemTitleStyle.Copy().
					Foreground(lipgloss.Color("#F2C14E")).
					Border(lipgloss.NormalBorder(), false, false, false, true).
					BorderForeground(lipgloss.Color("#F2C14E")).
					Padding(0, 0, 0, 1)

	selectedListItemMetaStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#C9A74A")).
					Border(lipgloss.NormalBorder(), false, false, false, true).
					BorderForeground(lipgloss.Color("#F2C14E")).
					Padding(0, 0, 0, 1)

	sectionLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#D8D8D8"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B"))
)

type browserMode int

const (
	focusSubscriptions browserMode = iota
	focusEpisodes
	focusEpisodeDetail

	subscriptionItemHeight = 3
	episodeItemHeight      = 5
)

type feedLoadedMsg struct {
	podcasts []podcast
	err      error
}

type coverReadyMsg struct {
	requestID int
	imageURL  string
	path      string
	err       error
}

type renderResultMsg struct {
	requestID int
	imageURL  string
	path      string
	width     int
	height    int
	ansi      string
	err       error
}

type playbackFinishedMsg struct {
	playerID int
	err      error
	stderr   string
}

type playbackTickMsg struct {
	playerID int
	at       time.Time
}

type durationLoadedMsg struct {
	mediaURL string
	duration time.Duration
	err      error
}

type model struct {
	width              int
	height             int
	zenMode            bool
	feedURLs           []string
	proxyURL           string
	coverCacheDir      string
	feedCacheDir       string
	renderCacheDir     string
	durationCachePath  string
	playStatePath      string
	httpClient         *http.Client
	focus              browserMode
	podcasts           []podcast
	selectedPodcast    int
	selectedEpisode    int
	coverRequestID     int
	coverImageURL      string
	coverPath          string
	coverANSI          string
	coverRenderWidth   int
	coverRenderHeight  int
	renderErr          error
	status             string
	errMessage         string
	loadingFeeds       bool
	loadingCover       bool
	renderingCover     bool
	loadingSpinner     spinner.Model
	player             *audioPlayer
	playbackProgress   progress.Model
	nextPlayerID       int
	episodeDurations   map[string]time.Duration
	savedPlayState     *playState
	probingDurationFor string
	detailScrollOffset int
	listViewport       viewport.Model
	pendingTopJump     bool
	quitting           bool
}

func newModel(feedURLs []string, proxyURL, coverCacheDir, feedCacheDir, renderCacheDir, durationCachePath, playStatePath string, client *http.Client, episodeDurations map[string]time.Duration, savedPlayState *playState) model {
	if episodeDurations == nil {
		episodeDurations = map[string]time.Duration{}
	}
	return model{
		feedURLs:          feedURLs,
		proxyURL:          proxyURL,
		coverCacheDir:     coverCacheDir,
		feedCacheDir:      feedCacheDir,
		renderCacheDir:    renderCacheDir,
		durationCachePath: durationCachePath,
		playStatePath:     playStatePath,
		httpClient:        client,
		focus:             focusSubscriptions,
		loadingFeeds:      true,
		status:            "Loading subscriptions...",
		playbackProgress: progress.New(
			progress.WithColors(
				lipgloss.Color("#F2C14E"),
				lipgloss.Color("#F59E0B"),
			),
			progress.WithScaled(true),
			progress.WithoutPercentage(),
		),
		loadingSpinner: spinner.New(
			spinner.WithSpinner(spinner.Dot),
			spinner.WithStyle(subtleStyle),
		),
		episodeDurations: episodeDurations,
		savedPlayState:   savedPlayState,
		listViewport:     viewport.New(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		loadFeedsCmd(m.httpClient, m.feedURLs, m.feedCacheDir),
		m.loadingSpinner.Tick,
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progress.FrameMsg:
		var cmd tea.Cmd
		m.playbackProgress, cmd = m.playbackProgress.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.loadingSpinner, cmd = m.loadingSpinner.Update(msg)
		if m.loadingFeeds {
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		width := m.targetCoverWidth()
		height := m.targetCoverHeight()
		if m.coverPath != "" && m.coverImageURL == m.currentImageURL() && (m.coverANSI == "" || width != m.coverRenderWidth || height != m.coverRenderHeight) {
			m.renderingCover = true
			return m, renderCoverCmd(m.coverRequestID, m.coverImageURL, m.coverPath, m.renderCacheDir, width, height)
		}
	case feedLoadedMsg:
		m.loadingFeeds = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			m.status = "Failed to load subscriptions"
			return m, nil
		}
		m.podcasts = msg.podcasts
		m.selectedPodcast = 0
		m.selectedEpisode = 0
		m.focus = focusSubscriptions
		if len(m.podcasts) == 0 {
			m.status = "No subscriptions found"
			return m, nil
		}
		m.errMessage = ""
		m.status = fmt.Sprintf("Loaded %d subscription(s)", len(m.podcasts))
		if m.restoreSavedEpisodeSelection() {
			m.status = "Restored last played episode"
		}
		cmd := m.refreshCover()
		return m, cmd
	case coverReadyMsg:
		if msg.requestID != m.coverRequestID {
			return m, nil
		}
		if msg.err != nil {
			m.coverImageURL = ""
			m.coverPath = ""
			m.coverANSI = ""
			m.renderErr = msg.err
			m.loadingCover = false
			m.renderingCover = false
			return m, nil
		}
		m.coverImageURL = msg.imageURL
		m.coverPath = msg.path
		m.loadingCover = false
		m.renderErr = nil
		m.renderingCover = true
		return m, renderCoverCmd(m.coverRequestID, m.coverImageURL, m.coverPath, m.renderCacheDir, m.targetCoverWidth(), m.targetCoverHeight())
	case renderResultMsg:
		if msg.requestID != m.coverRequestID || msg.imageURL != m.coverImageURL || msg.path != m.coverPath {
			return m, nil
		}
		m.loadingCover = false
		m.renderingCover = false
		m.coverANSI = msg.ansi
		m.coverRenderWidth = msg.width
		m.coverRenderHeight = msg.height
		m.renderErr = msg.err
	case playbackTickMsg:
		if m.player != nil && m.player.id == msg.playerID && !m.player.paused {
			m.persistCurrentPlayState()
			return m, tea.Batch(m.syncPlaybackProgress(), playbackTickCmd(msg.playerID))
		}
	case durationLoadedMsg:
		if msg.err == nil && msg.mediaURL != "" {
			m.episodeDurations[msg.mediaURL] = msg.duration
			if err := saveDurationCache(m.durationCachePath, m.episodeDurations); err != nil && m.errMessage == "" {
				m.errMessage = "failed to update duration cache: " + err.Error()
			}
		}
		if m.probingDurationFor == msg.mediaURL {
			m.probingDurationFor = ""
		}
		if m.player != nil && m.player.episode.MediaURL == msg.mediaURL {
			return m, m.syncPlaybackProgress()
		}
	case playbackFinishedMsg:
		if m.player == nil || m.player.id != msg.playerID {
			return m, nil
		}
		if m.player.paused && m.player.stoppingForPause {
			m.player.cmd = nil
			m.player.stderr = nil
			m.player.stoppingForPause = false
			m.status = "Paused: " + m.player.episode.Title
			return m, m.syncPlaybackProgress()
		}
		finishedEpisode := m.player.episode
		m.player = nil
		if msg.err != nil {
			m.status = "Playback stopped"
			if msg.stderr != "" {
				m.errMessage = msg.stderr
			} else {
				m.errMessage = msg.err.Error()
			}
		} else if msg.stderr != "" {
			m.status = "Playback stopped"
			m.errMessage = msg.stderr
		} else {
			m.status = "Playback finished"
			if episodeMatchesState(finishedEpisode, m.savedPlayState) {
				m.clearSavedPlayState()
			}
		}
		return m, m.syncPlaybackProgress()
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key != "g" {
		m.pendingTopJump = false
	}

	switch key {
	case "ctrl+c":
		if m.player != nil {
			m.persistCurrentPlayState()
			stopPlayback(m.player)
		}
		m.quitting = true
		return m, tea.Quit
	case "esc":
		if m.zenMode {
			m.zenMode = false
			m.errMessage = ""
			m.status = "Zen mode disabled"
			width := m.targetCoverWidth()
			height := m.targetCoverHeight()
			if m.coverPath != "" && m.coverImageURL == m.currentImageURL() && (m.coverANSI == "" || width != m.coverRenderWidth || height != m.coverRenderHeight) {
				m.renderingCover = true
				return m, renderCoverCmd(m.coverRequestID, m.coverImageURL, m.coverPath, m.renderCacheDir, width, height)
			}
			return m, m.ensureCover()
		}
	case "z":
		m.zenMode = !m.zenMode
		m.errMessage = ""
		if m.zenMode {
			m.status = "Zen mode enabled"
		} else {
			m.status = "Zen mode disabled"
		}
		width := m.targetCoverWidth()
		height := m.targetCoverHeight()
		if m.coverPath != "" && m.coverImageURL == m.currentImageURL() && (m.coverANSI == "" || width != m.coverRenderWidth || height != m.coverRenderHeight) {
			m.renderingCover = true
			return m, renderCoverCmd(m.coverRequestID, m.coverImageURL, m.coverPath, m.renderCacheDir, width, height)
		}
		return m, m.ensureCover()
	case "up", "k":
		if m.focus == focusEpisodeDetail {
			m.scrollEpisodeDetail(-1)
			return m, nil
		}
		return m.moveSelection(-1)
	case "down", "j":
		if m.focus == focusEpisodeDetail {
			m.scrollEpisodeDetail(1)
			return m, nil
		}
		return m.moveSelection(1)
	case "G":
		if m.focus == focusEpisodeDetail {
			m.scrollEpisodeDetailToBottom()
			return m, nil
		}
		if m.focus == focusSubscriptions {
			return m.jumpSelection(len(m.podcasts) - 1)
		}
		return m.jumpSelection(len(m.currentEpisodes()) - 1)
	case "g":
		if m.focus == focusEpisodeDetail {
			if m.pendingTopJump {
				m.pendingTopJump = false
				m.detailScrollOffset = 0
			} else {
				m.pendingTopJump = true
			}
			return m, nil
		}
		if m.pendingTopJump {
			m.pendingTopJump = false
			return m.jumpSelection(0)
		}
		m.pendingTopJump = true
		return m, nil
	case "enter", "right", "l":
		if m.focus == focusSubscriptions {
			if len(m.currentEpisodes()) == 0 {
				return m, nil
			}
			m.focus = focusEpisodes
			m.errMessage = ""
			m.status = fmt.Sprintf("Browsing episodes for %s", m.currentPodcast().Title)
			return m, m.ensureEpisodeDuration()
		}
		if m.focus == focusEpisodes {
			m.focus = focusEpisodeDetail
			m.detailScrollOffset = 0
			m.errMessage = ""
			m.status = "Viewing episode details"
			return m, tea.Batch(m.ensureEpisodeDuration(), m.refreshCover())
		}
		return m, nil
	case "left", "h", "backspace":
		if m.focus == focusEpisodeDetail {
			m.focus = focusEpisodes
			m.errMessage = ""
			m.status = fmt.Sprintf("Browsing episodes for %s", m.currentPodcast().Title)
			return m, m.refreshCover()
		}
		if m.focus == focusEpisodes {
			m.focus = focusSubscriptions
			m.errMessage = ""
			m.status = "Browsing subscriptions"
		}
		return m, nil
	case "p", "space":
		if msg.String() == "space" {
			if m.player != nil {
				return m.togglePausePlayback()
			}
			if _, ep := m.restoredPlayerPaneEpisode(); ep != nil {
				return m.resumeSavedEpisode()
			}
			return m, nil
		}
		if (m.focus == focusEpisodes || m.focus == focusEpisodeDetail) && !m.selectedEpisodeIsPlaying() {
			return m.startSelectedEpisode()
		}
		return m, nil
	case "r":
		m.loadingFeeds = true
		m.errMessage = ""
		m.status = "Refreshing subscriptions..."
		return m, tea.Batch(
			loadFeedsCmd(m.httpClient, m.feedURLs, m.feedCacheDir),
			m.loadingSpinner.Tick,
		)
	}

	return m, nil
}

func (m model) moveSelection(delta int) (tea.Model, tea.Cmd) {
	if len(m.podcasts) == 0 {
		return m, nil
	}

	if m.focus == focusSubscriptions {
		next := clampIndex(m.selectedPodcast+delta, len(m.podcasts))
		if next == m.selectedPodcast {
			return m, nil
		}
		m.selectedPodcast = next
		m.selectedEpisode = 0
		cmd := m.refreshCover()
		return m, cmd
	}

	episodes := m.currentEpisodes()
	if len(episodes) == 0 {
		return m, nil
	}
	next := clampIndex(m.selectedEpisode+delta, len(episodes))
	if next == m.selectedEpisode {
		return m, nil
	}
	m.selectedEpisode = next
	if m.focus == focusEpisodeDetail {
		return m, tea.Batch(m.ensureEpisodeDuration(), m.refreshCover())
	}
	return m, m.ensureEpisodeDuration()
}

func (m model) jumpSelection(index int) (tea.Model, tea.Cmd) {
	if m.focus == focusSubscriptions {
		if len(m.podcasts) == 0 {
			return m, nil
		}
		next := clampIndex(index, len(m.podcasts))
		if next == m.selectedPodcast {
			return m, nil
		}
		m.selectedPodcast = next
		m.selectedEpisode = 0
		return m, m.refreshCover()
	}

	episodes := m.currentEpisodes()
	if len(episodes) == 0 {
		return m, nil
	}
	next := clampIndex(index, len(episodes))
	if next == m.selectedEpisode {
		return m, nil
	}
	m.selectedEpisode = next
	if m.focus == focusEpisodeDetail {
		return m, tea.Batch(m.ensureEpisodeDuration(), m.refreshCover())
	}
	return m, m.ensureEpisodeDuration()
}

func (m model) startSelectedEpisode() (tea.Model, tea.Cmd) {
	episodes := m.currentEpisodes()
	if len(episodes) == 0 {
		return m, nil
	}
	podcast := m.currentPodcast()
	if podcast == nil {
		return m, nil
	}

	return m.startEpisodePlayback(*podcast, episodes[m.selectedEpisode])
}

func (m model) resumeSavedEpisode() (tea.Model, tea.Cmd) {
	podcast, ep := m.restoredPlayerPaneEpisode()
	if podcast == nil || ep == nil {
		return m, nil
	}
	return m.startEpisodePlayback(*podcast, *ep)
}

func (m model) startEpisodePlayback(p podcast, ep episode) (tea.Model, tea.Cmd) {
	if ep.MediaURL == "" {
		m.errMessage = "selected episode has no media URL"
		return m, nil
	}

	if m.player != nil {
		m.persistCurrentPlayState()
		stopPlayback(m.player)
		m.player = nil
	}

	resumeOffset := m.resumeOffsetForEpisode(ep)
	player, err := startPlayback(m.nextPlayerID, m.proxyURL, p.Title, p.FeedURL, ep, resumeOffset)
	if err != nil {
		m.errMessage = err.Error()
		m.status = "Playback failed"
		return m, nil
	}

	m.player = player
	m.nextPlayerID++
	m.persistCurrentPlayState()
	m.errMessage = ""
	if resumeOffset > 0 {
		m.status = "Resumed: " + ep.Title
	} else {
		m.status = "Playing: " + ep.Title
	}
	return m, tea.Batch(
		m.syncPlaybackProgress(),
		waitForPlaybackCmd(player),
		playbackTickCmd(player.id),
		m.ensureDurationForMedia(ep.MediaURL),
	)
}

func (m *model) refreshCover() tea.Cmd {
	m.coverRequestID++
	imageURL := m.currentImageURL()
	if imageURL == "" {
		m.coverImageURL = ""
		m.coverPath = ""
		m.coverANSI = ""
		m.renderErr = errors.New("no cover art available")
		m.loadingCover = false
		m.renderingCover = false
		return nil
	}

	m.coverImageURL = imageURL
	m.coverPath = ""
	m.loadingCover = true
	m.renderingCover = false
	m.renderErr = nil
	return fetchCoverCmd(m.coverRequestID, m.httpClient, m.coverCacheDir, imageURL)
}

func (m *model) ensureCover() tea.Cmd {
	if (m.loadingCover || m.renderingCover) && m.coverImageURL == m.currentImageURL() {
		return nil
	}
	if m.coverANSI != "" && m.coverImageURL == m.currentImageURL() {
		return nil
	}
	return m.refreshCover()
}

func (m model) currentPodcast() *podcast {
	if len(m.podcasts) == 0 || m.selectedPodcast < 0 || m.selectedPodcast >= len(m.podcasts) {
		return nil
	}
	return &m.podcasts[m.selectedPodcast]
}

func (m model) currentImageURL() string {
	if m.zenMode {
		if imageURL := m.playingImageURL(); imageURL != "" {
			return imageURL
		}
	}
	if m.focus == focusEpisodeDetail {
		if ep := m.currentEpisode(); ep != nil && ep.ImageURL != "" {
			return ep.ImageURL
		}
	}
	p := m.currentPodcast()
	if p == nil {
		return ""
	}
	return p.ImageURL
}

func (m model) playingImageURL() string {
	if m.player == nil {
		return ""
	}
	if m.player.episode.ImageURL != "" {
		return m.player.episode.ImageURL
	}
	if p := m.podcastForFeedURL(m.player.feedURL); p != nil {
		return p.ImageURL
	}
	return ""
}

func (m model) podcastForFeedURL(feedURL string) *podcast {
	for i := range m.podcasts {
		if m.podcasts[i].FeedURL == feedURL {
			return &m.podcasts[i]
		}
	}
	return nil
}

func (m model) currentEpisodes() []episode {
	p := m.currentPodcast()
	if p == nil {
		return nil
	}
	return p.Episodes
}

func (m model) currentEpisode() *episode {
	episodes := m.currentEpisodes()
	if len(episodes) == 0 || m.selectedEpisode < 0 || m.selectedEpisode >= len(episodes) {
		return nil
	}
	return &episodes[m.selectedEpisode]
}

func episodeMatchesState(ep episode, state *playState) bool {
	if state == nil {
		return false
	}
	if state.EpisodeGUID != "" && ep.GUID != "" && state.EpisodeGUID == ep.GUID {
		return true
	}
	return state.MediaURL != "" && ep.MediaURL == state.MediaURL
}

func (m *model) restoreSavedEpisodeSelection() bool {
	podcastIndex, episodeIndex, ok := m.findEpisodeIndexForState(m.savedPlayState)
	if !ok {
		return false
	}
	m.selectedPodcast = podcastIndex
	m.selectedEpisode = episodeIndex
	m.focus = focusEpisodes
	return true
}

func (m model) currentEpisodeDuration() time.Duration {
	ep := m.currentEpisode()
	if ep == nil || ep.MediaURL == "" {
		return 0
	}
	return m.durationForMedia(ep.MediaURL)
}

func (m *model) ensureEpisodeDuration() tea.Cmd {
	ep := m.currentEpisode()
	if ep == nil || ep.MediaURL == "" {
		return nil
	}
	return m.ensureDurationForMedia(ep.MediaURL)
}

func (m model) durationForMedia(mediaURL string) time.Duration {
	if mediaURL == "" {
		return 0
	}
	return m.episodeDurations[mediaURL]
}

func (m model) findEpisodeIndexForState(state *playState) (int, int, bool) {
	if state == nil {
		return 0, 0, false
	}
	for podcastIndex, p := range m.podcasts {
		if state.FeedURL != "" && p.FeedURL != "" && p.FeedURL != state.FeedURL {
			continue
		}
		for episodeIndex, ep := range p.Episodes {
			if episodeMatchesState(ep, state) {
				return podcastIndex, episodeIndex, true
			}
		}
	}
	return 0, 0, false
}

func (m model) restoredPlayerPaneEpisode() (*podcast, *episode) {
	if m.player != nil || m.savedPlayState == nil {
		return nil, nil
	}
	podcastIndex, episodeIndex, ok := m.findEpisodeIndexForState(m.savedPlayState)
	if !ok {
		return nil, nil
	}
	return &m.podcasts[podcastIndex], &m.podcasts[podcastIndex].Episodes[episodeIndex]
}

func (m model) resumeOffsetForEpisode(ep episode) time.Duration {
	if !episodeMatchesState(ep, m.savedPlayState) || m.savedPlayState == nil {
		return 0
	}

	offset, err := time.ParseDuration(m.savedPlayState.Position)
	if err != nil || offset <= 0 {
		return 0
	}

	duration := m.durationForMedia(ep.MediaURL)
	if duration > 0 && offset >= duration-(5*time.Second) {
		return 0
	}
	return offset
}

func (m model) selectedEpisodeIsPlaying() bool {
	if m.player == nil {
		return false
	}
	podcast := m.currentPodcast()
	ep := m.currentEpisode()
	if podcast == nil || ep == nil {
		return false
	}
	if podcast.FeedURL != "" && m.player.feedURL != "" && podcast.FeedURL != m.player.feedURL {
		return false
	}
	return episodeMatchesState(*ep, &playState{
		EpisodeGUID: strings.TrimSpace(m.player.episode.GUID),
		MediaURL:    strings.TrimSpace(m.player.episode.MediaURL),
	})
}

func (m *model) persistCurrentPlayState() {
	if m.player == nil {
		return
	}
	state := playState{
		FeedURL:      strings.TrimSpace(m.player.feedURL),
		EpisodeGUID:  strings.TrimSpace(m.player.episode.GUID),
		MediaURL:     strings.TrimSpace(m.player.episode.MediaURL),
		PodcastTitle: strings.TrimSpace(m.player.podcastTitle),
		EpisodeTitle: strings.TrimSpace(m.player.episode.Title),
		Position:     playerElapsed(m.player).Round(time.Second).String(),
		UpdatedAt:    time.Now(),
	}
	if err := savePlayState(m.playStatePath, state); err != nil && m.errMessage == "" {
		m.errMessage = "failed to save play state: " + err.Error()
		return
	}
	m.savedPlayState = &state
}

func (m *model) clearSavedPlayState() {
	if err := clearPlayState(m.playStatePath); err != nil && m.errMessage == "" {
		m.errMessage = "failed to clear play state: " + err.Error()
		return
	}
	m.savedPlayState = nil
}

func (m *model) ensureDurationForMedia(mediaURL string) tea.Cmd {
	if mediaURL == "" {
		return nil
	}
	if _, ok := m.episodeDurations[mediaURL]; ok {
		return nil
	}
	if m.probingDurationFor == mediaURL {
		return nil
	}
	m.probingDurationFor = mediaURL
	return probeDurationCmd(mediaURL)
}

func (m model) isCurrentEpisodePlaying() bool {
	ep := m.currentEpisode()
	return m.player != nil && ep != nil && m.player.episode.MediaURL == ep.MediaURL
}

func (m model) togglePausePlayback() (tea.Model, tea.Cmd) {
	if m.player == nil {
		return m, nil
	}
	if m.player.paused {
		if err := resumePlayback(m.player, m.proxyURL); err != nil {
			m.errMessage = err.Error()
			return m, nil
		}
		m.errMessage = ""
		m.status = "Resumed: " + m.player.episode.Title
		return m, tea.Batch(m.syncPlaybackProgress(), waitForPlaybackCmd(m.player), playbackTickCmd(m.player.id))
	}
	if err := pausePlayback(m.player); err != nil {
		m.errMessage = err.Error()
		return m, nil
	}
	m.errMessage = ""
	m.status = "Paused: " + m.player.episode.Title
	return m, m.syncPlaybackProgress()
}

func (m model) View() tea.View {
	if m.quitting {
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}

	if m.zenMode {
		v := tea.NewView(appStyle.Render(m.renderZenView()))
		v.AltScreen = true
		return v
	}

	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderListPanel(),
		m.renderRightColumn(),
	)

	footer := subtleStyle.Render(m.footerText())
	if m.errMessage != "" {
		footer = errorStyle.Render(m.errMessage)
	}
	v := tea.NewView(appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, body, "", footer)))
	v.AltScreen = true
	return v
}

func (m model) renderZenView() string {
	contentWidth := maxInt(24, m.width-8)
	cover := coverLines(m, contentWidth)
	coverBlockWidth := maxRenderedLineWidth(cover)
	if coverBlockWidth == 0 {
		coverBlockWidth = minInt(contentWidth, 24)
	}
	coverBlock := lipgloss.NewStyle().
		Width(coverBlockWidth).
		Align(lipgloss.Center).
		Render(strings.Join(cover, "\n"))

	infoWidth := clampInt(maxInt(coverBlockWidth, 32), 32, contentWidth)
	infoBlock := lipgloss.NewStyle().
		Width(infoWidth).
		Align(lipgloss.Center).
		Render(strings.Join(m.zenInfoLines(infoWidth), "\n"))

	block := lipgloss.JoinVertical(lipgloss.Center, coverBlock, "", infoBlock)
	hint := lipgloss.NewStyle().
		Width(maxInt(1, m.width-4)).
		Align(lipgloss.Center).
		Render(subtleStyle.Render(truncate(m.zenShortcutHint(maxInt(24, m.width-8)), maxInt(24, m.width-8))))
	bodyHeight := maxInt(1, m.height-3)
	body := lipgloss.Place(
		maxInt(1, m.width-4),
		bodyHeight,
		lipgloss.Center,
		lipgloss.Center,
		block,
	)
	return lipgloss.JoinVertical(lipgloss.Left, body, hint)
}

func (m model) renderListPanel() string {
	width, _ := paneWidths(m.width)
	height := paneHeight(m.height)
	style := panelStyle
	if m.focus == focusSubscriptions {
		style = activePanelStyle
	}
	contentWidth, contentHeight := panelContentSize(style, width, height)

	lines := []string{}
	if m.loadingFeeds {
		lines = append(lines, lipgloss.JoinHorizontal(
			lipgloss.Center,
			m.loadingSpinner.View(),
			" ",
			subtleStyle.Render("Loading subscriptions..."),
		))
	} else if len(m.podcasts) == 0 {
		lines = append(lines, subtleStyle.Render("No subscriptions"))
	} else {
		lines = append(lines, m.listItems(contentWidth)...)
	}

	m.listViewport.SetWidth(contentWidth)
	m.listViewport.SetHeight(contentHeight)
	m.listViewport.SetContent(strings.Join(lines, "\n"))
	m.listViewport.SetYOffset(listViewportOffset(len(lines), contentHeight, m.selectedListLine(), subscriptionItemHeight))

	content := lipgloss.NewStyle().
		Width(contentWidth).
		MaxWidth(contentWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(m.listViewport.View())
	return style.Width(width).Height(height).Render(content)
}

func (m model) renderRightColumn() string {
	_, width := paneWidths(m.width)
	height := paneHeight(m.height)
	topHeight, bottomHeight := m.rightPaneHeights(width, height)
	top := m.renderDetailPanel(width, topHeight)
	bottom := m.renderPlayerPanel(width, bottomHeight)
	return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
}

func (m model) renderDetailPanel(width, height int) string {
	style := panelStyle
	if m.focus == focusEpisodes || m.focus == focusEpisodeDetail {
		style = activePanelStyle
	}
	contentWidth, contentHeight := panelContentSize(style, width, height)

	lines := m.detailLines(contentWidth)
	offset := 0
	if m.focus == focusEpisodeDetail {
		offset = clampInt(m.detailScrollOffset, 0, maxInt(0, len(lines)-contentHeight))
	} else if m.focus == focusEpisodes {
		offset = listViewportOffset(len(lines), contentHeight, m.selectedDetailLine(contentWidth), episodeItemHeight)
	}
	lines = visibleLinesAtOffset(lines, contentHeight, offset)
	content := lipgloss.NewStyle().
		Width(contentWidth).
		MaxWidth(contentWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(strings.Join(lines, "\n"))
	return style.Width(width).Height(height).Render(content)
}

func (m model) renderPlayerPanel(width, height int) string {
	contentWidth, contentHeight := panelContentSize(playerPanelStyle, width, height)
	lines := visibleLines(m.playerLines(contentWidth), contentHeight, -1)
	content := lipgloss.NewStyle().
		Width(contentWidth).
		MaxWidth(contentWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(strings.Join(lines, "\n"))
	return playerPanelStyle.Width(width).Height(height).Render(content)
}

func (m model) zenInfoLines(contentWidth int) []string {
	nowPlaying := "Nothing playing"
	if m.player != nil {
		nowPlaying = m.player.episode.Title
	} else if ep := m.currentEpisode(); ep != nil {
		nowPlaying = ep.Title
	}

	lines := []string{titleStyle.Render(truncate(nowPlaying, contentWidth))}
	if m.player != nil {
		state := "playing"
		if m.player.paused {
			state = "paused"
		}
		lines = append(lines, "")
		lines = append(lines, m.renderPlaybackProgress(contentWidth))
		lines = append(lines, subtleStyle.Render(truncate(m.playerPlaybackStatusText(), contentWidth)))
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render(truncate(fmt.Sprintf("[%s]", state), contentWidth)))
		return lines
	}

	lines = append(lines, "")
	lines = append(lines, subtleStyle.Render(truncate("Select an episode and press enter or p to play", contentWidth)))
	return lines
}

func (m model) zenShortcutHint(contentWidth int) string {
	if m.player != nil {
		return "[space] pause/resume   [p] play selected   [z/esc] exit zen   [ctrl+c] quit"
	}
	return "[p] play selected   [z/esc] exit zen   [ctrl+c] quit"
}

func (m model) listItems(contentWidth int) []string {
	lines := make([]string, 0, len(m.podcasts)*subscriptionItemHeight)
	for i, p := range m.podcasts {
		title := truncate(p.Title, contentWidth)
		meta := truncate(subscriptionLastUpdatedLabel(p, time.Now()), contentWidth)
		lines = append(lines, strings.Split(renderListCard(title, nil, meta, contentWidth, i == m.selectedPodcast), "\n")...)
	}
	return trimTrailingBlank(lines)
}

func subscriptionLastUpdatedLabel(p podcast, now time.Time) string {
	var latest time.Time
	for _, ep := range p.Episodes {
		if ep.PublishedAt.IsZero() {
			continue
		}
		if latest.IsZero() || ep.PublishedAt.After(latest) {
			latest = ep.PublishedAt
		}
	}
	if latest.IsZero() {
		return "Unknown update time"
	}

	days := int(now.In(time.Local).Sub(latest.In(time.Local)).Hours() / 24)
	if days < 0 {
		days = 0
	}
	if days <= 7 {
		if days == 0 {
			return "today"
		}
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
	return latest.In(time.Local).Format("01.02")
}

func (m model) detailLines(contentWidth int) []string {
	if m.focus == focusEpisodeDetail {
		return m.episodeDetailLines(contentWidth)
	}

	lines := []string{}

	p := m.currentPodcast()
	if p == nil {
		return []string{titleStyle.Render("Subscription Details"), "", subtleStyle.Render("No subscription selected")}
	}

	lines = append(lines, podcastHeroLines(m, contentWidth)...)
	lines = append(lines, "")
	lines = append(lines, sectionLabelStyle.Render("Episodes"))

	episodes := m.currentEpisodes()
	if len(episodes) == 0 {
		lines = append(lines, subtleStyle.Render("No episodes"))
		return trimTrailingBlank(lines)
	}

	for i, ep := range episodes {
		title := truncate(ep.Title, contentWidth)
		preview := clampTextLines(cleanText(ep.Summary), episodePreviewWidth(contentWidth), 2)
		date := ep.PubDate
		if date == "" {
			date = "Unknown date"
		}
		metaParts := []string{date}
		if dur := m.durationForMedia(ep.MediaURL); dur > 0 {
			metaParts = append(metaParts, formatDuration(dur))
		}
		meta := truncate(strings.Join(metaParts, "  •  "), contentWidth)
		lines = append(lines, strings.Split(renderListCard(title, preview, meta, contentWidth, i == m.selectedEpisode && m.focus == focusEpisodes), "\n")...)
	}

	return trimTrailingBlank(lines)
}

func (m model) episodeDetailLines(contentWidth int) []string {
	if m.currentPodcast() == nil {
		return []string{titleStyle.Render("Episode Details"), "", subtleStyle.Render("No subscription selected")}
	}

	ep := m.currentEpisode()
	if ep == nil {
		return []string{titleStyle.Render("Episode Details"), "", subtleStyle.Render("No episode selected")}
	}

	lines := episodeHeroLines(m, contentWidth)
	lines = append(lines, "")

	lines = append(lines, sectionLabelStyle.Render("Description"))
	description := formatDescriptionText(ep.DescriptionHTML)
	if description == "" {
		description = cleanText(ep.Description)
	}
	if description == "" {
		description = cleanText(ep.Summary)
	}
	lines = append(lines, wrapText(description, contentWidth)...)
	return trimTrailingBlank(lines)
}

func renderListCard(title string, description []string, meta string, contentWidth int, selected bool) string {
	titleLineStyle := listItemTitleStyle
	bodyLineStyle := listItemMetaStyle
	if selected {
		titleLineStyle = selectedListItemTitleStyle
		bodyLineStyle = selectedListItemMetaStyle
	}

	renderLine := func(style lipgloss.Style, text string) string {
		textWidth := maxInt(1, contentWidth-style.GetHorizontalFrameSize())
		return style.Width(contentWidth).Render(truncate(text, textWidth))
	}

	titleLine := renderLine(titleLineStyle, title)
	descriptionLines := make([]string, 0, len(description))
	for _, line := range description {
		descriptionLines = append(descriptionLines, renderLine(bodyLineStyle, line))
	}
	metaLine := renderLine(bodyLineStyle, meta)
	cardLines := append([]string{titleLine}, descriptionLines...)
	cardLines = append(cardLines, metaLine, "")
	return lipgloss.JoinVertical(lipgloss.Left, cardLines...)
}

func episodePreviewWidth(contentWidth int) int {
	frameWidth := maxInt(listItemMetaStyle.GetHorizontalFrameSize(), selectedListItemMetaStyle.GetHorizontalFrameSize())
	return maxInt(1, contentWidth-frameWidth)
}

func (m model) playerLines(contentWidth int) []string {
	if m.player == nil {
		if p, ep := m.restoredPlayerPaneEpisode(); ep != nil {
			lines := []string{titleStyle.Render(truncate(ep.Title, contentWidth))}
			metaParts := []string{}
			if p != nil && p.Title != "" {
				metaParts = append(metaParts, p.Title)
			}
			metaParts = append(metaParts, "Ready to resume")
			lines = append(lines, subtleStyle.Render(truncate(strings.Join(metaParts, "  •  "), contentWidth)))
			lines = append(lines, "")
			lines = append(lines, m.renderPlaybackProgress(contentWidth))
			lines = append(lines, subtleStyle.Render(truncate(m.playerPlaybackStatusText()+"  •  [space] resume  •  [p] play selected", contentWidth)))
			return lines
		}
		return []string{subtleStyle.Render(truncate("Idle  •  p plays the selected episode", contentWidth))}
	}

	state := "Playing"
	if m.player.paused {
		state = "Paused"
	}
	lines := []string{}
	lines = append(lines, titleStyle.Render(truncate(m.player.episode.Title, contentWidth)))
	metaParts := []string{}
	if m.player.podcastTitle != "" {
		metaParts = append(metaParts, m.player.podcastTitle)
	}
	metaParts = append(metaParts, state)
	lines = append(lines, subtleStyle.Render(truncate(strings.Join(metaParts, "  •  "), contentWidth)))
	lines = append(lines, "")
	lines = append(lines, m.renderPlaybackProgress(contentWidth))
	lines = append(lines, subtleStyle.Render(truncate(m.playerPlaybackStatusText()+"  •  [space] pause/resume  •  [p] play selected", contentWidth)))
	return lines
}

func (m model) playerPlaybackProgress() float64 {
	duration := m.playerDuration()
	if duration <= 0 {
		if m.player != nil {
			elapsed := playerElapsed(m.player)
			phase := (elapsed / time.Second) % 12
			return float64(phase) / 12
		}
		return 0
	}
	elapsed := m.playerElapsedDuration()
	progress := float64(elapsed) / float64(duration)
	if progress < 0 {
		return 0
	}
	if progress > 1 {
		return 1
	}
	return progress
}

func (m model) playerDuration() time.Duration {
	if m.player == nil {
		if _, ep := m.restoredPlayerPaneEpisode(); ep != nil {
			return m.durationForMedia(ep.MediaURL)
		}
		return 0
	}
	return m.durationForMedia(m.player.episode.MediaURL)
}

func (m model) playerElapsedDuration() time.Duration {
	if m.player != nil {
		return playerElapsed(m.player)
	}
	if _, ep := m.restoredPlayerPaneEpisode(); ep != nil {
		return m.resumeOffsetForEpisode(*ep)
	}
	return 0
}

func (m model) playerPlaybackStatusText() string {
	if m.player == nil {
		if _, ep := m.restoredPlayerPaneEpisode(); ep != nil {
			duration := m.playerDuration()
			elapsed := m.resumeOffsetForEpisode(*ep)
			if duration > 0 {
				return fmt.Sprintf("%s / %s", formatDuration(elapsed), formatDuration(duration))
			}
			if m.probingDurationFor == ep.MediaURL {
				return fmt.Sprintf("%s elapsed  •  probing duration...", formatDuration(elapsed))
			}
			return fmt.Sprintf("%s elapsed  •  duration unavailable", formatDuration(elapsed))
		}
		return "Press enter to start playback"
	}
	duration := m.playerDuration()
	elapsed := m.playerElapsedDuration()
	if duration > 0 {
		return fmt.Sprintf("%s / %s", formatDuration(elapsed), formatDuration(duration))
	}
	if m.probingDurationFor == m.player.episode.MediaURL {
		return fmt.Sprintf("%s elapsed  •  probing duration...", formatDuration(elapsed))
	}
	return fmt.Sprintf("%s elapsed  •  duration unavailable", formatDuration(elapsed))
}

func coverLines(m model, contentWidth int) []string {
	switch {
	case m.loadingCover || m.renderingCover:
		if m.coverANSI != "" {
			return strings.Split(m.coverANSI, "\n")
		}
		return placeholderCoverLines(m, "Loading cover art")
	case m.renderErr != nil:
		return wrapText("Cover: "+m.renderErr.Error(), contentWidth)
	case m.coverANSI != "":
		return strings.Split(m.coverANSI, "\n")
	default:
		return placeholderCoverLines(m, "No cover art")
	}
}

func placeholderCoverLines(m model, label string) []string {
	width, height := placeholderCoverSize(m)
	centerLine := height / 2
	label = truncate(label, width)
	fill := strings.Repeat("░", width)

	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		content := fill
		if row == centerLine {
			content = centerText(label, width)
		}
		lines = append(lines, subtleStyle.Render(content))
	}
	return lines
}

func placeholderCoverSize(m model) (int, int) {
	if m.coverANSI != "" {
		lines := strings.Split(m.coverANSI, "\n")
		return maxInt(1, maxRenderedLineWidth(lines)), maxInt(1, len(lines))
	}
	return maxInt(8, m.targetCoverWidth()), maxInt(4, m.targetCoverHeight())
}

func centerText(text string, width int) string {
	text = truncate(text, width)
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	left := (width - textWidth) / 2
	right := width - textWidth - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func podcastHeroLines(m model, contentWidth int) []string {
	p := m.currentPodcast()
	if p == nil {
		return nil
	}

	cover := coverLines(m, contentWidth)
	body := podcastHeroBodyLines(*p, contentWidth)

	coverWidth := maxRenderedLineWidth(cover)
	const gap = 2
	if contentWidth >= 64 && coverWidth > 0 {
		textWidth := contentWidth - coverWidth - gap
		if textWidth >= 24 {
			body = podcastHeroBodyLines(*p, textWidth)
			coverBlock := lipgloss.NewStyle().
				Width(coverWidth).
				MaxWidth(coverWidth).
				Render(strings.Join(cover, "\n"))
			textBlock := lipgloss.NewStyle().
				Width(textWidth).
				MaxWidth(textWidth).
				Render(strings.Join(body, "\n"))
			return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, coverBlock, strings.Repeat(" ", gap), textBlock), "\n")
		}
	}

	lines := append([]string{}, cover...)
	lines = append(lines, "")
	lines = append(lines, body...)
	return trimTrailingBlank(lines)
}

func episodeHeroLines(m model, contentWidth int) []string {
	ep := m.currentEpisode()
	if ep == nil {
		return nil
	}

	cover := coverLines(m, contentWidth)
	body := m.episodeHeroBodyLines(*ep, contentWidth)

	coverWidth := maxRenderedLineWidth(cover)
	const gap = 2
	if contentWidth >= 64 && coverWidth > 0 {
		textWidth := contentWidth - coverWidth - gap
		if textWidth >= 24 {
			body = m.episodeHeroBodyLines(*ep, textWidth)
			coverBlock := lipgloss.NewStyle().
				Width(coverWidth).
				MaxWidth(coverWidth).
				Render(strings.Join(cover, "\n"))
			textBlock := lipgloss.NewStyle().
				Width(textWidth).
				MaxWidth(textWidth).
				Render(strings.Join(body, "\n"))
			return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, coverBlock, strings.Repeat(" ", gap), textBlock), "\n")
		}
	}

	lines := append([]string{}, cover...)
	lines = append(lines, "")
	lines = append(lines, body...)
	return trimTrailingBlank(lines)
}

func (m model) episodeHeroBodyLines(ep episode, contentWidth int) []string {
	titleLines := wrapText(cleanText(ep.Title), contentWidth)
	lines := make([]string, 0, len(titleLines)+4)
	for _, line := range titleLines {
		lines = append(lines, titleStyle.Render(line))
	}
	metaParts := []string{}
	if p := m.currentPodcast(); p != nil && p.Title != "" {
		metaParts = append(metaParts, p.Title)
	}
	if ep.PubDate != "" {
		metaParts = append(metaParts, ep.PubDate)
	}
	if dur := m.durationForMedia(ep.MediaURL); dur > 0 {
		metaParts = append(metaParts, formatDuration(dur))
	}
	if len(metaParts) > 0 {
		lines = append(lines, subtleStyle.Render(truncate(strings.Join(metaParts, "  •  "), contentWidth)))
	}
	return trimTrailingBlank(lines)
}

func podcastHeroBodyLines(p podcast, contentWidth int) []string {
	titleLines := wrapText(cleanText(p.Title), contentWidth)
	lines := make([]string, 0, len(titleLines)+4)
	for _, line := range titleLines {
		lines = append(lines, titleStyle.Render(line))
	}
	lines = append(lines, "", sectionLabelStyle.Render("Description"))
	lines = append(lines, wrapText(cleanText(p.Description), contentWidth)...)
	if p.Link != "" {
		lines = append(lines, "", subtleStyle.Render("Link"))
		lines = append(lines, wrapText(p.Link, contentWidth)...)
	}
	return trimTrailingBlank(lines)
}

func wrapText(s string, width int) []string {
	if s == "" {
		return []string{subtleStyle.Render("No description")}
	}
	if width <= 8 {
		return []string{truncate(s, width)}
	}

	var lines []string
	for _, paragraph := range strings.Split(s, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			if len(lines) == 0 || lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}
		lines = append(lines, wrapParagraph(paragraph, width)...)
	}
	if len(lines) == 0 {
		return []string{s}
	}
	return lines
}

func wrapParagraph(s string, width int) []string {
	runes := []rune(s)
	var lines []string
	start := 0
	preferWhitespaceBreaks := !containsCJKRunes(runes)

	for start < len(runes) {
		lineWidth := 0
		lastBreak := -1
		end := start

		for end < len(runes) {
			r := runes[end]
			runeWidth := lipgloss.Width(string(r))
			if preferWhitespaceBreaks && unicode.IsSpace(r) {
				lastBreak = end
			}
			if lineWidth+runeWidth > width {
				break
			}
			lineWidth += runeWidth
			end++
		}

		if end == len(runes) {
			lines = append(lines, strings.TrimSpace(string(runes[start:end])))
			break
		}

		if lastBreak >= start {
			lines = append(lines, strings.TrimSpace(string(runes[start:lastBreak])))
			start = lastBreak + 1
			for start < len(runes) && unicode.IsSpace(runes[start]) {
				start++
			}
			continue
		}

		if end == start {
			end++
		}
		lines = append(lines, strings.TrimSpace(string(runes[start:end])))
		start = end
	}

	return lines
}

func containsCJKRunes(runes []rune) bool {
	for _, r := range runes {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

func clampTextLines(s string, width, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}

	lines := wrapText(s, width)
	if len(lines) <= maxLines {
		return lines
	}

	clamped := append([]string(nil), lines[:maxLines]...)
	last := strings.TrimRight(clamped[maxLines-1], " ")
	if lipgloss.Width(last) >= width && width > 1 {
		last = truncate(last, width-1)
	}
	clamped[maxLines-1] = last + "…"
	return clamped
}

func maxRenderedLineWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = maxInt(width, lipgloss.Width(line))
	}
	return width
}

func (m model) selectedDetailLine(contentWidth int) int {
	if m.focus == focusEpisodeDetail {
		return 0
	}
	if m.focus != focusEpisodes {
		return -1
	}
	p := m.currentPodcast()
	if p == nil {
		return -1
	}
	episodes := m.currentEpisodes()
	if len(episodes) == 0 || m.selectedEpisode < 0 || m.selectedEpisode >= len(episodes) {
		return -1
	}

	line := len(podcastHeroLines(m, contentWidth))
	line += 1
	line += 1
	return line + (m.selectedEpisode * episodeItemHeight)
}

func (m model) selectedListLine() int {
	if len(m.podcasts) == 0 {
		return -1
	}
	return m.selectedPodcast * subscriptionItemHeight
}

func (m *model) scrollEpisodeDetail(delta int) {
	if delta == 0 {
		return
	}

	maxOffset := m.maxEpisodeDetailOffset()
	m.detailScrollOffset = clampInt(m.detailScrollOffset+delta, 0, maxOffset)
}

func (m model) maxEpisodeDetailOffset() int {
	_, width := paneWidths(m.width)
	height := paneHeight(m.height)
	detailHeight, _ := m.rightPaneHeights(width, height)
	contentWidth, contentHeight := panelContentSize(activePanelStyle, width, detailHeight)
	lines := m.episodeDetailLines(contentWidth)
	return maxInt(0, len(lines)-contentHeight)
}

func (m *model) scrollEpisodeDetailToBottom() {
	m.detailScrollOffset = m.maxEpisodeDetailOffset()
}

func (m model) footerText() string {
	if m.focus == focusSubscriptions {
		return "j/k subscriptions • l focus episodes • enter open episodes • z zen • esc back/exit zen • space pause/resume • r refresh • ctrl+c quit"
	}
	if m.focus == focusEpisodeDetail {
		return "j/k episodes • p play selected • h back to list • z zen • esc back/exit zen • space pause/resume • r refresh • ctrl+c quit"
	}
	return "j/k episodes • l open details • enter/p play selected • h back to subscriptions • z zen • esc back/exit zen • space pause/resume • r refresh • ctrl+c quit"
}

func loadFeedsCmd(client *http.Client, feedURLs []string, feedCacheDir string) tea.Cmd {
	return func() tea.Msg {
		podcasts, err := loadFeeds(client, feedURLs, feedCacheDir)
		return feedLoadedMsg{podcasts: podcasts, err: err}
	}
}

func fetchCoverCmd(requestID int, client *http.Client, cacheDir, imageURL string) tea.Cmd {
	return func() tea.Msg {
		path, err := fetchCover(client, imageURL, cacheDir)
		return coverReadyMsg{requestID: requestID, imageURL: imageURL, path: path, err: err}
	}
}

func renderCoverCmd(requestID int, imageURL, path, cacheDir string, width, height int) tea.Cmd {
	return func() tea.Msg {
		ansi, err := renderCover(path, cacheDir, width, height)
		return renderResultMsg{requestID: requestID, imageURL: imageURL, path: path, width: width, height: height, ansi: ansi, err: err}
	}
}

func playbackTickCmd(playerID int) tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return playbackTickMsg{playerID: playerID, at: t}
	})
}

func probeDurationCmd(mediaURL string) tea.Cmd {
	return func() tea.Msg {
		duration, err := probeDuration(mediaURL)
		return durationLoadedMsg{mediaURL: mediaURL, duration: duration, err: err}
	}
}

func waitForPlaybackCmd(player *audioPlayer) tea.Cmd {
	return func() tea.Msg {
		err := player.cmd.Wait()
		stderr := ""
		if player.stderr != nil {
			stderr = strings.TrimSpace(player.stderr.String())
		}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == -1 {
				return playbackFinishedMsg{playerID: player.id, stderr: stderr}
			}
			return playbackFinishedMsg{playerID: player.id, err: err, stderr: stderr}
		}
		return playbackFinishedMsg{playerID: player.id, stderr: stderr}
	}
}

func coverWidth(termWidth int) int {
	_, detailWidth := paneWidths(termWidth)
	if detailWidth <= 0 {
		return 28
	}
	return clampInt(detailWidth-8, 18, 42)
}

func coverHeight(termHeight int) int {
	if termHeight <= 0 {
		return 14
	}
	return clampInt(termHeight/3, 10, 18)
}

func zenCoverWidth(termWidth int) int {
	if termWidth <= 0 {
		return 42
	}
	return clampInt(termWidth/2, 28, 72)
}

func zenCoverHeight(termHeight int) int {
	if termHeight <= 0 {
		return 20
	}
	return clampInt((termHeight*3)/5, 14, 30)
}

func (m model) targetCoverWidth() int {
	if m.zenMode {
		return zenCoverWidth(m.width)
	}
	return coverWidth(m.width)
}

func (m model) targetCoverHeight() int {
	if m.zenMode {
		return zenCoverHeight(m.height)
	}
	return coverHeight(m.height)
}

func paneHeight(termHeight int) int {
	return maxInt(18, termHeight-4)
}

func (m model) rightPaneHeights(width, totalHeight int) (int, int) {
	const minDetailHeight = 10
	const minPlayerHeight = 3
	const maxPlayerHeight = 8

	contentWidth := maxInt(1, width-playerPanelStyle.GetHorizontalFrameSize())
	playerLines := len(m.playerLines(contentWidth))
	playerHeight := playerLines + playerPanelStyle.GetVerticalFrameSize()
	playerHeight = clampInt(playerHeight, minPlayerHeight, maxPlayerHeight)

	maxPlayerAllowed := maxInt(minPlayerHeight, totalHeight-minDetailHeight)
	playerHeight = minInt(playerHeight, maxPlayerAllowed)

	detailHeight := maxInt(minDetailHeight, totalHeight-playerHeight)
	if detailHeight+playerHeight < totalHeight {
		detailHeight += totalHeight - (detailHeight + playerHeight)
	}
	return detailHeight, playerHeight
}

func panelContentSize(style lipgloss.Style, outerWidth, outerHeight int) (int, int) {
	return maxInt(1, outerWidth-style.GetHorizontalFrameSize()), maxInt(1, outerHeight-style.GetVerticalFrameSize())
}

func paneWidths(termWidth int) (int, int) {
	if termWidth <= 0 {
		return 32, 56
	}

	innerWidth := maxInt(36, termWidth-4)
	left := clampInt(innerWidth/3, 24, 40)
	right := maxInt(20, innerWidth-left)
	if left+right < innerWidth {
		right += innerWidth - (left + right)
	}
	return left, right
}

func visibleLines(lines []string, availableHeight int, selected int) []string {
	if availableHeight <= 0 {
		return nil
	}
	if len(lines) <= availableHeight {
		return lines
	}

	offset := 0
	if selected >= 0 {
		offset = scrollOffset(len(lines), availableHeight, selected)
	}

	return visibleLinesAtOffset(lines, availableHeight, offset)
}

func visibleLinesAtOffset(lines []string, availableHeight, offset int) []string {
	if availableHeight <= 0 {
		return nil
	}
	if len(lines) <= availableHeight {
		return lines
	}
	if offset < 0 {
		offset = 0
	}
	maxOffset := maxInt(0, len(lines)-availableHeight)
	if offset > maxOffset {
		offset = maxOffset
	}

	end := minInt(len(lines), offset+availableHeight)
	window := append([]string(nil), lines[offset:end]...)
	if offset > 0 && len(window) > 0 {
		window[0] = subtleStyle.Render("...")
	}
	if end < len(lines) && len(window) > 0 {
		window[len(window)-1] = subtleStyle.Render("...")
	}
	return window
}

func scrollOffset(total, height, selected int) int {
	if total <= height || selected < 0 {
		return 0
	}

	offset := selected - height/2
	if offset < 0 {
		offset = 0
	}
	maxOffset := total - height
	if offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func listViewportOffset(totalLines, visibleHeight, selectedLine, itemHeight int) int {
	if selectedLine < 0 {
		return 0
	}
	visibleHeight = maxInt(1, visibleHeight)
	if totalLines <= visibleHeight {
		return 0
	}
	itemHeight = maxInt(1, itemHeight)

	itemBottom := selectedLine + itemHeight - 1
	offset := selectedLine - visibleHeight/2
	if offset < 0 {
		offset = 0
	}
	if itemBottom >= offset+visibleHeight {
		offset = itemBottom - visibleHeight + 1
	}

	maxOffset := maxInt(0, totalLines-visibleHeight)
	if offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (m *model) syncPlaybackProgress() tea.Cmd {
	return m.playbackProgress.SetPercent(m.playerPlaybackProgress())
}

func (m model) renderPlaybackProgress(width int) string {
	if width <= 0 {
		return ""
	}
	bar := m.playbackProgress
	bar.SetWidth(width)
	return bar.View()
}
