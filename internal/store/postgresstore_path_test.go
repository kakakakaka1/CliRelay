package store

import (
	"path/filepath"
	"testing"
)

func TestPostgresStoreResolveDeletePathStaysInsideAuthDir(t *testing.T) {
	authDir := t.TempDir()
	store := &PostgresStore{authDir: authDir}

	got, err := store.resolveDeletePath("tenant-a/credential.json")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(authDir, "tenant-a", "credential.json")
	if got != want {
		t.Fatalf("resolveDeletePath() = %q, want %q", got, want)
	}

	if _, err = store.resolveDeletePath(filepath.Join(authDir, "..", "outside.json")); err == nil {
		t.Fatal("expected path outside auth directory to be rejected")
	}
	if _, err = store.resolveDeletePath("../outside.json"); err == nil {
		t.Fatal("expected relative traversal to be rejected")
	}
}
