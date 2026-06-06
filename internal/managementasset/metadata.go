package managementasset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const PanelMetadataFileName = "panel-meta.json"

var defaultPanelDirCandidates = []string{
	"/home/web/html/relay-panel",
	"/home/web/html/cliproxy-panel",
}

// PanelMetadata describes the management panel currently present on disk.
// It lets update checks compare the actual served UI instead of stale binary build info.
type PanelMetadata struct {
	Version    string `json:"version"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
	Repository string `json:"repository"`
	BuildDate  string `json:"build_date"`
}

// ResolvePanelDir returns the directory containing the SPA panel (manage.html + assets/).
func ResolvePanelDir(configFilePath string) string {
	if override := strings.TrimSpace(os.Getenv("MANAGEMENT_PANEL_DIR")); override != "" {
		if info, err := os.Stat(override); err == nil && info.IsDir() {
			return override
		}
	}

	candidates := append([]string(nil), defaultPanelDirCandidates...)
	if staticDir := StaticDir(configFilePath); staticDir != "" {
		candidates = append(candidates, staticDir)
	}

	for _, dir := range candidates {
		manageHTML := filepath.Join(dir, "manage.html")
		if _, err := os.Stat(manageHTML); err == nil {
			return dir
		}
	}
	return ""
}

func ReadPanelMetadata(panelDir string) (PanelMetadata, bool) {
	panelDir = strings.TrimSpace(panelDir)
	if panelDir == "" {
		return PanelMetadata{}, false
	}

	data, err := os.ReadFile(filepath.Join(panelDir, PanelMetadataFileName))
	if err != nil {
		return PanelMetadata{}, false
	}

	var meta PanelMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return PanelMetadata{}, false
	}
	meta.Version = strings.TrimSpace(meta.Version)
	meta.Ref = strings.TrimSpace(meta.Ref)
	meta.Commit = strings.TrimSpace(meta.Commit)
	meta.Repository = strings.TrimSpace(meta.Repository)
	meta.BuildDate = strings.TrimSpace(meta.BuildDate)
	return meta, meta.Version != "" || meta.Commit != ""
}

func CurrentPanelMetadata(configFilePath string) (PanelMetadata, bool) {
	return ReadPanelMetadata(ResolvePanelDir(configFilePath))
}
