package network

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// 私有網段檢查在 Dial 階段執行，因此 DNS 重新綁定（rebinding）與轉址都會被同一個
// 判斷攔下：無論名稱解析結果如何，實際連線的 IP 一定經過這裡。
func (t *Tool) controlDial(_, address string, _ syscall.RawConn) error {
	if t.Settings().AllowPrivateNetworks {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("resolve dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refused dial to %q: address is not an IP", host)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("refused connection to private address %s: enable allow_private_networks to reach it", ip)
	}
	return nil
}

// isPublicIP 只放行可路由的公開位址。除了 loopback 與 RFC1918，也擋下 link-local
// （含雲端 metadata 服務使用的 169.254.169.254）、CGNAT 與 multicast。
func isPublicIP(ip net.IP) bool {
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if value := ip.To4(); value != nil {
		switch {
		case value[0] == 0:
			return false
		case value[0] == 100 && value[1] >= 64 && value[1] <= 127: // 100.64.0.0/10 CGNAT
			return false
		case value[0] == 192 && value[1] == 0 && value[2] == 0: // 192.0.0.0/24 IETF protocol assignments
			return false
		case value[0] == 198 && (value[1] == 18 || value[1] == 19): // 198.18.0.0/15 benchmarking
			return false
		}
	}
	return true
}

// checkURL 在送出 request 與每一次轉址時都會執行。
func (t *Tool) checkURL(target *url.URL) error {
	if target == nil {
		return fmt.Errorf("url is required")
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q: only http and https are allowed", target.Scheme)
	}
	host := strings.ToLower(target.Hostname())
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	for _, pattern := range t.options.BlockedHosts {
		if hostMatches(host, pattern) {
			return fmt.Errorf("host %q is on the backend block list", host)
		}
	}
	if len(t.options.AllowedHosts) == 0 {
		return nil
	}
	for _, pattern := range t.options.AllowedHosts {
		if hostMatches(host, pattern) {
			return nil
		}
	}
	return fmt.Errorf("host %q is not on the backend allow list", host)
}

// hostMatches 接受完全相同的主機名，或該網域底下的子網域。
func hostMatches(host, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(pattern, "*.")))
	if pattern == "" {
		return false
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}
