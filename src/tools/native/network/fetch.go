package network

import (
	"AgenticService/src/domain"
	"AgenticService/src/ports"
	"AgenticService/src/tools"
	"AgenticService/src/tools/native/internal/toolutil"
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultMaxResponseBytes = 1024 * 1024
	defaultTimeoutSeconds   = 30
	defaultMaxRedirects     = 5
	maxRequestBodyBytes     = 256 * 1024
)

// Options 是只能由設定檔調整的靜態邊界；管理介面調整的開關放在 domain.HTTPFetchSettings。
type Options struct {
	MaxResponseBytes int
	TimeoutSeconds   int
	MaxRedirects     int
	// AllowedHosts 非空時只允許清單內的網域；BlockedHosts 一律優先拒絕。
	AllowedHosts []string
	BlockedHosts []string
}

// Tool 是 http_fetch：Agent 唯一的對外網路入口。
//
// 其他原生工具都在本機沙箱內；這個工具會把資料送出去，因此除了 elevated 權限與
// 人工 Approval 之外，還可以在管理介面直接關閉，不必改設定檔或重啟後端。
type Tool struct {
	options  Options
	settings atomic.Pointer[domain.HTTPFetchSettings]
	client   *http.Client
}

var _ tools.ToggleableTool = (*Tool)(nil)

func New(options Options, settings domain.HTTPFetchSettings) *Tool {
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = defaultMaxResponseBytes
	}
	if options.TimeoutSeconds <= 0 {
		options.TimeoutSeconds = defaultTimeoutSeconds
	}
	if options.MaxRedirects < 0 {
		options.MaxRedirects = defaultMaxRedirects
	}
	tool := &Tool{options: options}
	tool.ApplySettings(settings)
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: tool.controlDial}
	tool.client = &http.Client{
		// 刻意不使用系統 Proxy 設定：經過 Proxy 後實際連線目標不再是網址的主機，
		// 私有網段檢查就會失去意義。
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: time.Duration(options.TimeoutSeconds) * time.Second,
			MaxIdleConns:          8,
			IdleConnTimeout:       60 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: tool.checkRedirect,
	}
	return tool
}

// ApplySettings 讓管理介面的變更立即生效，不需要重新啟動後端。
func (t *Tool) ApplySettings(settings domain.HTTPFetchSettings) {
	value := settings
	t.settings.Store(&value)
}

func (t *Tool) Settings() domain.HTTPFetchSettings {
	if value := t.settings.Load(); value != nil {
		return *value
	}
	return domain.HTTPFetchSettings{}
}

func (t *Tool) Enabled() bool { return t.Settings().Enabled }

func (t *Tool) DisabledReason() string {
	return "http_fetch is turned off in the backend service settings"
}

