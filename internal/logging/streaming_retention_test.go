package logging

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamingCloseEnforcesRequestFileCap(t *testing.T) {
	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 10)
	// Every file is produced by the real asynchronous stream writer and Close;
	// no fabricated log files or background size-cleaner timing is involved.
	for i := 0; i <= defaultRequestLogsMaxFiles; i++ {
		writer, err := logger.LogStreamingRequest("/v1/responses", http.MethodPost, nil, []byte(`{"model":"demo","stream":true}`), fmt.Sprintf("repro-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteStatus(http.StatusOK, nil); err != nil {
			t.Fatal(err)
		}
		writer.WriteChunkAsync([]byte("data: [DONE]\n\n"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(logsDir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	streamCount := len(files)
	t.Logf("after %d real streaming Close calls: log_files=%d cap=%d", defaultRequestLogsMaxFiles+1, streamCount, defaultRequestLogsMaxFiles)

	// The non-streaming control proves the same logger's retention helper can
	// remove these files; only the stream completion lifecycle misses it.
	if err := logger.LogRequest("/v1/responses", http.MethodPost, nil, nil, http.StatusOK, nil, []byte(`{}`), nil, nil, nil, "nonstream-control", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	files, err = filepath.Glob(filepath.Join(logsDir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after one non-streaming control: log_files=%d", len(files))
	if len(files) != defaultRequestLogsMaxFiles {
		t.Fatalf("control did not enforce the existing cap: got %d", len(files))
	}
	if streamCount > defaultRequestLogsMaxFiles {
		t.Fatalf("streaming completion retained %d request logs, want <= %d", streamCount, defaultRequestLogsMaxFiles)
	}
}
