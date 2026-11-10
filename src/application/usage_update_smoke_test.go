package application

import (
	"AgenticService/src/domain"
	"sync"
	"testing"
)

func TestModelPriceUpdateSmoke(t *testing.T) {
	s := &Service{}
	prices := map[string]map[string]domain.ModelPrice{"synthetic": {"model": {InputPerMillion: 1, Currency: "USD"}}}
	s.UpdateModelPrices(prices)
	prices["synthetic"]["model"] = domain.ModelPrice{InputPerMillion: 9}
	run := domain.Run{ProviderID: "synthetic", Model: "model"}
	result := domain.RunResult{Usage: &domain.RunUsage{InputTokens: 1_000_000}}
	s.saveRunUsage(&run, &result)
	if run.Usage.EstimatedCostUSD == nil || *run.Usage.EstimatedCostUSD != 1 {
		t.Fatal("價格快照受外部 map 修改影響")
	}
	s.UpdateModelPrices(nil)
	next := domain.Run{ProviderID: "synthetic", Model: "model"}
	s.saveRunUsage(&next, &result)
	if next.Usage.EstimatedCostUSD != nil || *run.Usage.EstimatedCostUSD != 1 {
		t.Fatal("舊價格殘留或歷史成本被改寫")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.UpdateModelPrices(prices)
				r := domain.Run{ProviderID: "synthetic", Model: "model"}
				v := domain.RunResult{Usage: &domain.RunUsage{InputTokens: 1}}
				s.saveRunUsage(&r, &v)
			}
		}()
	}
	wg.Wait()
}