func (t *Tool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:               "http_fetch",
		Label:              "讀取網路資源",
		Version:            "1.0.0",
		Category:           "network",
		Description:        "以 HTTP 讀取外部資源並回傳文字內容。HTML 會轉成純文字，回應大小與轉址次數都有上限；預設允許連線到 localhost 與私有網段，可由服務設定關閉。",
		Platforms:          []string{"darwin", "linux", "windows"},
		Capabilities:       []string{"http", "html-to-text", "bounded-response", "redirect-limit", "private-network-guard"},
		RequiresPermission: true,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":             map[string]any{"type": "string", "description": "http 或 https 網址"},
				"method":          map[string]any{"type": "string", "enum": []string{"GET", "HEAD", "POST"}, "default": "GET"},
				"headers":         map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "額外的 request header"},
				"body":            map[string]any{"type": "string", "description": "只供 POST 使用；未指定 Content-Type 時預設 application/json"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": t.options.TimeoutSeconds, "default": t.options.TimeoutSeconds},
				"max_bytes":       map[string]any{"type": "integer", "minimum": 1024, "maximum": t.options.MaxResponseBytes, "default": t.options.MaxResponseBytes},
				"strip_html":      map[string]any{"type": "boolean", "default": true, "description": "false 時回傳原始 HTML"},
			},
			"required": []string{"url"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, invocation tools.Invocation, _ ports.ToolUpdateSink) (domain.ToolExecution, error) {
	call := invocation.Call
	if !t.Enabled() {
		return failure(call, t.DisabledReason(), nil), nil
	}
	arguments := call.Arguments
	rawURL := toolutil.String(arguments, "url")
	if rawURL == "" {
		return failure(call, "url is required", nil), nil
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return failure(call, fmt.Sprintf("invalid url: %v", err), nil), nil
	}
	if err := t.checkURL(target); err != nil {
		return failure(call, err.Error(), nil), nil
	}
	method := strings.ToUpper(strings.TrimSpace(toolutil.String(arguments, "method")))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost {
		return failure(call, fmt.Sprintf("unsupported method %q: only GET, HEAD and POST are allowed", method), nil), nil
	}
	body := toolutil.String(arguments, "body")
	if body != "" && method != http.MethodPost {
		return failure(call, "body is only supported with POST", nil), nil
	}
	if len(body) > maxRequestBodyBytes {
		return failure(call, fmt.Sprintf("body exceeds %d bytes", maxRequestBodyBytes), nil), nil
	}
	timeoutSeconds := toolutil.Int(arguments, "timeout_seconds", t.options.TimeoutSeconds, 1, t.options.TimeoutSeconds)
	maxBytes := toolutil.Int(arguments, "max_bytes", t.options.MaxResponseBytes, 1024, t.options.MaxResponseBytes)

	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, target.String(), payload)
	if err != nil {
		return failure(call, err.Error(), nil), nil
	}
	applyHeaders(request, arguments["headers"])
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "NR-Intern/1.0 (+http_fetch)")
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "text/html,application/json;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	}
	if body != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}

	startedAt := time.Now()
	response, err := t.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil && ctx.Err() != nil {
			return domain.ToolExecution{}, ctx.Err()
		}
		return failure(call, requestFailureMessage(err, timeoutSeconds, requestCtx.Err()), map[string]any{"url": target.String(), "method": method}), nil
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
	if err != nil && ctx.Err() != nil {
		return domain.ToolExecution{}, ctx.Err()
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	duration := time.Since(startedAt)

	mediaType, charset := contentType(response.Header.Get("Content-Type"))
	details := map[string]any{
		"url":          response.Request.URL.String(),
		"method":       method,
		"status":       response.StatusCode,
		"content_type": strings.TrimSpace(response.Header.Get("Content-Type")),
		"bytes":        len(data),
		"truncated":    truncated,
		"duration_ms":  duration.Milliseconds(),
	}
	if response.Request.URL.String() != target.String() {
		details["requested_url"] = target.String()
	}
	header := fmt.Sprintf("%s %s → %d %s", method, response.Request.URL.String(), response.StatusCode, http.StatusText(response.StatusCode))
	if err != nil {
		details["read_error"] = err.Error()
	}

	if method == http.MethodHead {
		return t.result(call, header+"\n"+formatHeaders(response.Header), details, response.StatusCode), nil
	}
	if !isTextual(mediaType) {
		details["binary"] = true
		content := fmt.Sprintf("%s\n[non-text content type %q was not returned; %d bytes received]", header, mediaType, len(data))
		return t.result(call, content, details, response.StatusCode), nil
	}
	text := string(data)
	if charset != "" && !isUTF8Charset(charset) {
		details["charset"] = charset
	}
	if isHTML(mediaType) && toolutil.Bool(arguments, "strip_html", true) {
		title, extracted := htmlToText(text)
		if title != "" {
			details["title"] = title
			header += "\ntitle: " + title
		}
		details["html_extracted"] = true
		text = extracted
	}
	text = strings.TrimSpace(text)
	if truncated {
		text += "\n[response truncated]"
	}
	if text == "" {
		text = "[empty response body]"
	}
	return t.result(call, header+"\n\n"+text, details, response.StatusCode), nil
}

// result 把 4xx／5xx 視為失敗，但仍附上回應內容：狀態碼通常要配合 body 才看得出原因。
func (t *Tool) result(call domain.ToolCall, content string, details map[string]any, status int) domain.ToolExecution {
	return domain.ToolExecution{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    content,
		Details:    details,
		IsError:    status >= 400,
	}
}

func (t *Tool) checkRedirect(request *http.Request, via []*http.Request) error {
	if len(via) > t.options.MaxRedirects {
		return fmt.Errorf("stopped after %d redirects", t.options.MaxRedirects)
	}
	return t.checkURL(request.URL)
}

func applyHeaders(request *http.Request, raw any) {
	values, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" || isRestrictedHeader(key) {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		// 換行會讓 header 值變成額外的 header；這裡直接拒絕，不做拆解。
		if text == "" || strings.ContainsAny(text, "\r\n") {
			continue
		}
		request.Header.Set(key, text)
	}
}

// isRestrictedHeader 擋下由 Transport 自行管理或會改變連線語意的 header。
func isRestrictedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding", "upgrade",
		"proxy-authorization", "proxy-connection", "te", "trailer", "keep-alive":
		return true
	}
	return false
}

func contentType(value string) (mediaType string, charset string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	parsed, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		if index := strings.IndexByte(value, ';'); index > 0 {
			return strings.ToLower(strings.TrimSpace(value[:index])), ""
		}
		return strings.ToLower(value), ""
	}
	return strings.ToLower(parsed), strings.ToLower(strings.TrimSpace(parameters["charset"]))
}

func isTextual(mediaType string) bool {
	if mediaType == "" {
		return true
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	if strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") {
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/javascript", "application/ecmascript",
		"application/x-ndjson", "application/yaml", "application/x-yaml", "application/graphql",
		"application/x-www-form-urlencoded":
		return true
	}
	return false
}

func isHTML(mediaType string) bool {
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func isUTF8Charset(charset string) bool {
	switch charset {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return true
	}
	return false
}

func formatHeaders(header http.Header) string {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+": "+strings.Join(header.Values(name), ", "))
	}
	return strings.Join(lines, "\n")
}

func requestFailureMessage(err error, timeoutSeconds int, ctxErr error) string {
	if ctxErr == context.DeadlineExceeded {
		return fmt.Sprintf("request timed out after %d seconds", timeoutSeconds)
	}
	return err.Error()
}

func failure(call domain.ToolCall, message string, details map[string]any) domain.ToolExecution {
	return domain.ToolExecution{ToolCallID: call.ID, ToolName: call.Name, Content: strings.TrimSpace(message), Details: details, IsError: true}
}
