package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	syncDescription           = "podcast-player-cli sync data"
	syncConfigFilename        = "config.json"
	syncPlayStateFilename     = "play-state.json"
	syncManifestFilename      = "manifest.json"
	syncManifestSchemaVersion = 1
	syncAppID                 = "podcast-player-cli"
	emptySyncPlayStateJSON    = "{}\n"
)

type syncConfig struct {
	GistID string `json:"gist_id,omitempty"`
}

type syncState struct {
	LastRemoteUpdatedAt string    `json:"last_remote_updated_at,omitempty"`
	LastConfigHash      string    `json:"last_config_hash,omitempty"`
	LastPlayStateHash   string    `json:"last_play_state_hash,omitempty"`
	LastSyncDirection   string    `json:"last_sync_direction,omitempty"`
	LastSyncAt          time.Time `json:"last_sync_at,omitempty"`
}

type syncManifest struct {
	SchemaVersion int       `json:"schema_version"`
	AppID         string    `json:"app_id"`
	LastWriterAt  time.Time `json:"last_writer_at"`
	DeviceName    string    `json:"device_name,omitempty"`
}

type gistFile struct {
	Filename  string `json:"filename"`
	Type      string `json:"type,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content,omitempty"`
}

type gistResponse struct {
	ID        string              `json:"id"`
	UpdatedAt time.Time           `json:"updated_at"`
	Files     map[string]gistFile `json:"files"`
}

type gistWriteFile struct {
	Content string `json:"content"`
}

type gistCreateRequest struct {
	Description string                   `json:"description"`
	Public      bool                     `json:"public"`
	Files       map[string]gistWriteFile `json:"files"`
}

type gistUpdateRequest struct {
	Description string                   `json:"description,omitempty"`
	Files       map[string]gistWriteFile `json:"files"`
}

