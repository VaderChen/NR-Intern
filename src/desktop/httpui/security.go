package httpui

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// loopback 監聽不等同瀏覽器同源；避免 DNS rebinding 與跨站請求借用代理權限。
func localUIRequest(request *http.Request) bool {
	authority, err := url.Parse("http://" + request.Host)
	if err != nil || authority.User != nil || authority.Path != "" || authority.RawQuery != "" || authority.Fragment != "" {
		return false
	}
	host := authority.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	switch request.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		if err != nil || parsed.Scheme != scheme || !strings.EqualFold(parsed.Host, request.Host) ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false
		}
	}
	return true
}
