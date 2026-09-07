package watcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScheduleAuthFileUpdateDebounces(t *testing.T) {
	tmp := t.TempDir()
	authFile := filepath.Join(tmp, "antigravity-user.json")
	if err := os.WriteFile(authFile, []byte(`{"type":"antigravity","access_token":"t1"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	w := &Watcher{lastAuthHashes: make(map[string]string)}
	w.scheduleAuthFileUpdate(authFile)
	w.scheduleAuthFileUpdate(authFile)

	w.authReloadMu.Lock()
	pending := len(w.pendingAuthPaths)
	hasTimer := w.authReloadTimer != nil
	w.authReloadMu.Unlock()
	if pending != 1 {
		t.Fatalf("pending paths = %d, want 1 after coalesced schedules", pending)
	}
	if !hasTimer {
		t.Fatal("expected debounce timer to be armed")
	}

	w.flushPendingAuthUpdates()
	w.authReloadMu.Lock()
	left := len(w.pendingAuthPaths)
	timer := w.authReloadTimer
	w.authReloadMu.Unlock()
	if left != 0 {
		t.Fatalf("pending after flush = %d, want 0", left)
	}
	if timer != nil {
		t.Fatal("expected timer cleared after flush")
	}
	w.stopAuthReloadTimer()
}
