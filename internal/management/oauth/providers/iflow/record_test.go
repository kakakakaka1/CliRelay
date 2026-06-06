package iflow

import (
	"testing"
	"time"

	internaliflow "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/iflow"
)

func TestRecordFromTokenStorageBuildsEmailRecord(t *testing.T) {
	storage := &internaliflow.IFlowTokenStorage{
		Email:  " user@example.com ",
		APIKey: "api-key",
	}

	record := RecordFromTokenStorage(storage, time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC))
	if record == nil {
		t.Fatal("RecordFromTokenStorage() = nil")
	}
	if record.ID != "iflow-user@example.com.json" || record.FileName != "iflow-user@example.com.json" {
		t.Fatalf("ID/FileName = %q/%q, want iflow-user@example.com.json", record.ID, record.FileName)
	}
	if record.Provider != "iflow" || record.Storage != storage {
		t.Fatalf("provider/storage = %q/%#v, want iflow/original storage", record.Provider, record.Storage)
	}
	if got, _ := record.Metadata["email"].(string); got != "user@example.com" {
		t.Fatalf("metadata[email] = %q, want user@example.com", got)
	}
	if got, _ := record.Metadata["api_key"].(string); got != "api-key" {
		t.Fatalf("metadata[api_key] = %q, want api-key", got)
	}
	if got := record.Attributes["api_key"]; got != "api-key" {
		t.Fatalf("attributes[api_key] = %q, want api-key", got)
	}
}

func TestRecordFromTokenStorageUsesTimestampFallback(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 30, 0, 123000000, time.UTC)
	storage := &internaliflow.IFlowTokenStorage{APIKey: "api-key"}

	record := RecordFromTokenStorage(storage, now)
	if record == nil {
		t.Fatal("RecordFromTokenStorage() = nil")
	}
	if storage.Email != "1780749000123" {
		t.Fatalf("storage.Email = %q, want timestamp identifier", storage.Email)
	}
	if record.ID != "iflow-1780749000123.json" || record.FileName != "iflow-1780749000123.json" {
		t.Fatalf("ID/FileName = %q/%q, want timestamped iflow filename", record.ID, record.FileName)
	}
	if got, _ := record.Metadata["email"].(string); got != "1780749000123" {
		t.Fatalf("metadata[email] = %q, want timestamp identifier", got)
	}
}

func TestRecordFromTokenStorageHandlesNilStorage(t *testing.T) {
	if record := RecordFromTokenStorage(nil, time.Time{}); record != nil {
		t.Fatalf("RecordFromTokenStorage(nil) = %#v, want nil", record)
	}
}
