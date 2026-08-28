package domain

// ServiceSettings 是管理介面可即時調整的服務顯示設定。
type ServiceSettings struct {
	ServiceName string `json:"service_name"`
}

type UpdateServiceSettingsInput struct {
	ServiceName string `json:"service_name"`
}
