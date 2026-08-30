package network

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestTool(t *testing.T, settings domain.HTTPFetchSettings, options Options) *Tool {
	t.Helper()
	return New(options, settings)
}

func execute(t *testing.T, tool *Tool, arguments map[string]any) domain.ToolExecution {
	t.Helper()
	result, err := tool.Execute(context.Background(), tools.Invocation{
		Session: domain.Session{ID: "session_1", PermissionProfile: "default"},
		Call:    domain.ToolCall{ID: "call_1", Name: "http_fetch", Arguments: arguments},
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return result
}

// httptest 只監聽 loopback，因此測試一律開啟私有網段；關閉時的行為另有測試。
func localSettings() domain.HTTPFetchSettings {
	return domain.HTTPFetchSettings{Enabled: true, AllowPrivateNetworks: true}
}

func TestFetchExtractsReadableTextFromHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(writer, `<html><head><title>發行說明</title><style>.a{color:red}</style></head>
<body><script>console.log("secret")</script><h1>1.2.0</h1><p>修正排程時間軸。</p><p>新增 http_fetch。</p></body></html>`)
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{})

	result := execute(t, tool, map[string]any{"url": server.URL})

	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	for _, unwanted := range []string{"console.log", "color:red", "<p>"} {
		if strings.Contains(result.Content, unwanted) {
			t.Fatalf("content still carries markup %q: %s", unwanted, result.Content)
		}
	}
	for _, wanted := range []string{"發行說明", "1.2.0", "修正排程時間軸。", "新增 http_fetch。"} {
		if !strings.Contains(result.Content, wanted) {
			t.Fatalf("content is missing %q: %s", wanted, result.Content)
		}
	}
	if result.Details["title"] != "發行說明" {
		t.Fatalf("title = %v", result.Details["title"])
	}
}

func TestFetchKeepsRawHTMLWhenAsked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		fmt.Fprint(writer, "<p>hello</p>")
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{})

	result := execute(t, tool, map[string]any{"url": server.URL, "strip_html": false})

	if !strings.Contains(result.Content, "<p>hello</p>") {
		t.Fatalf("raw html was not preserved: %s", result.Content)
	}
}

// 私有網段的判斷在 Dial 階段，因此 DNS 指向內網或轉址到內網都會被同一條規則擋下。
func TestFetchRefusesPrivateAddressByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, "internal")
	}))
	defer server.Close()
	tool := newTestTool(t, domain.HTTPFetchSettings{Enabled: true}, Options{})

	result := execute(t, tool, map[string]any{"url": server.URL})

	if !result.IsError || !strings.Contains(result.Content, "private address") {
		t.Fatalf("expected private address refusal, got: %s", result.Content)
	}
}

func TestFetchRefusedWhenTurnedOff(t *testing.T) {
	tool := newTestTool(t, domain.HTTPFetchSettings{Enabled: false}, Options{})
	if tool.Enabled() {
		t.Fatal("tool must report itself as disabled")
	}
	result := execute(t, tool, map[string]any{"url": "https://example.com"})
	if !result.IsError || !strings.Contains(result.Content, "turned off") {
		t.Fatalf("expected disabled refusal, got: %s", result.Content)
	}
}

func TestApplySettingsTakesEffectImmediately(t *testing.T) {
	tool := newTestTool(t, domain.HTTPFetchSettings{Enabled: false}, Options{})
	tool.ApplySettings(domain.HTTPFetchSettings{Enabled: true, AllowPrivateNetworks: true})
	if !tool.Enabled() || !tool.Settings().AllowPrivateNetworks {
		t.Fatalf("settings were not applied: %+v", tool.Settings())
	}
}

func TestFetchTruncatesLargeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(writer, strings.Repeat("a", 40_000))
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{MaxResponseBytes: 4096})

	result := execute(t, tool, map[string]any{"url": server.URL})

	if result.Details["truncated"] != true {
		t.Fatalf("expected truncation, details: %+v", result.Details)
	}
	if bytes, _ := result.Details["bytes"].(int); bytes != 4096 {
		t.Fatalf("bytes = %v, want 4096", result.Details["bytes"])
	}
	if !strings.Contains(result.Content, "[response truncated]") {
		t.Fatal("truncation marker is missing")
	}
}

func TestFetchDoesNotReturnBinaryContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{})

	result := execute(t, tool, map[string]any{"url": server.URL})

	if result.Details["binary"] != true || !strings.Contains(result.Content, "non-text content type") {
		t.Fatalf("binary content was not refused: %s", result.Content)
	}
}

func TestFetchReportsHTTPErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		fmt.Fprint(writer, `{"error":"not found"}`)
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{})

	result := execute(t, tool, map[string]any{"url": server.URL})

	if !result.IsError {
		t.Fatal("4xx must be reported as a failed tool result")
	}
	if !strings.Contains(result.Content, "not found") {
		t.Fatalf("response body should still be returned: %s", result.Content)
	}
}

func TestFetchSendsHeadersAndBody(t *testing.T) {
	observed := make(chan *http.Request, 1)
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 64)
		count, _ := request.Body.Read(buffer)
		body = string(buffer[:count])
		observed <- request
		writer.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(writer, "ok")
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{})

	execute(t, tool, map[string]any{
		"url":     server.URL,
		"method":  "POST",
		"headers": map[string]any{"X-Token": "abc", "Host": "evil.example", "X-Bad": "line\nbreak"},
		"body":    `{"q":"1"}`,
	})

	request := <-observed
	if request.Header.Get("X-Token") != "abc" {
		t.Fatalf("custom header was not sent: %v", request.Header)
	}
	if request.Header.Get("X-Bad") != "" {
		t.Fatal("header values with line breaks must be dropped")
	}
	if strings.Contains(request.Host, "evil.example") {
		t.Fatalf("Host header must not be overridable: %s", request.Host)
	}
	if request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", request.Header.Get("Content-Type"))
	}
	if body != `{"q":"1"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestFetchRejectsUnsupportedInput(t *testing.T) {
	tool := newTestTool(t, localSettings(), Options{})
	cases := map[string]map[string]any{
		"missing url":        {},
		"file scheme":        {"url": "file:///etc/passwd"},
		"unsupported method": {"url": "https://example.com", "method": "DELETE"},
		"body without post":  {"url": "https://example.com", "body": "x"},
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			if result := execute(t, tool, arguments); !result.IsError {
				t.Fatalf("expected refusal, got: %s", result.Content)
			}
		})
	}
}

func TestFetchAppliesHostLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, "ok")
	}))
	defer server.Close()

	blocked := newTestTool(t, localSettings(), Options{BlockedHosts: []string{"127.0.0.1"}})
	if result := execute(t, blocked, map[string]any{"url": server.URL}); !strings.Contains(result.Content, "block list") {
		t.Fatalf("blocked host was not refused: %s", result.Content)
	}

	restricted := newTestTool(t, localSettings(), Options{AllowedHosts: []string{"example.com"}})
	if result := execute(t, restricted, map[string]any{"url": server.URL}); !strings.Contains(result.Content, "allow list") {
		t.Fatalf("host outside the allow list was not refused: %s", result.Content)
	}
	if result := execute(t, restricted, map[string]any{"url": "https://docs.example.com/x"}); strings.Contains(result.Content, "allow list") {
		t.Fatalf("subdomain of an allowed host must pass the host check: %s", result.Content)
	}
}

func TestFetchStopsAfterRedirectLimit(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, server.URL+"/next", http.StatusFound)
	}))
	defer server.Close()
	tool := newTestTool(t, localSettings(), Options{MaxRedirects: 2})

	result := execute(t, tool, map[string]any{"url": server.URL})

	if !result.IsError || !strings.Contains(result.Content, "redirects") {
		t.Fatalf("expected redirect limit failure, got: %s", result.Content)
	}
}

func TestIsPublicIPRejectsInternalRanges(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.5", "172.16.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "0.0.0.0", "198.18.0.1"} {
		if isPublicIP(parseIP(t, address)) {
			t.Fatalf("%s must not be treated as public", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !isPublicIP(parseIP(t, address)) {
			t.Fatalf("%s must be treated as public", address)
		}
	}
}

func parseIP(t *testing.T, value string) net.IP {
	t.Helper()
	ip := net.ParseIP(value)
	if ip == nil {
		t.Fatalf("parse ip %q", value)
	}
	return ip
}
