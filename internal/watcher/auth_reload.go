// auth_reload.go debounces noisy auth-file Write/Create events so rapid
// token-refresh / probe-marker writes coalesce into one incremental reload.
package watcher

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

const authReloadDebounce = 250 * time.Millisecond

func (w *Watcher) scheduleAuthFileUpdate(path string) {
	if w == nil {
		return
	}
	normalized := w.normalizeAuthPath(path)
	if normalized == "" {
		return
	}

	w.authReloadMu.Lock()
	defer w.authReloadMu.Unlock()
	if w.pendingAuthPaths == nil {
		w.pendingAuthPaths = make(map[string]string)
	}
	w.pendingAuthPaths[normalized] = path
	if w.authReloadTimer != nil {
		// Keep the first event deadline so writes from other accounts cannot starve reloads.
		return
	}
	w.authReloadTimer = time.AfterFunc(authReloadDebounce, func() {
		w.flushPendingAuthUpdates()
	})
}

func (w *Watcher) flushPendingAuthUpdates() {
	w.authReloadMu.Lock()
	pending := w.pendingAuthPaths
	w.pendingAuthPaths = nil
	w.authReloadTimer = nil
	w.authReloadMu.Unlock()

	for _, path := range pending {
		if unchanged, errSame := w.authFileUnchanged(path); errSame == nil && unchanged {
			log.Debugf("auth file unchanged (hash match), skipping reload: %s", filepath.Base(path))
			continue
		}
		log.Infof("auth file changed (%s): %s, processing incrementally", fsnotify.Write.String(), filepath.Base(path))
		w.addOrUpdateClient(path)
	}
}

func (w *Watcher) stopAuthReloadTimer() {
	if w == nil {
		return
	}
	w.authReloadMu.Lock()
	if w.authReloadTimer != nil {
		w.authReloadTimer.Stop()
		w.authReloadTimer = nil
	}
	w.pendingAuthPaths = nil
	w.authReloadMu.Unlock()
}
