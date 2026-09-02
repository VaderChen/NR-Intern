package application

import (
	"AgenticService/src/domain"
	"sort"
	"strings"
)

// cloneModelPrices 複製設定值，讓 Service 不會持有可被呼叫端改寫的 map。
func cloneModelPrices(values map[string]map[string]domain.ModelPrice) map[string]map[string]domain.ModelPrice {
	if len(values) == 0 {
		return map[string]map[string]domain.ModelPrice{}
	}
	result := make(map[string]map[string]domain.ModelPrice, len(values))
	for providerID, models := range values {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			continue
		}
		result[providerID] = make(map[string]domain.ModelPrice, len(models))
		for model, price := range models {
			model = strings.TrimSpace(model)
			if model != "" {
				result[providerID][model] = price
			}
		}
	}
	return result
}

// pricedRunUsage 只接受後端設定的 USD 價格，並清除引擎可能夾帶的成本，
// 避免非信任的 Agent 實作自行捏造金額出現在歷史 Run。
func pricedRunUsage(raw *domain.RunUsage, providerID, model string, prices map[string]map[string]domain.ModelPrice) *domain.RunUsage {
	if raw == nil {
		return nil
	}
	usage := *raw
	usage.ProviderID = strings.TrimSpace(providerID)
	usage.Model = strings.TrimSpace(model)
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	usage.EstimatedCostUSD = nil
	usage.Currency = ""
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return &usage
	}
	price, ok := prices[usage.ProviderID][usage.Model]
	if !ok || (strings.TrimSpace(price.Currency) != "" && !strings.EqualFold(strings.TrimSpace(price.Currency), "USD")) {
		return &usage
	}
	cost := float64(usage.InputTokens)*price.InputPerMillion/1_000_000 +
		float64(usage.OutputTokens)*price.OutputPerMillion/1_000_000
	usage.EstimatedCostUSD = &cost
	usage.Currency = "USD"
	return &usage
}

func saveRunUsage(run *domain.Run, result *domain.RunResult, prices map[string]map[string]domain.ModelPrice) {
	if run == nil || result == nil || result.Usage == nil {
		return
	}
	usage := pricedRunUsage(result.Usage, run.ProviderID, run.Model, prices)
	run.Usage = usage
	result.Usage = usage
}

type sessionUsageGroup struct {
	usage     domain.RunUsage
	costKnown bool
}

// summarizeSessionUsage 只讀取已保存的 Run 快照，不掃描 transcript 或事件，
// 因此重新載入 Session、重試同一請求都不會把同一輪 token 再算一次。
func summarizeSessionUsage(runs []domain.Run) *domain.SessionUsage {
	result := &domain.SessionUsage{}
	groups := map[string]*sessionUsageGroup{}
	allCostKnown := true
	hasUsage := false
	totalCost := float64(0)
	for _, run := range runs {
		if run.Usage == nil {
			continue
		}
		usage := *run.Usage
		if usage.TotalTokens <= 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		result.InputTokens += usage.InputTokens
		result.OutputTokens += usage.OutputTokens
		result.TotalTokens += usage.TotalTokens
		if usage.TotalTokens <= 0 {
			continue
		}
		hasUsage = true
		key := usage.ProviderID + "\x00" + usage.Model
		group := groups[key]
		if group == nil {
			group = &sessionUsageGroup{usage: domain.RunUsage{
				ProviderID: usage.ProviderID,
				Model:      usage.Model,
			}, costKnown: true}
			groups[key] = group
		}
		group.usage.InputTokens += usage.InputTokens
		group.usage.OutputTokens += usage.OutputTokens
		group.usage.TotalTokens += usage.TotalTokens
		if usage.EstimatedCostUSD == nil {
			group.costKnown = false
			allCostKnown = false
		} else {
			if group.usage.EstimatedCostUSD == nil {
				cost := float64(0)
				group.usage.EstimatedCostUSD = &cost
			}
			*group.usage.EstimatedCostUSD += *usage.EstimatedCostUSD
			totalCost += *usage.EstimatedCostUSD
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result.ByModel = make([]domain.RunUsage, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		if !group.costKnown {
			group.usage.EstimatedCostUSD = nil
			group.usage.Currency = ""
		} else if group.usage.EstimatedCostUSD != nil {
			group.usage.Currency = "USD"
		}
		result.ByModel = append(result.ByModel, group.usage)
	}
	if hasUsage && allCostKnown {
		result.EstimatedCostUSD = &totalCost
		result.Currency = "USD"
	}
	return result
}
