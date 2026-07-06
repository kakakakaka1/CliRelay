package compatdriver

import "testing"

func TestRewritePlaceholdersSkipsQuotedQuestionMarks(t *testing.T) {
	got := rewritePlaceholders("SELECT '?' AS q, a FROM t WHERE b = ? AND c = ?")
	want := "SELECT '?' AS q, a FROM t WHERE b = $1 AND c = $2"
	if got != want {
		t.Fatalf("rewritePlaceholders() = %q, want %q", got, want)
	}
}

func TestRewriteSQLMapsSQLiteDDL(t *testing.T) {
	got := rewriteSQL("CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, at DATETIME, body BLOB NOT NULL DEFAULT X'')")
	want := "CREATE TABLE t (id BIGSERIAL PRIMARY KEY, at TIMESTAMPTZ, body BYTEA NOT NULL DEFAULT decode('', 'hex'))"
	if got != want {
		t.Fatalf("rewriteSQL() = %q, want %q", got, want)
	}
}

func TestRewriteSQLMakesAddColumnIdempotent(t *testing.T) {
	got := rewriteSQL("ALTER TABLE api_keys ADD COLUMN id TEXT NOT NULL DEFAULT ''")
	want := "ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS id TEXT NOT NULL DEFAULT ''"
	if got != want {
		t.Fatalf("rewriteSQL() = %q, want %q", got, want)
	}
}
