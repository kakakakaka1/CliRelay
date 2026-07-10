package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const updaterEventHeartbeat = 15 * time.Second

func resolveUpdaterStateFile(explicit string) string {
	return strings.TrimSpace(explicit)
}

func (s *updaterServer) restoreStatus() {
	if s == nil || strings.TrimSpace(s.stateFile) == "" {
		return
	}
	data, err := os.ReadFile(s.stateFile)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("clirelay updater: read state failed: %v", err)
		return
	}
	var status updateStatusResponse
	if err := json.Unmarshal(data, &status); err != nil {
		log.Printf("clirelay updater: decode state failed: %v", err)
		return
	}
	if strings.TrimSpace(status.Status) == "" || strings.TrimSpace(status.Stage) == "" {
		return
	}
	s.status = cloneUpdateStatus(status)
	s.runID = status.RunID
	if strings.EqualFold(status.Status, "running") {
		now := time.Now().UTC().Format(time.RFC3339)
		s.status.Status = "failed"
		s.status.Stage = "failed"
		s.status.MessageCode = "updater_restarted"
		s.status.Message = "updater restarted before the update completed"
		s.status.UpdatedAt = now
		s.status.FinishedAt = now
		s.statusChangedLocked()
	}
}

func (s *updaterServer) persistStatusLocked() {
	if s == nil || strings.TrimSpace(s.stateFile) == "" {
		return
	}
	data, err := json.MarshalIndent(s.status, "", "  ")
	if err != nil {
		log.Printf("clirelay updater: encode state failed: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.stateFile), 0o755); err != nil {
		log.Printf("clirelay updater: create state directory failed: %v", err)
		return
	}
	tmp := s.stateFile + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		log.Printf("clirelay updater: write state failed: %v", err)
		return
	}
	if err := os.Rename(tmp, s.stateFile); err != nil {
		_ = os.Remove(tmp)
		log.Printf("clirelay updater: replace state failed: %v", err)
	}
}

func cloneUpdateStatus(status updateStatusResponse) updateStatusResponse {
	clone := status
	if len(status.Logs) > 0 {
		clone.Logs = append([]updateLogEntry(nil), status.Logs...)
	}
	return clone
}

func (s *updaterServer) statusChangedLocked() {
	s.publishStatusLocked(true)
}

func (s *updaterServer) statusObservedLocked() {
	s.publishStatusLocked(false)
}

func (s *updaterServer) publishStatusLocked(persist bool) {
	s.status.EventID++
	if persist {
		s.persistStatusLocked()
	}
	snapshot := cloneUpdateStatus(s.status)
	for subscriber := range s.subscribers {
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- snapshot:
			default:
			}
		}
	}
}

func (s *updaterServer) subscribeStatus() (<-chan updateStatusResponse, updateStatusResponse, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan updateStatusResponse, 1)
	s.subscribers[ch] = struct{}{}
	initial := cloneUpdateStatus(s.status)
	return ch, initial, func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
	}
}

func (s *updaterServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	events, initial, unsubscribe := s.subscribeStatus()
	defer unsubscribe()
	if err := writeUpdateEvent(w, initial); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(updaterEventHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case status := <-events:
			if err := writeUpdateEvent(w, status); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeUpdateEvent(w http.ResponseWriter, status updateStatusResponse) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if status.EventID > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", status.EventID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, "event: update\n"); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}
