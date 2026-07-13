package usage

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestDashboardQueriesAreTenantScoped(t *testing.T) {
	initTestUsageDB(t, config.RequestLogStorageConfig{})
	db := getDB()
	const tenantA = "00000000-0000-0000-0000-00000000000a"
	const tenantB = "00000000-0000-0000-0000-00000000000b"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, tenantID := range []string{tenantA, tenantB} {
		if _, err := db.Exec(`
			INSERT INTO request_logs
			(tenant_id, timestamp, api_key, model, source, channel_name, auth_index, failed,
			 latency_ms, first_token_ms, input_tokens, output_tokens, reasoning_tokens,
			 cached_tokens, total_tokens, cost)
			VALUES (?, ?, '', 'model', 'source', 'channel', 'auth', 0, 1, 1, 10, 5, 0, 0, 15, 0.1)
		`, tenantID, now); err != nil {
			t.Fatalf("insert %s: %v", tenantID, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO request_logs
		(tenant_id, timestamp, api_key, model, source, channel_name, auth_index, failed,
		 latency_ms, first_token_ms, input_tokens, output_tokens, reasoning_tokens,
		 cached_tokens, total_tokens, cost)
		VALUES (?, ?, '', 'model', 'source', 'channel', 'auth', 1, 1, 1, 20, 10, 0, 0, 30, 0.2)
	`, tenantB, now); err != nil {
		t.Fatal(err)
	}

	kpiA, err := QueryDashboardKPIForTenant(tenantA, 1)
	if err != nil {
		t.Fatal(err)
	}
	if kpiA.TotalRequests != 1 || kpiA.TotalTokens != 15 || kpiA.FailedRequests != 0 {
		t.Fatalf("tenant A KPI leaked: %+v", kpiA)
	}
	kpiB, err := QueryDashboardKPIForTenant(tenantB, 1)
	if err != nil {
		t.Fatal(err)
	}
	if kpiB.TotalRequests != 2 || kpiB.TotalTokens != 45 || kpiB.FailedRequests != 1 {
		t.Fatalf("tenant B KPI = %+v", kpiB)
	}

	// Per-tenant throughput stays isolated.
	trendsA, err := QueryDashboardTrendsForTenant(tenantA, 1)
	if err != nil {
		t.Fatal(err)
	}
	trendsB, err := QueryDashboardTrendsForTenant(tenantB, 1)
	if err != nil {
		t.Fatal(err)
	}
	rpmA := latestThroughputRPM(trendsA.ThroughputSeries)
	rpmB := latestThroughputRPM(trendsB.ThroughputSeries)
	if rpmA != 1 {
		t.Fatalf("tenant A latest RPM = %.0f, want 1", rpmA)
	}
	if rpmB != 2 {
		t.Fatalf("tenant B latest RPM = %.0f, want 2", rpmB)
	}

	// Platform super-admin view aggregates every tenant.
	allSeries, err := QueryDashboardThroughputAcrossTenants()
	if err != nil {
		t.Fatal(err)
	}
	if latestThroughputRPM(allSeries) != 3 {
		t.Fatalf("all-tenant latest RPM = %.0f, want 3", latestThroughputRPM(allSeries))
	}
}

func latestThroughputRPM(series []DashboardThroughputPoint) float64 {
	if len(series) == 0 {
		return 0
	}
	return series[len(series)-1].RPM
}
