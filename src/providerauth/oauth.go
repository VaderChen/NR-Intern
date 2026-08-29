// Package providerauth implements the ChatGPT/Codex OAuth authorization-code
// flow used by PI Agent. It owns a short-lived loopback callback server and a
// permission-restricted token store; it never reads another application's login
// files or returns bearer credentials to the management UI.
package providerauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// CodexClientID is PI Agent's public OAuth client identifier. Desktop PKCE
	// clients cannot keep a client secret, so this value is not a credential.
	CodexClientID           = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultAuthorizationURL = "https://auth.openai.com/oauth/authorize"
	DefaultTokenURL         = "https://auth.openai.com/oauth/token"
	DefaultScope            = "openid profile email offline_access"
	DefaultOriginator       = "pi"
	CallbackAddress         = "127.0.0.1:1455"
	CallbackPath            = "/auth/callback"
	CallbackURI             = "http://localhost:1455/auth/callback"
	flowLifetime            = 10 * time.Minute
	maxOAuthResponseBytes   = 2 * 1024 * 1024
	chatGPTAuthClaim        = "https://api.openai.com/auth"
)

// Config is retained inside Provider configuration for backwards-compatible
// decoding. normalizeConfig always replaces it with the fixed Codex public
// client settings so the UI cannot redirect credentials to arbitrary endpoints.
type Config struct {
	ClientID         string `json:"client_id,omitempty"`
	AuthorizationURL string `json:"authorization_url,omitempty"`
	TokenURL         string `json:"token_url,omitempty"`
	Scope            string `json:"scope,omitempty"`
	Originator       string `json:"originator,omitempty"`
}

// DefaultConfig returns the fixed, public ChatGPT/Codex OAuth client settings.
func DefaultConfig() Config {
	return Config{
		ClientID:         CodexClientID,
		AuthorizationURL: DefaultAuthorizationURL,
		TokenURL:         DefaultTokenURL,
		Scope:            DefaultScope,
		Originator:       DefaultOriginator,
	}
}

// StartResult is safe to return to the management UI. It contains no token.
type StartResult struct {
	ProviderID       string `json:"provider_id"`
	Status           string `json:"status"`
	AuthorizationURL string `json:"authorization_url"`
	CallbackURI      string `json:"callback_uri"`
	BrowserOpened    bool   `json:"browser_opened"`
	ExpiresAt        string `json:"expires_at"`
}

