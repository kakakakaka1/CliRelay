package usage

import (
	"math"
	"testing"
)

const businessTenantID = "00000000-0000-0000-0000-0000000000aa"

func TestListModelConfigsForTenantInheritsSystemCatalog(t *testing.T) {
	initModelConfigTestDB(t)

	if err := UpsertModelConfigForTenant(systemTenantID, ModelConfigRow{
		ModelID:               "openrouter-inherited",
		OwnedBy:               "openrouter",
		Enabled:               true,
		PricingMode:           "token",
		InputPricePerMillion:  1.5,
		OutputPricePerMillion: 6,
		Source:                "openrouter",
	}); err != nil {
		t.Fatalf("UpsertModelConfigForTenant(system) error = %v", err)
	}

	rows := ListModelConfigsForTenant(businessTenantID)
	var found ModelConfigRow
	var ok bool
	for _, row := range rows {
		if row.ModelID == "openrouter-inherited" {
			found = row
			ok = true
			break
		}
	}
	if !ok {
		t.Fatal("expected business tenant to inherit system model config")
	}
	if found.InputPricePerMillion != 1.5 || found.OutputPricePerMillion != 6 {
		t.Fatalf("inherited pricing = %+v", found)
	}
	if found.Source != "openrouter" {
		t.Fatalf("inherited source = %q", found.Source)
	}
}

func TestGetModelConfigForTenantPrefersTenantOverride(t *testing.T) {
	initModelConfigTestDB(t)

	if err := UpsertModelConfigForTenant(systemTenantID, ModelConfigRow{
		ModelID:               "override-me",
		Enabled:               true,
		PricingMode:           "token",
		InputPricePerMillion:  1,
		OutputPricePerMillion: 2,
		Source:                "openrouter",
	}); err != nil {
		t.Fatalf("system upsert: %v", err)
	}
	if err := UpsertModelConfigForTenant(businessTenantID, ModelConfigRow{
		ModelID:               "override-me",
		Enabled:               true,
		PricingMode:           "token",
		InputPricePerMillion:  9,
		OutputPricePerMillion: 18,
		Source:                "user",
	}); err != nil {
		t.Fatalf("tenant upsert: %v", err)
	}

	row, ok := GetModelConfigForTenant(businessTenantID, "override-me")
	if !ok {
		t.Fatal("expected tenant override")
	}
	if row.InputPricePerMillion != 9 || row.Source != "user" {
		t.Fatalf("got %+v, want tenant override", row)
	}
}

func TestGetAllModelPricingForTenantInheritsSystem(t *testing.T) {
	initModelConfigTestDB(t)

	if err := UpsertModelPricingV2ForTenant(systemTenantID, "priced-system", 2, 4, 0.5, 0.25, 0.75); err != nil {
		t.Fatalf("system pricing upsert: %v", err)
	}
	if err := UpsertModelPricingV2ForTenant(businessTenantID, "priced-tenant", 3, 6, 0, 0, 0); err != nil {
		t.Fatalf("tenant pricing upsert: %v", err)
	}

	all := GetAllModelPricingForTenant(businessTenantID)
	if _, ok := all["priced-system"]; !ok {
		t.Fatal("expected inherited system pricing")
	}
	if _, ok := all["priced-tenant"]; !ok {
		t.Fatal("expected tenant-owned pricing")
	}

	// Tenant override wins over system for same model id.
	if err := UpsertModelPricingV2ForTenant(systemTenantID, "priced-tenant", 1, 1, 0, 0, 0); err != nil {
		t.Fatalf("system conflict upsert: %v", err)
	}
	all = GetAllModelPricingForTenant(businessTenantID)
	if got := all["priced-tenant"].InputPricePerMillion; got != 3 {
		t.Fatalf("tenant override lost: input=%v", got)
	}
}

func TestCalculateCostForTenantUsesSystemPricing(t *testing.T) {
	initModelConfigTestDB(t)

	if err := UpsertModelConfigForTenant(systemTenantID, ModelConfigRow{
		ModelID:               "cost-inherit",
		Enabled:               true,
		PricingMode:           "token",
		InputPricePerMillion:  10,
		OutputPricePerMillion: 20,
		CachedPricePerMillion: 1,
		Source:                "openrouter",
	}); err != nil {
		t.Fatalf("system upsert: %v", err)
	}

	cost := CalculateCostForTenant(businessTenantID, "cost-inherit", 1000, 500, 0)
	want := (float64(1000)*10 + float64(500)*20) / 1_000_000
	if math.Abs(cost-want) > 1e-12 {
		t.Fatalf("cost = %v, want %v", cost, want)
	}
}