func loadSyncState(path string) (syncState, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return syncState{}, nil
	} else if err != nil {
		return syncState{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return syncState{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return syncState{}, nil
	}

	var state syncState
	if err := json.Unmarshal(data, &state); err != nil {
		return syncState{}, fmt.Errorf("parse sync state %s: %w", path, err)
	}
	return state, nil
}

func saveSyncState(path string, state syncState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func configHash(cfg appConfig) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func playStateHashFromFile(path string) (string, error) {
	data, err := syncPlayStateContent(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func syncPlayStateContent(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte(emptySyncPlayStateJSON), nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte(emptySyncPlayStateJSON), nil
	}
	state, err := parsePlayState(data)
	if err != nil {
		return nil, fmt.Errorf("parse play state %s: %w", path, err)
	}
	if state == nil {
		return []byte(emptySyncPlayStateJSON), nil
	}
	normalized, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(normalized, '\n'), nil
}

func syncManifestContent() ([]byte, error) {
	deviceName, err := os.Hostname()
	if err != nil {
		deviceName = ""
	}
	manifest := syncManifest{
		SchemaVersion: syncManifestSchemaVersion,
		AppID:         syncAppID,
		LastWriterAt:  time.Now().UTC(),
		DeviceName:    strings.TrimSpace(deviceName),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func parseSyncManifest(data []byte) (syncManifest, error) {
	var manifest syncManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return syncManifest{}, err
	}
	if manifest.SchemaVersion != syncManifestSchemaVersion {
		return syncManifest{}, fmt.Errorf("unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.AppID) != syncAppID {
		return syncManifest{}, fmt.Errorf("unexpected manifest app id %q", manifest.AppID)
	}
	return manifest, nil
}

func buildSyncGistFiles(cfg appConfig, playStatePath string) (map[string]gistWriteFile, error) {
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	playStateData, err := syncPlayStateContent(playStatePath)
	if err != nil {
		return nil, err
	}
	manifestData, err := syncManifestContent()
	if err != nil {
		return nil, err
	}
	return map[string]gistWriteFile{
		syncConfigFilename:    {Content: string(append(cfgData, '\n'))},
		syncPlayStateFilename: {Content: string(playStateData)},
		syncManifestFilename:  {Content: string(manifestData)},
	}, nil
}

func parseGistTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

func pullShouldWarn(syncMeta syncState, cfg appConfig, playStatePath string) bool {
	if syncMeta.LastSyncAt.IsZero() {
		return false
	}
	cfgHash, err := configHash(cfg)
	if err != nil {
		return false
	}
	playHash, err := playStateHashFromFile(playStatePath)
	if err != nil {
		return false
	}
	return syncMeta.LastConfigHash != "" && syncMeta.LastConfigHash != cfgHash ||
		syncMeta.LastPlayStateHash != "" && syncMeta.LastPlayStateHash != playHash
}

func pushShouldWarn(syncMeta syncState, remoteUpdatedAt time.Time) bool {
	if syncMeta.LastRemoteUpdatedAt == "" || remoteUpdatedAt.IsZero() {
		return false
	}
	lastSeen, err := parseGistTimestamp(syncMeta.LastRemoteUpdatedAt)
	if err != nil {
		return false
	}
	return remoteUpdatedAt.After(lastSeen)
}

func updateSyncStateAfterSync(path string, direction string, remoteUpdatedAt time.Time, cfg appConfig, playStatePath string) error {
	cfgHash, err := configHash(cfg)
	if err != nil {
		return err
	}
	playHash, err := playStateHashFromFile(playStatePath)
	if err != nil {
		return err
	}
	return saveSyncState(path, syncState{
		LastRemoteUpdatedAt: remoteUpdatedAt.UTC().Format(time.RFC3339),
		LastConfigHash:      cfgHash,
		LastPlayStateHash:   playHash,
		LastSyncDirection:   direction,
		LastSyncAt:          time.Now().UTC(),
	})
}

func resolveSyncGistID(cfg appConfig, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		return override
	}
	return strings.TrimSpace(cfg.Sync.GistID)
}

func ensureSyncConfig(cfg appConfig, gistID string) appConfig {
	cfg.Sync.GistID = strings.TrimSpace(gistID)
	return cfg
}

func applyPulledFiles(paths appPaths, gistID string, gist gistResponse) (appConfig, error) {
	configFile, ok := gist.Files[syncConfigFilename]
	if !ok {
		return appConfig{}, fmt.Errorf("gist %s is missing %s", gist.ID, syncConfigFilename)
	}
	manifestFile, ok := gist.Files[syncManifestFilename]
	if !ok {
		return appConfig{}, fmt.Errorf("gist %s is missing %s", gist.ID, syncManifestFilename)
	}
	if configFile.Truncated || manifestFile.Truncated {
		return appConfig{}, fmt.Errorf("gist %s contains truncated sync files", gist.ID)
	}
	if _, err := parseSyncManifest([]byte(manifestFile.Content)); err != nil {
		return appConfig{}, fmt.Errorf("invalid %s: %w", syncManifestFilename, err)
	}
	cfg, err := parseAppConfig([]byte(configFile.Content))
	if err != nil {
		return appConfig{}, fmt.Errorf("invalid %s: %w", syncConfigFilename, err)
	}
	cfg = ensureSyncConfig(cfg, gistID)
	if err := saveConfig(paths.ConfigPath, cfg); err != nil {
		return appConfig{}, err
	}

	playStateFile, ok := gist.Files[syncPlayStateFilename]
	if !ok || strings.TrimSpace(playStateFile.Content) == "" || strings.TrimSpace(playStateFile.Content) == "{}" {
		if err := clearPlayState(paths.PlayStatePath); err != nil {
			return appConfig{}, err
		}
		return cfg, nil
	}
	if playStateFile.Truncated {
		return appConfig{}, fmt.Errorf("gist %s contains truncated %s", gist.ID, syncPlayStateFilename)
	}
	state, err := parsePlayState([]byte(playStateFile.Content))
	if err != nil {
		return appConfig{}, fmt.Errorf("invalid %s: %w", syncPlayStateFilename, err)
	}
	if state == nil {
		if err := clearPlayState(paths.PlayStatePath); err != nil {
			return appConfig{}, err
		}
		return cfg, nil
	}
	if err := savePlayState(paths.PlayStatePath, *state); err != nil {
		return appConfig{}, err
	}
	return cfg, nil
}

func ghAuthToken() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("failed to get GitHub auth token via gh: %s", message)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("gh returned an empty auth token")
	}
	return token, nil
}

func githubAPIRequest(client *http.Client, token, method, url string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

func createGist(client *http.Client, token string, reqBody gistCreateRequest) (gistResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return gistResponse{}, err
	}
	resp, err := githubAPIRequest(client, token, http.MethodPost, "https://api.github.com/gists", body)
	if err != nil {
		return gistResponse{}, err
	}
	defer resp.Body.Close()
	return decodeGistResponse(resp)
}

func getGist(client *http.Client, token, gistID string) (gistResponse, error) {
	resp, err := githubAPIRequest(client, token, http.MethodGet, "https://api.github.com/gists/"+gistID, nil)
	if err != nil {
		return gistResponse{}, err
	}
	defer resp.Body.Close()
	return decodeGistResponse(resp)
}

func updateGist(client *http.Client, token, gistID string, reqBody gistUpdateRequest) (gistResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return gistResponse{}, err
	}
	resp, err := githubAPIRequest(client, token, http.MethodPatch, "https://api.github.com/gists/"+gistID, body)
	if err != nil {
		return gistResponse{}, err
	}
	defer resp.Body.Close()
	return decodeGistResponse(resp)
}

func decodeGistResponse(resp *http.Response) (gistResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return gistResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return gistResponse{}, fmt.Errorf("github gist API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var gist gistResponse
	if err := json.Unmarshal(body, &gist); err != nil {
		return gistResponse{}, err
	}
	if gist.ID == "" {
		return gistResponse{}, errors.New("github gist API response did not include a gist id")
	}
	return gist, nil
}

func runSyncCommand(paths appPaths, cfg appConfig, proxyURL string, args []string) error {
	if len(args) == 0 {
		return errors.New("expected a sync subcommand: init, pull, or push")
	}

	client, err := newHTTPClient(proxyURL)
	if err != nil {
		return fmt.Errorf("invalid proxy: %w", err)
	}
	token, err := ghAuthToken()
	if err != nil {
		return fmt.Errorf("%w; run `gh auth login` first", err)
	}

	switch args[0] {
	case "init":
		return runSyncInit(paths, cfg, client, token)
	case "pull":
		return runSyncPull(paths, cfg, client, token, args[1:])
	case "push":
		return runSyncPush(paths, cfg, client, token, args[1:])
	default:
		return fmt.Errorf("unknown sync subcommand %q", args[0])
	}
}

func runSyncInit(paths appPaths, cfg appConfig, client *http.Client, token string) error {
	files, err := buildSyncGistFiles(cfg, paths.PlayStatePath)
	if err != nil {
		return err
	}
	created, err := createGist(client, token, gistCreateRequest{
		Description: syncDescription,
		Public:      false,
		Files:       files,
	})
	if err != nil {
		return err
	}

	cfg = ensureSyncConfig(cfg, created.ID)
	if err := saveConfig(paths.ConfigPath, cfg); err != nil {
		return err
	}

	files, err = buildSyncGistFiles(cfg, paths.PlayStatePath)
	if err != nil {
		return err
	}
	updated, err := updateGist(client, token, created.ID, gistUpdateRequest{Files: files})
	if err != nil {
		return err
	}
	if err := updateSyncStateAfterSync(paths.SyncStatePath, "push", updated.UpdatedAt, cfg, paths.PlayStatePath); err != nil {
		return err
	}

	fmt.Printf("Initialized secret sync gist %s\n", updated.ID)
	return nil
}

func runSyncPull(paths appPaths, cfg appConfig, client *http.Client, token string, args []string) error {
	fs := flag.NewFlagSet("sync pull", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	gistIDFlag := fs.String("gist", "", "gist ID to pull from")
	if err := fs.Parse(args); err != nil {
		return err
	}

	gistID := resolveSyncGistID(cfg, *gistIDFlag)
	if gistID == "" {
		return errors.New("no gist configured; run `podcast-player-cli sync init` or pass `--gist <id>`")
	}

	syncMeta, err := loadSyncState(paths.SyncStatePath)
	if err != nil {
		return err
	}
	if pullShouldWarn(syncMeta, cfg, paths.PlayStatePath) {
		fmt.Fprintln(os.Stderr, "warning: local config or play state changed since the last sync; pull will overwrite local files")
	}

	gist, err := getGist(client, token, gistID)
	if err != nil {
		return err
	}
	cfg, err = applyPulledFiles(paths, gistID, gist)
	if err != nil {
		return err
	}
	if err := updateSyncStateAfterSync(paths.SyncStatePath, "pull", gist.UpdatedAt, cfg, paths.PlayStatePath); err != nil {
		return err
	}

	fmt.Printf("Pulled sync state from gist %s\n", gistID)
	return nil
}

func runSyncPush(paths appPaths, cfg appConfig, client *http.Client, token string, args []string) error {
	fs := flag.NewFlagSet("sync push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	gistIDFlag := fs.String("gist", "", "gist ID to push to")
	if err := fs.Parse(args); err != nil {
		return err
	}

	gistID := resolveSyncGistID(cfg, *gistIDFlag)
	if gistID == "" {
		return errors.New("no gist configured; run `podcast-player-cli sync init` or pass `--gist <id>`")
	}
	cfg = ensureSyncConfig(cfg, gistID)
	if err := saveConfig(paths.ConfigPath, cfg); err != nil {
		return err
	}

	syncMeta, err := loadSyncState(paths.SyncStatePath)
	if err != nil {
		return err
	}
	remote, err := getGist(client, token, gistID)
	if err != nil {
		return err
	}
	if pushShouldWarn(syncMeta, remote.UpdatedAt) {
		fmt.Fprintln(os.Stderr, "warning: remote gist changed since the last sync; push will overwrite remote files")
	}

	files, err := buildSyncGistFiles(cfg, paths.PlayStatePath)
	if err != nil {
		return err
	}
	updated, err := updateGist(client, token, gistID, gistUpdateRequest{Files: files})
	if err != nil {
		return err
	}
	if err := updateSyncStateAfterSync(paths.SyncStatePath, "push", updated.UpdatedAt, cfg, paths.PlayStatePath); err != nil {
		return err
	}

	fmt.Printf("Pushed sync state to gist %s\n", gistID)
	return nil
}
