package authfiles

import (
	"errors"
	"os"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestBuildEntryRequiresPathForNonRuntimeAuth(t *testing.T) {
	auth := &coreauth.Auth{ID: "missing-path", Provider: "codex"}
	if entry := BuildEntry(auth, EntryOptions{}); entry != nil {
		t.Fatalf("BuildEntry() = %#v, want nil", entry)
	}
}

func TestListEntriesBuildsAndSortsAuthEntries(t *testing.T) {
	auths := []*coreauth.Auth{
		{
			ID:       "zeta",
			FileName: "zeta.json",
			Provider: "codex",
			Attributes: map[string]string{
				"runtime_only": "true",
			},
		},
		nil,
		{
			ID:       "alpha",
			FileName: "alpha.json",
			Provider: "claude",
			Attributes: map[string]string{
				"runtime_only": "true",
			},
		},
		{
			ID:       "hidden",
			Provider: "codex",
		},
	}

	got := ListEntries(auths, EntryOptions{})
	if len(got) != 2 {
		t.Fatalf("ListEntries() length = %d, want 2: %#v", len(got), got)
	}
	if got[0]["name"] != "alpha.json" || got[1]["name"] != "zeta.json" {
		t.Fatalf("sorted names = %#v, want alpha then zeta", []any{got[0]["name"], got[1]["name"]})
	}
}

func TestBuildEntryAllowsRuntimeOnlyAuthWithoutPath(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "runtime",
		Provider: "codex",
		Attributes: map[string]string{
			"runtime_only": "true",
		},
	}

	entry := BuildEntry(auth, EntryOptions{})
	if entry == nil {
		t.Fatal("expected runtime-only entry")
	}
	if runtimeOnly, _ := entry["runtime_only"].(bool); !runtimeOnly {
		t.Fatalf("runtime_only = %#v, want true", entry["runtime_only"])
	}
	if source, _ := entry["source"].(string); source != "memory" {
		t.Fatalf("source = %q, want memory", source)
	}
}

func TestBuildEntryUsesInjectedStat(t *testing.T) {
	modtime := time.Date(2026, 4, 1, 9, 30, 0, 0, time.UTC)
	auth := &coreauth.Auth{
		ID:       "file-auth",
		FileName: "file-auth.json",
		Provider: "codex",
		Attributes: map[string]string{
			"path": "/tmp/file-auth.json",
		},
	}

	entry := BuildEntry(auth, EntryOptions{
		Stat: func(path string) (os.FileInfo, error) {
			if path != "/tmp/file-auth.json" {
				t.Fatalf("stat path = %q, want /tmp/file-auth.json", path)
			}
			return fakeFileInfo{size: 42, modtime: modtime}, nil
		},
	})
	if entry == nil {
		t.Fatal("expected file entry")
	}
	if got, _ := entry["size"].(int64); got != 42 {
		t.Fatalf("size = %d, want 42", got)
	}
	if got, _ := entry["modtime"].(time.Time); !got.Equal(modtime) {
		t.Fatalf("modtime = %v, want %v", got, modtime)
	}
}

func TestBuildEntryHidesRemovedDisabledFileAuth(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "removed-auth",
		Provider: "codex",
		Disabled: true,
		Attributes: map[string]string{
			"path": "/tmp/removed-auth.json",
		},
	}

	entry := BuildEntry(auth, EntryOptions{
		Stat: func(string) (os.FileInfo, error) {
			return nil, os.ErrNotExist
		},
	})
	if entry != nil {
		t.Fatalf("BuildEntry() = %#v, want nil", entry)
	}
}

func TestBuildEntryCallsStatErrorHook(t *testing.T) {
	auth := &coreauth.Auth{
		ID:       "stat-error",
		Provider: "codex",
		Attributes: map[string]string{
			"path": "/tmp/stat-error.json",
		},
	}
	wantErr := errors.New("boom")
	var called bool

	entry := BuildEntry(auth, EntryOptions{
		Stat: func(string) (os.FileInfo, error) {
			return nil, wantErr
		},
		OnStatError: func(path string, err error) {
			called = true
			if path != "/tmp/stat-error.json" || !errors.Is(err, wantErr) {
				t.Fatalf("stat hook = (%q, %v), want path and boom", path, err)
			}
		},
	})
	if entry == nil {
		t.Fatal("expected entry even when stat fails")
	}
	if !called {
		t.Fatal("expected stat error hook")
	}
}

type fakeFileInfo struct {
	size    int64
	modtime time.Time
}

func (f fakeFileInfo) Name() string       { return "fake" }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return f.modtime }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