// Status is the redacted state exposed to the management UI.
type Status struct {
	ProviderID   string `json:"provider_id"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	AccountEmail string `json:"account_email,omitempty"`
	AccountName  string `json:"account_name,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

type tokenRecord struct {
	ProviderID   string `json:"provider_id"`
	ClientID     string `json:"client_id"`
	AccountID    string `json:"account_id,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	AccountEmail string `json:"account_email,omitempty"`
	AccountName  string `json:"account_name,omitempty"`
	UpdatedAt    string `json:"updated_at"`
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type userInfo struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

type pendingSession struct {
	ProviderID  string
	State       string
	Verifier    string
	Config      Config
	Status      string
	LastError   string
	ExpiresAt   time.Time
	CallbackURI string
}

// Manager owns the short-lived loopback server and the encrypted-in-transit,
// permission-restricted token store. Only one interactive flow can use the fixed
// registered loopback redirect at a time.
type Manager struct {
	path   string
	logger *slog.Logger
	client *http.Client

	mu       sync.Mutex
	pending  *pendingSession
	server   *http.Server
	listener net.Listener
	storeMu  sync.Mutex
}

func New(path string, logger *slog.Logger) (*Manager, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("OAuth token store path is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		path:   filepath.Clean(path),
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Start starts the loopback listener before returning the authorization URL.
func (m *Manager) Start(providerID string, config Config) (StartResult, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return StartResult{}, fmt.Errorf("provider id is required")
	}
	config, err := normalizeConfig(config)
	if err != nil {
		return StartResult{}, err
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("create OAuth state: %w", err)
	}
	verifier, err := randomURLSafe(48)
	if err != nil {
		return StartResult{}, fmt.Errorf("create PKCE verifier: %w", err)
	}

	m.mu.Lock()
	if m.listener != nil {
		m.mu.Unlock()
		return StartResult{}, fmt.Errorf("another OAuth authorization is already waiting for a callback")
	}
	listener, err := net.Listen("tcp4", CallbackAddress)
	if err != nil {
		m.mu.Unlock()
		return StartResult{}, fmt.Errorf("start temporary OAuth callback server at %s: %w", CallbackAddress, err)
	}
	expiresAt := time.Now().Add(flowLifetime)
	m.pending = &pendingSession{
		ProviderID:  providerID,
		State:       state,
		Verifier:    verifier,
		Config:      config,
		Status:      "pending",
		ExpiresAt:   expiresAt,
		CallbackURI: CallbackURI,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(CallbackPath, m.handleCallback)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       15 * time.Second,
	}
	m.server = server
	m.listener = listener
	m.mu.Unlock()
	m.logger.Info("ChatGPT Codex OAuth callback server started", "provider_id", providerID, "address", CallbackAddress)

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			m.failPending(state, fmt.Sprintf("OAuth callback server failed: %v", serveErr))
		}
	}()
	go m.expirePending(state, expiresAt)

	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", CallbackURI)
	values.Set("scope", config.Scope)
	values.Set("state", state)
	values.Set("code_challenge", pkceS256Challenge(verifier))
	values.Set("code_challenge_method", "S256")
	values.Set("id_token_add_organizations", "true")
	values.Set("codex_cli_simplified_flow", "true")
	values.Set("originator", config.Originator)
	authorizationURL := config.AuthorizationURL + "?" + values.Encode()
	browserOpened := openBrowser(authorizationURL)
	return StartResult{
		ProviderID:       providerID,
		Status:           "pending",
		AuthorizationURL: authorizationURL,
		CallbackURI:      CallbackURI,
		BrowserOpened:    browserOpened,
		ExpiresAt:        expiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// Status never exposes OAuth bearer material. An expired access token is
// refreshed before reporting a connected state when a refresh token exists.
func (m *Manager) Status(ctx context.Context, providerID string, config Config) (Status, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return Status{}, fmt.Errorf("provider id is required")
	}
	config, err := normalizeConfig(config)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	if m.pending != nil && m.pending.ProviderID == providerID && (m.pending.Status == "pending" || m.pending.Status == "failed") {
		status := Status{
			ProviderID: providerID,
			Status:     m.pending.Status,
			Message:    m.pending.LastError,
			ExpiresAt:  m.pending.ExpiresAt.UTC().Format(time.RFC3339),
		}
		m.mu.Unlock()
		return status, nil
	}
	m.mu.Unlock()
	if _, err := m.AccessToken(ctx, providerID, config); err == nil {
		record, getErr := m.getRecord(providerID)
		if getErr == nil {
			return statusFromRecord(record), nil
		}
	}
	return Status{ProviderID: providerID, Status: "idle"}, nil
}

// AccessToken returns a valid bearer token for an adapter request and refreshes
// it when necessary. The token is never persisted in Provider settings.
func (m *Manager) AccessToken(ctx context.Context, providerID string, config Config) (string, error) {
	providerID = strings.TrimSpace(providerID)
	config, err := normalizeConfig(config)
	if err != nil {
		return "", err
	}
	m.storeMu.Lock()
	defer m.storeMu.Unlock()
	records, err := m.loadRecordsLocked()
	if err != nil {
		return "", err
	}
	record, exists := records[providerID]
	if !exists || strings.TrimSpace(record.AccessToken) == "" {
		return "", fmt.Errorf("ChatGPT/Codex OAuth is not connected for provider %q", providerID)
	}
	if record.ClientID != config.ClientID {
		return "", fmt.Errorf("stored OAuth token is not a ChatGPT/Codex PI credential; reconnect provider %q", providerID)
	}
	if strings.TrimSpace(record.AccountID) == "" {
		record.AccountID = codexAccountID(record.AccessToken)
		if record.AccountID == "" {
			return "", fmt.Errorf("ChatGPT/Codex OAuth token is missing chatgpt_account_id")
		}
		records[providerID] = record
		if err := m.saveRecordsLocked(records); err != nil {
			return "", err
		}
	}
	if tokenUsable(record.ExpiresAt) {
		return strings.TrimSpace(record.AccessToken), nil
	}
	if strings.TrimSpace(record.RefreshToken) == "" {
		return "", fmt.Errorf("ChatGPT/Codex OAuth token expired and has no refresh token")
	}
	token, err := m.exchangeToken(ctx, config.TokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {config.ClientID},
		"refresh_token": {strings.TrimSpace(record.RefreshToken)},
	}, false)
	if err != nil {
		return "", fmt.Errorf("refresh ChatGPT/Codex OAuth token: %w", err)
	}
	record = mergeTokenRecord(record, token)
	if record.AccountID == "" {
		return "", fmt.Errorf("refreshed ChatGPT/Codex OAuth token is missing chatgpt_account_id")
	}
	records[providerID] = record
	if err := m.saveRecordsLocked(records); err != nil {
		return "", err
	}
	return strings.TrimSpace(record.AccessToken), nil
}

// Disconnect removes only this Provider's OAuth tokens and cancels its pending
// callback flow. Provider settings remain intact.
func (m *Manager) Disconnect(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	m.stopPending(providerID, "")
	m.storeMu.Lock()
	defer m.storeMu.Unlock()
	records, err := m.loadRecordsLocked()
	if err != nil {
		return err
	}
	if _, exists := records[providerID]; !exists {
		return nil
	}
	delete(records, providerID)
	return m.saveRecordsLocked(records)
}

// HasToken performs a local, redacted credential-presence check without network
// access or token refresh. It is intended for settings views only.
func (m *Manager) HasToken(providerID string) bool {
	record, err := m.getRecord(strings.TrimSpace(providerID))
	if err != nil {
		return false
	}
	return strings.TrimSpace(record.AccessToken) != "" && record.ClientID == CodexClientID && strings.TrimSpace(record.AccountID) != ""
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	server := m.server
	listener := m.listener
	m.server = nil
	m.listener = nil
	m.pending = nil
	m.mu.Unlock()
	if server != nil {
		return server.Shutdown(ctx)
	}
	if listener != nil {
		return listener.Close()
	}
	return nil
}

func (m *Manager) handleCallback(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method != http.MethodGet || request.URL.Path != CallbackPath || len(request.URL.RawQuery) > 16*1024 {
		http.Error(writer, "invalid OAuth callback", http.StatusBadRequest)
		return
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		http.Error(writer, "OAuth callback must originate from this computer", http.StatusForbidden)
		return
	}
	query := request.URL.Query()
	state := strings.TrimSpace(query.Get("state"))
	m.mu.Lock()
	if m.pending == nil || m.pending.State != state || time.Now().After(m.pending.ExpiresAt) {
		m.mu.Unlock()
		writeCallbackHTML(writer, false, "OAuth 驗證狀態無效或已逾時。")
		return
	}
	session := *m.pending
	m.mu.Unlock()

	if callbackError := strings.TrimSpace(query.Get("error")); callbackError != "" {
		description := strings.TrimSpace(query.Get("error_description"))
		if description == "" {
			description = callbackError
		}
		m.failPending(state, description)
		writeCallbackHTML(writer, false, description)
		m.shutdownCallbackServer()
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" || len(code) > 16*1024 {
		m.failPending(state, "OAuth callback is missing an authorization code")
		writeCallbackHTML(writer, false, "OAuth 回傳缺少 authorization code。")
		m.shutdownCallbackServer()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := m.exchangeToken(ctx, session.Config.TokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {session.Config.ClientID},
		"code":          {code},
		"code_verifier": {session.Verifier},
		"redirect_uri":  {session.CallbackURI},
	}, true)
	if err != nil {
		m.failPending(state, err.Error())
		writeCallbackHTML(writer, false, "OAuth Token 交換失敗，請回到應用程式查看錯誤。")
		m.shutdownCallbackServer()
		return
	}
	accountID := codexAccountID(token.AccessToken)
	if accountID == "" {
		m.failPending(state, "ChatGPT OAuth token is missing chatgpt_account_id")
		writeCallbackHTML(writer, false, "登入成功，但 Token 缺少 ChatGPT 帳號識別，請回到應用程式重試。")
		m.shutdownCallbackServer()
		return
	}
	info := userInfoFromTokens(token)
	m.mu.Lock()
	flowStillActive := m.pending != nil && m.pending.State == state && m.pending.Status == "pending"
	m.mu.Unlock()
	if !flowStillActive {
		writeCallbackHTML(writer, false, "OAuth 驗證已由應用程式取消。")
		return
	}
	record := tokenRecord{
		ProviderID:   session.ProviderID,
		ClientID:     session.Config.ClientID,
		AccountID:    accountID,
		TokenType:    firstNonEmpty(token.TokenType, "Bearer"),
		AccessToken:  strings.TrimSpace(token.AccessToken),
		RefreshToken: strings.TrimSpace(token.RefreshToken),
		IDToken:      strings.TrimSpace(token.IDToken),
		Scope:        firstNonEmpty(token.Scope, session.Config.Scope),
		ExpiresAt:    tokenExpiry(token),
		AccountEmail: info.Email,
		AccountName:  info.Name,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := m.putRecord(record); err != nil {
		m.failPending(state, err.Error())
		writeCallbackHTML(writer, false, "OAuth Token 無法安全儲存，請回到應用程式查看錯誤。")
		m.shutdownCallbackServer()
		return
	}
	m.mu.Lock()
	if m.pending != nil && m.pending.State == state {
		m.pending = nil
	}
	m.mu.Unlock()
	m.logger.Info("ChatGPT Codex OAuth connected", "provider_id", session.ProviderID)
	writeCallbackHTML(writer, true, "ChatGPT／Codex 登入已完成，可以關閉此頁回到應用程式。")
	m.shutdownCallbackServer()
}

func (m *Manager) exchangeToken(ctx context.Context, endpoint string, values url.Values, requireRefreshToken bool) (tokenResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "NR-Intern/chatgpt-codex-oauth")
	response, err := m.client.Do(request)
	if err != nil {
		return tokenResponse{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBytes+1))
	if err != nil {
		return tokenResponse{}, err
	}
	if len(data) > maxOAuthResponseBytes {
		return tokenResponse{}, fmt.Errorf("OAuth token response is too large")
	}
	var token tokenResponse
	if err := json.Unmarshal(data, &token); err != nil {
		return tokenResponse{}, fmt.Errorf("OAuth token endpoint returned invalid JSON")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || token.Error != "" {
		message := firstNonEmpty(token.ErrorDescription, token.Error, response.Status)
		return tokenResponse{}, fmt.Errorf("OAuth token endpoint rejected the request: %s", message)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return tokenResponse{}, fmt.Errorf("OAuth token response is missing access_token")
	}
	if requireRefreshToken && strings.TrimSpace(token.RefreshToken) == "" {
		return tokenResponse{}, fmt.Errorf("OAuth token response is missing refresh_token")
	}
	return token, nil
}

func normalizeConfig(_ Config) (Config, error) {
	return DefaultConfig(), nil
}

func (m *Manager) expirePending(state string, expiresAt time.Time) {
	timer := time.NewTimer(time.Until(expiresAt))
	defer timer.Stop()
	<-timer.C
	m.mu.Lock()
	if m.pending == nil || m.pending.State != state {
		m.mu.Unlock()
		return
	}
	m.pending.Status = "failed"
	m.pending.LastError = "OAuth authorization timed out"
	server := m.server
	listener := m.listener
	m.server = nil
	m.listener = nil
	m.mu.Unlock()
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	} else if listener != nil {
		_ = listener.Close()
	}
}

func (m *Manager) failPending(state, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil && m.pending.State == state {
		m.pending.Status = "failed"
		m.pending.LastError = strings.TrimSpace(message)
	}
}

func (m *Manager) stopPending(providerID, message string) {
	m.mu.Lock()
	if m.pending == nil || m.pending.ProviderID != providerID {
		m.mu.Unlock()
		return
	}
	if message != "" {
		m.pending.Status = "failed"
		m.pending.LastError = message
	} else {
		m.pending = nil
	}
	server := m.server
	listener := m.listener
	m.server = nil
	m.listener = nil
	m.mu.Unlock()
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	} else if listener != nil {
		_ = listener.Close()
	}
}

func (m *Manager) shutdownCallbackServer() {
	go func() {
		m.mu.Lock()
		server := m.server
		listener := m.listener
		m.server = nil
		m.listener = nil
		m.mu.Unlock()
		if server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.Shutdown(ctx)
			cancel()
		} else if listener != nil {
			_ = listener.Close()
		}
	}()
}

func (m *Manager) getRecord(providerID string) (tokenRecord, error) {
	m.storeMu.Lock()
	defer m.storeMu.Unlock()
	records, err := m.loadRecordsLocked()
	if err != nil {
		return tokenRecord{}, err
	}
	record, exists := records[strings.TrimSpace(providerID)]
	if !exists {
		return tokenRecord{}, os.ErrNotExist
	}
	return record, nil
}

func (m *Manager) putRecord(record tokenRecord) error {
	m.storeMu.Lock()
	defer m.storeMu.Unlock()
	records, err := m.loadRecordsLocked()
	if err != nil {
		return err
	}
	records[record.ProviderID] = record
	return m.saveRecordsLocked(records)
}

func (m *Manager) loadRecordsLocked() (map[string]tokenRecord, error) {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o750); err != nil {
		return nil, fmt.Errorf("create OAuth token directory: %w", err)
	}
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]tokenRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OAuth token store: %w", err)
	}
	if err := os.Chmod(m.path, 0o600); err != nil {
		return nil, fmt.Errorf("secure OAuth token store: %w", err)
	}
	records := map[string]tokenRecord{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return records, nil
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("decode OAuth token store: %w", err)
	}
	return records, nil
}

func (m *Manager) saveRecordsLocked(records map[string]tokenRecord) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o750); err != nil {
		return fmt.Errorf("create OAuth token directory: %w", err)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OAuth token store: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(m.path), filepath.Base(m.path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create OAuth token temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure OAuth token temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write OAuth token store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync OAuth token store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close OAuth token temporary file: %w", err)
	}
	if err := replaceTokenFile(temporaryPath, m.path); err != nil {
		return fmt.Errorf("replace OAuth token store: %w", err)
	}
	return nil
}

func mergeTokenRecord(record tokenRecord, token tokenResponse) tokenRecord {
	record.TokenType = firstNonEmpty(token.TokenType, record.TokenType, "Bearer")
	record.AccessToken = strings.TrimSpace(token.AccessToken)
	record.AccountID = firstNonEmpty(codexAccountID(token.AccessToken), record.AccountID)
	record.RefreshToken = firstNonEmpty(token.RefreshToken, record.RefreshToken)
	record.IDToken = firstNonEmpty(token.IDToken, record.IDToken)
	record.Scope = firstNonEmpty(token.Scope, record.Scope)
	record.ExpiresAt = tokenExpiry(token)
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	info := userInfoFromTokens(token)
	record.AccountEmail = firstNonEmpty(info.Email, record.AccountEmail)
	record.AccountName = firstNonEmpty(info.Name, record.AccountName)
	return record
}

func statusFromRecord(record tokenRecord) Status {
	return Status{
		ProviderID:   record.ProviderID,
		Status:       "connected",
		AccountEmail: record.AccountEmail,
		AccountName:  record.AccountName,
		ExpiresAt:    record.ExpiresAt,
	}
}

func tokenUsable(expiresAt string) bool {
	if strings.TrimSpace(expiresAt) == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	return err == nil && time.Until(parsed) > 90*time.Second
}

func tokenExpiry(token tokenResponse) string {
	if token.ExpiresIn > 0 {
		return time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	if expiresAt := jwtExpiry(token.AccessToken); !expiresAt.IsZero() {
		return expiresAt.UTC().Format(time.RFC3339)
	}
	return ""
}

func userInfoFromTokens(token tokenResponse) userInfo {
	claims := jwtClaims(token.IDToken)
	if len(claims) == 0 {
		claims = jwtClaims(token.AccessToken)
	}
	return userInfo{
		Email: stringClaim(claims, "email"),
		Name:  stringClaim(claims, "name"),
	}
}

func codexAccountID(token string) string {
	claims := jwtClaims(token)
	auth, _ := claims[chatGPTAuthClaim].(map[string]any)
	return stringClaim(auth, "chatgpt_account_id")
}

func jwtExpiry(token string) time.Time {
	claims := jwtClaims(token)
	value, ok := claims["exp"].(float64)
	if !ok || value <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(value), 0)
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}
	return claims
}

func stringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return strings.TrimSpace(value)
}

func randomURLSafe(length int) (string, error) {
	data := make([]byte, length)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pkceS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func openBrowser(target string) bool {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start() == nil
}

func writeCallbackHTML(writer http.ResponseWriter, success bool, message string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	status := "失敗"
	color := "#b42318"
	if success {
		status = "完成"
		color = "#1769e0"
	}
	_, _ = fmt.Fprintf(writer, `<!doctype html><html lang="zh-Hant"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>ChatGPT／Codex OAuth %s</title></head><body style="font-family:system-ui,sans-serif;max-width:560px;margin:12vh auto;padding:24px;color:#17212a"><h1 style="color:%s">ChatGPT／Codex OAuth %s</h1><p>%s</p></body></html>`, status, color, status, html.EscapeString(message))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
