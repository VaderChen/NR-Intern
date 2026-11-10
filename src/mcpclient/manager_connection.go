package mcpclient

import (
	"AgenticService/src/domain"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (m *Manager) ensureConnected(parent context.Context, state *serverState, forceList bool) error {
	state.connectMu.Lock()
	defer state.connectMu.Unlock()
	state.mu.RLock()
	config := state.config
	session := state.session
	toolsDirty := state.toolsDirty
	state.mu.RUnlock()
	if m.state(config.ID) != state {
		return fmt.Errorf("%w: MCP 設定來源已替換，不能重新啟用舊連線", domain.ErrConflict)
	}
	if !config.Enabled {
		return nil
	}
	// 連線可能在閒置期間死掉（伺服器重啟、session 逾時、stdio 子程序結束）。
	// 在使用前就確認，讓呼叫端拿到的一定是活的 session，而不是等工具呼叫失敗
	// 才發現——後者對沒有宣告 idempotent 的工具等於直接失敗。
	if session != nil && !m.sessionUsable(parent, state, session) {
		m.closeStateLocked(state)
		session = nil
		toolsDirty = true
	}
	if session != nil && !forceList && !toolsDirty {
		return nil
	}
	if session == nil {
		m.setStatus(state, "connecting", "")
		ctx, cancel := context.WithTimeout(parent, time.Duration(config.StartupTimeoutSeconds)*time.Second)
		defer cancel()
		connected, err := m.connect(ctx, state, config)
		if err != nil {
			err = describeConnectError(config, err)
			m.setStatus(state, "error", err.Error())
			return err
		}
		done := make(chan struct{})
		state.mu.Lock()
		state.session = connected
		state.sessionDone = done
		state.lastUsedAt = time.Now()
		state.mu.Unlock()
		m.watchSession(state, connected, done)
	}
	catalogCtx, cancel := context.WithTimeout(parent, time.Duration(config.StartupTimeoutSeconds)*time.Second)
	defer cancel()
	return m.refreshTools(catalogCtx, state)
}

// sessionUsable 判斷既有連線是否還能直接使用。
//
// 先看連線是否已經結束（watchSession 會關閉 sessionDone），再對閒置一段時間的
// 連線送出 ping。ping 沒有副作用，所以這個檢查不會像重送工具呼叫那樣有重複執行
// 的風險。
func (m *Manager) sessionUsable(parent context.Context, state *serverState, session *mcp.ClientSession) bool {
	state.mu.RLock()
	done := state.sessionDone
	lastUsedAt := state.lastUsedAt
	state.mu.RUnlock()
	if done != nil {
		select {
		case <-done:
			return false
		default:
		}
	}
	if !lastUsedAt.IsZero() && time.Since(lastUsedAt) < mcpIdleProbeInterval {
		return true
	}
	ctx, cancel := context.WithTimeout(parent, mcpProbeTimeout)
	defer cancel()
	if err := session.Ping(ctx, nil); err != nil {
		// 只有「連線確定已死」才重建。慢的伺服器 ping 逾時是常態，把它當成斷線會在
		// 每次閒置後多付一次 initialize 與 tools/list 的成本，反而讓呼叫更慢；真正
		// 失效的 session 會在送出工具呼叫時被伺服器拒收，那條路徑已經會重連並重送。
		if !deliveryRejectedMCPError(err) {
			m.logger.Debug("MCP session probe did not answer; keeping the session",
				"server_id", stateID(state), "error", err)
			return true
		}
		m.logger.Info("MCP session probe reported a dead session; reconnecting", "server_id", stateID(state), "error", err)
		return false
	}
	state.mu.Lock()
	state.lastUsedAt = time.Now()
	state.mu.Unlock()
	return true
}

// watchSession 在連線結束時立刻標記狀態，不必等到下一次工具呼叫才發現。
func (m *Manager) watchSession(state *serverState, session *mcp.ClientSession, done chan struct{}) {
	go func() {
		_ = session.Wait()
		close(done)
		state.mu.Lock()
		current := state.session
		if current == session {
			state.session = nil
			state.tools = map[string]resolvedTool{}
			state.toolsDirty = state.config.Enabled
			if state.status == "connected" {
				state.status = "disconnected"
			}
			state.updatedAt = time.Now().UTC()
		}
		state.mu.Unlock()
	}()
}

// describeConnectError 把常見的憑證錯誤講清楚。使用者最容易誤判的情況就是
// 「金鑰已儲存卻連不上」，錯誤訊息必須指出目前實際送出的驗證方式。
func describeConnectError(config ServerConfig, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unauthorized") || strings.Contains(message, "401"):
		return fmt.Errorf("MCP 伺服器拒絕憑證（HTTP 401 Unauthorized）；目前送出的驗證方式是 %s：%w", authModeLabel(config), err)
	case strings.Contains(message, "forbidden") || strings.Contains(message, "403"):
		return fmt.Errorf("MCP 伺服器拒絕存取（HTTP 403 Forbidden）；目前送出的驗證方式是 %s：%w", authModeLabel(config), err)
	case strings.Contains(message, "404") || strings.Contains(message, "not found") ||
		strings.Contains(message, "405") || strings.Contains(message, "method not allowed"):
		// 只填主機、少了端點路徑（例如 /mcp）是最常見的設定錯誤，錯誤訊息要指出來，
		// 否則很容易被誤判成憑證問題。
		return fmt.Errorf("MCP 端點沒有回應 MCP 協定（HTTP 404／405）；請確認 %s 是否需要加上端點路徑，例如 /mcp：%w", config.URL, err)
	}
	return err
}

