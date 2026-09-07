package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyMigrationCleanFailureRollsBackSchema(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CLIRELAY_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("CLIRELAY_POSTGRES_TEST_DSN is not set")
	}
	for _, tc := range []struct {
		name       string
		triggerSQL string
	}{
		{name: "clean update rejected", triggerSQL: "RAISE EXCEPTION 'test clean marker failure';"},
		{name: "deadline during clean update", triggerSQL: "PERFORM pg_sleep(10);"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := newDisposableMigratedDB(t, ctx, dsn, "migration_atomic")
			// Inject failure at the real SQL clean-marker boundary. Migration DDL
			// must not survive an update failure or a cancellation at this point.
			if _, err := db.ExecContext(ctx, `
                CREATE FUNCTION fail_test_clean_marker() RETURNS trigger LANGUAGE plpgsql AS $$
                BEGIN
                    IF NEW.version = 'test_atomic_schema' AND NOT NEW.dirty THEN
                        `+tc.triggerSQL+`
                    END IF;
                    RETURN NEW;
                END;
                $$;
                CREATE TRIGGER test_clean_marker BEFORE UPDATE ON schema_migrations
                FOR EACH ROW EXECUTE FUNCTION fail_test_clean_marker();
            `); err != nil {
				t.Fatalf("install clean marker failure: %v", err)
			}
			migration := Migration{Version: "test_atomic_schema", SQL: "CREATE TABLE atomic_schema_probe (id INTEGER)"}
			migrationCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			err := ApplyMigrations(migrationCtx, db, []Migration{migration})
			if err == nil || !strings.Contains(err.Error(), "mark migration test_atomic_schema clean") {
				t.Fatalf("ApplyMigrations = %v, want clean marker failure", err)
			}
			t.Logf("observed clean-marker failure: %v", err)
			var exists, dirty bool
			if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.atomic_schema_probe') IS NOT NULL`).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Error("migration schema committed even though the clean marker failed")
			}
			if err := db.QueryRowContext(ctx, `SELECT dirty FROM schema_migrations WHERE version = ?`, migration.Version).Scan(&dirty); err != nil {
				t.Fatal(err)
			}
			if !dirty {
				t.Error("failed migration must retain its dirty marker for operator inspection")
			}
			// Historical dirty rows must remain fail-closed; this change does not
			// infer a previous process's commit outcome or automatically replay it.
			if err := ApplyMigrations(ctx, db, []Migration{migration}); err == nil || !strings.Contains(err.Error(), "is dirty") {
				t.Fatalf("re-apply dirty migration = %v, want explicit dirty refusal", err)
			}
		})
	}
}

func TestApplyMigrationCommitsSchemaAndCleanMarkerTogether(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CLIRELAY_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("CLIRELAY_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	db := newDisposableMigratedDB(t, ctx, dsn, "migration_success")
	migration := Migration{Version: "test_atomic_success", SQL: "CREATE TABLE atomic_success_probe (id INTEGER)"}
	if err := ApplyMigrations(ctx, db, []Migration{migration}); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	var exists, dirty bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.atomic_success_probe') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	var checksum string
	var duration int64
	if err := db.QueryRowContext(ctx, `SELECT checksum, dirty, duration_ms FROM schema_migrations WHERE version = ?`, migration.Version).Scan(&checksum, &dirty, &duration); err != nil {
		t.Fatal(err)
	}
	if !exists || dirty || checksum != migrationChecksum(migration.SQL) || duration < 0 {
		t.Fatalf("committed migration: schema=%v dirty=%v checksum=%q duration=%d", exists, dirty, checksum, duration)
	}
	if err := ApplyMigrations(ctx, db, []Migration{migration}); err != nil {
		t.Fatalf("re-apply clean migration must not replay CREATE TABLE: %v", err)
	}
}
