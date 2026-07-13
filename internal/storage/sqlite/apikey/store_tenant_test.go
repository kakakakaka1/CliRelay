package apikey

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStoreTenantIsolation(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	InitTable(db)

	tenantA := NewTenantStore(db, "00000000-0000-0000-0000-00000000000a")
	tenantB := NewTenantStore(db, "00000000-0000-0000-0000-00000000000b")
	if err := tenantA.Upsert(APIKeyRow{ID: "id-a", Key: "sk-a", Name: "A"}); err != nil {
		t.Fatalf("tenant A upsert: %v", err)
	}
	if got := tenantB.Get("sk-a"); got != nil {
		t.Fatalf("tenant B read tenant A key: %#v", got)
	}
	if err := tenantB.DeleteByID("id-a"); err != nil {
		t.Fatalf("tenant B delete by ID: %v", err)
	}
	if got := tenantA.GetByID("id-a"); got == nil {
		t.Fatal("tenant B delete removed tenant A key")
	}
	if err := tenantB.Upsert(APIKeyRow{ID: "id-b", Key: "sk-a", Name: "B"}); err == nil {
		t.Fatal("globally duplicate API key should be rejected")
	}
	if got := tenantB.List(); len(got) != 0 {
		t.Fatalf("tenant B list = %#v, want empty", got)
	}
}
