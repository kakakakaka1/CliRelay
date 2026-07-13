package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountJSONAuthFilesCountsNonEmptyJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"id":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.json"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "tenant")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.json"), []byte(`{"id":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	count, err := CountJSONAuthFiles(dir)
	if err != nil {
		t.Fatalf("CountJSONAuthFiles: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestCountJSONAuthFilesMissingDir(t *testing.T) {
	count, err := CountJSONAuthFiles(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("CountJSONAuthFiles missing: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestLegacyDockerAuthDirs(t *testing.T) {
	dirs := LegacyDockerAuthDirs()
	if len(dirs) < 2 {
		t.Fatalf("expected at least two legacy paths, got %#v", dirs)
	}
}
