package mcpclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (m *Manager) transport(config ServerConfig) (mcp.Transport, error) {
	switch config.Transport {
	case TransportStdio:
		command := exec.Command(config.Command, config.Args...)
		command.Dir = config.WorkDir
		command.Env = safeChildEnvironment(config.Environment)
		return &mcp.CommandTransport{Command: command, TerminateDuration: 4 * time.Second}, nil
	case TransportStreamableHTTP:
		client := newMCPHTTPClient(config)
		return &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client, MaxRetries: 2, DisableStandaloneSSE: true}, nil
	case TransportSSE:
		client := newMCPHTTPClient(config)
		return &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: client}, nil
	default:
		return nil, fmt.Errorf("不支援的 MCP transport %q", config.Transport)
	}
}

func safeChildEnvironment(extra map[string]string) []string {
	allowed := []string{"HOME", "LANG", "LC_ALL", "LOGNAME", "PATH", "SHELL", "TMPDIR", "USER"}
	values := make(map[string]string, len(allowed)+len(extra))
	for _, key := range allowed {
		if value, exists := os.LookupEnv(key); exists {
			values[key] = value
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

type headerTransport struct {
	base     http.RoundTripper
	apiKey   string
	username string
	password string
	headers  map[string]string
}

func (t *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	// Apply credentials after custom headers so an explicitly configured key is
	// also authoritative for the initialize request and every later MCP call.
	if t.apiKey != "" {
		clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	} else if t.username != "" || t.password != "" {
		clone.SetBasicAuth(t.username, t.password)
	}
	return t.base.RoundTrip(clone)
}

func newMCPHTTPClient(config ServerConfig) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	return &http.Client{
		Transport: &headerTransport{
			base:     base,
			apiKey:   config.APIKey,
			username: config.Username,
			password: config.Password,
			headers:  config.Headers,
		},
		CheckRedirect: preserveMCPPostRedirect,
	}
}

// preserveMCPPostRedirect prevents a 301/302 endpoint-slash redirect from
// changing MCP's JSON-RPC POST into GET and dropping the initialize body.
// Redirects to another host are rejected so credentials cannot leave the
// configured MCP endpoint.
func preserveMCPPostRedirect(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1]
	if previous.URL == nil || request.URL == nil || !safeMCPRedirect(previous.URL, request.URL) {
		return fmt.Errorf("MCP HTTP redirect changed endpoint origin")
	}
	if previous.Method != http.MethodPost || request.Method != http.MethodGet {
		return nil
	}
	if previous.GetBody == nil {
		return fmt.Errorf("MCP HTTP redirect cannot replay request body")
	}
	body, err := previous.GetBody()
	if err != nil {
		return fmt.Errorf("MCP HTTP redirect replay body: %w", err)
	}
	request.Method = previous.Method
	request.Body = body
	request.GetBody = previous.GetBody
	request.ContentLength = previous.ContentLength
	request.Header = previous.Header.Clone()
	return nil
}

func safeMCPRedirect(from, to *url.URL) bool {
	if !strings.EqualFold(from.Host, to.Host) {
		return false
	}
	if strings.EqualFold(from.Scheme, to.Scheme) {
		return true
	}
	return strings.EqualFold(from.Scheme, "http") && strings.EqualFold(to.Scheme, "https")
}
