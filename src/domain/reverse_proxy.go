package domain

type ReverseProxyStatus struct {
	RuntimeChecked bool   `json:"runtime_checked"`
	Available      bool   `json:"available"`
	Running        bool   `json:"running"`
	Connected      bool   `json:"connected"`
	Endpoint       string `json:"endpoint"`
	APIKeySet      bool   `json:"api_key_set"`
	Name           string `json:"name,omitempty"`
	TargetPort     int    `json:"target_port"`
	PID            int    `json:"pid,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	PublicURL      string `json:"public_url,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type UpdateReverseProxyInput struct {
	Endpoint    string `json:"endpoint"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
	Name        string `json:"name"`
}

type StartReverseProxyInput struct {
	AcceptUsagePolicy bool `json:"accept_usage_policy"`
}