// AuthMode 回傳這個設定實際會送出的驗證方式，供管理介面顯示。
func (c ServerConfig) AuthMode() string {
	switch {
	case c.Transport == TransportStdio:
		return AuthModeNone
	case strings.TrimSpace(c.APIKey) != "":
		return AuthModeBearer
	case strings.TrimSpace(c.Username) != "" || c.Password != "":
		return AuthModeBasic
	case len(c.Headers) > 0:
		return AuthModeHeaders
	default:
		return AuthModeNone
	}
}

func authModeLabel(config ServerConfig) string {
	switch config.AuthMode() {
	case AuthModeBearer:
		return "Bearer Token"
	case AuthModeBasic:
		return "Basic Auth（帳號 " + config.Username + "）"
	case AuthModeHeaders:
		return "自訂 HTTP Headers"
	default:
		return "不使用驗證"
	}
}

// connect 建立 MCP 連線。Streamable HTTP 的 subscriptions/listen 是選用能力，
// 實務上仍有 Server 宣告可連線但收到訂閱請求就關閉 session；工具目錄本來就會
// 在每次 Run 的待刷新狀態下明確呼叫 tools/list，因此不主動開啟這個訂閱，避免
// 非必要的背景串流讓整個 client 進入 closing。
func (m *Manager) connect(ctx context.Context, state *serverState, config ServerConfig) (*mcp.ClientSession, error) {
	transport, err := m.transport(config)
	if err != nil {
		return nil, err
	}
	watchTools := config.Transport != TransportStreamableHTTP
	connected, err := mcp.NewClient(
		&mcp.Implementation{Name: m.clientName, Title: "NR-Intern MCP Host", Version: m.clientVersion},
		m.clientOptions(state, watchTools),
	).Connect(ctx, transport, nil)
	return connected, err
}

func (m *Manager) clientOptions(state *serverState, watchTools bool) *mcp.ClientOptions {
	options := &mcp.ClientOptions{
		Logger:       m.logger.With("mcp_server_id", stateID(state)),
		Capabilities: &mcp.ClientCapabilities{},
		// Keep multi-round-trip manual: the next input comes from the Agent／user
		// loop, not from an implicit empty elicitation handler. The returned
		// input_required payload is exposed to the model and can be retried with
		// _mcp_input_responses and _mcp_request_state.
		MultiRoundTrip:              &mcp.MultiRoundTripOptions{Disabled: true},
		ProgressNotificationHandler: m.handleProgress,
		// 定期 ping 讓閒置連線不被伺服器或反向代理默默回收，連線真的斷掉時也
		// 會即時關閉，由 watchSession 標記狀態。
		KeepAlive:                 mcpKeepAliveInterval,
		KeepAliveFailureThreshold: mcpKeepAliveFailures,
	}
	if watchTools {
		options.ToolListChangedHandler = func(context.Context, *mcp.ToolListChangedRequest) {
			state.mu.Lock()
			state.toolsDirty = true
			state.updatedAt = time.Now().UTC()
			state.mu.Unlock()
		}
	}
	return options
}
