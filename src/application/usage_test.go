package application

import (
	"AgenticService/src/domain"
	"math"
	"testing"
)

func TestPricedRunUsageWithoutPriceLeavesCostUnset(t *testing.T) {
	raw := &domain.RunUsage{
		ProviderID:   "provider",
		Model:        "model",
		InputTokens:  120,
		OutputTokens: 8,
		TotalTokens:  128,
	}
	got := pricedRunUsage(raw, "provider", "model", map[string]map[string]domain.ModelPrice{})
	if got == nil || got.TotalTokens != 128 {
		t.Fatalf("usage = %+v, want token snapshot", got)
	}
	if got.EstimatedCostUSD != nil || got.Currency != "" {
		t.Fatalf("usage = %+v, missing price must not produce cost", got)
	}
}

func TestSessionUsageGroupsRunsAndDoesNotDoubleCountRetryOrCancel(t *testing.T) {
	canceled := domain.Run{ID: "run-canceled", Usage: &domain.RunUsage{
		ProviderID: "provider", Model: "model", InputTokens: 100, OutputTokens: 20, TotalTokens: 120,
	}}
	retry := domain.Run{ID: "run-retry", Usage: &domain.RunUsage{
		ProviderID: "provider", Model: "model", InputTokens: 80, OutputTokens: 10, TotalTokens: 90,
	}}
	runs := []domain.Run{canceled, retry}
	first := summarizeSessionUsage(runs)
	second := summarizeSessionUsage(runs)
	if first.TotalTokens != 210 || first.InputTokens != 180 || first.OutputTokens != 30 {
		t.Fatalf("session usage = %+v, want total=210 input=180 output=30", first)
	}
	if len(first.ByModel) != 1 || first.ByModel[0].TotalTokens != 210 {
		t.Fatalf("grouped usage = %+v, want one 210-token group", first.ByModel)
	}
	if second.TotalTokens != first.TotalTokens || len(second.ByModel) != len(first.ByModel) {
		t.Fatalf("re-reading usage changed the aggregate: first=%+v second=%+v", first, second)
	}
}

func TestSaveRunUsageIsIdempotentForCanceledRun(t *testing.T) {
	run := domain.Run{ProviderID: "provider", Model: "model"}
	result := domain.RunResult{Usage: &domain.RunUsage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}}
	prices := map[string]map[string]domain.ModelPrice{
		"provider": {"model": {InputPerMillion: 1, OutputPerMillion: 2, Currency: "USD"}},
	}
	saveRunUsage(&run, &result, prices)
	saveRunUsage(&run, &result, prices)
	if run.Usage == nil || run.Usage.TotalTokens != 120 {
		t.Fatalf("run usage = %+v, want one 120-token snapshot", run.Usage)
	}
	if run.Usage.EstimatedCostUSD == nil || math.Abs(*run.Usage.EstimatedCostUSD-0.00014) > 1e-12 {
		t.Fatalf("estimated cost = %v, want 0.00014", run.Usage.EstimatedCostUSD)
	}
}
