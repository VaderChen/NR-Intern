package harness

import "AgenticService/src/domain"

func sameToolCatalog(left, right []domain.ToolDefinition) bool {
	if len(left) != len(right) {
		return false
	}
	ids := map[string]string{}
	for _, definition := range left {
		ids[definition.Name] = domain.ToolContractID(definition)
	}
	for _, definition := range right {
		if ids[definition.Name] == "" || ids[definition.Name] != domain.ToolContractID(definition) {
			return false
		}
	}
	return true
}

func refreshedRetriever(previous *toolRetriever, definitions []domain.ToolDefinition, query string, enabled bool) *toolRetriever {
	next := newToolRetriever(definitions, query, enabled)
	if previous != nil {
		previous.mu.Lock()
		defer previous.mu.Unlock()
		for name := range previous.revealed {
			if _, exists := next.known[name]; exists {
				next.revealed[name] = true
			}
		}
	}
	return next
}
