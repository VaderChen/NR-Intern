package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// scope 是 Runtime 的來源版本識別，不放入憑證或本機設定內容。
func ToolContractFingerprint(definition ToolDefinition, scope string) string {
	definition.ContractID = ""
	encoded, err := json.Marshal(struct {
		Scope      string
		Definition ToolDefinition
	}{scope, definition})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func ToolContractID(definition ToolDefinition) string {
	if definition.ContractID != "" {
		return definition.ContractID
	}
	return ToolContractFingerprint(definition, "")
}
