package usage

import "testing"

func TestMiniMaxImageDefaultPricingUsesSuccessfulSingleImageCalls(t *testing.T) {
	initModelConfigTestDB(t)
	row, ok := GetModelConfig("image-01")
	if !ok {
		t.Fatal("MiniMax image model missing from persisted model library")
	}
	if row.PricingMode != "call" || row.PricePerCall != 0.0035 {
		t.Fatalf("pricing = %q / %v, want call / 0.0035", row.PricingMode, row.PricePerCall)
	}
	if cost := CalculateCostV2("image-01", TokenStats{}); cost != 0.0035 {
		t.Fatalf("zero-token image cost = %v, want 0.0035", cost)
	}
	row.PricePerCall = 0.01
	if err := UpsertModelConfig(row); err != nil {
		t.Fatal(err)
	}
	if cost := CalculateCostV2("image-01", TokenStats{}); cost != 0.01 {
		t.Fatalf("operator override cost = %v, want 0.01", cost)
	}
}
