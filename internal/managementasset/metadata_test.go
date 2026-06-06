package managementasset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePanelDirRecognizesKnownDeploymentDir(t *testing.T) {
	primary := t.TempDir()
	fallback := t.TempDir()

	originalCandidates := defaultPanelDirCandidates
	defaultPanelDirCandidates = []string{primary, fallback}
	t.Cleanup(func() {
		defaultPanelDirCandidates = originalCandidates
	})

	if err := os.WriteFile(filepath.Join(fallback, "manage.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write manage.html: %v", err)
	}

	if got := ResolvePanelDir(""); got != fallback {
		t.Fatalf("ResolvePanelDir() = %q, want %q", got, fallback)
	}
}

func TestCurrentPanelMetadataReadsResolvedPanelDir(t *testing.T) {
	panelDir := t.TempDir()

	originalCandidates := defaultPanelDirCandidates
	defaultPanelDirCandidates = []string{panelDir}
	t.Cleanup(func() {
		defaultPanelDirCandidates = originalCandidates
	})

	if err := os.WriteFile(filepath.Join(panelDir, "manage.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write manage.html: %v", err)
	}

	want := PanelMetadata{
		Version:    "panel-dev-e559f2c",
		Ref:        "dev",
		Commit:     "e559f2c444927cd0225b65b1d71a0ed23f5098dc",
		Repository: "https://github.com/kittors/codeProxy",
		BuildDate:  "2026-06-06T11:15:01Z",
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(panelDir, PanelMetadataFileName), data, 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	got, ok := CurrentPanelMetadata("")
	if !ok {
		t.Fatal("CurrentPanelMetadata() ok = false, want true")
	}
	if got != want {
		t.Fatalf("CurrentPanelMetadata() = %+v, want %+v", got, want)
	}
}
