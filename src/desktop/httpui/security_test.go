package httpui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopProxyAuthorizationSmoke(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/backend/", func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(204) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	server := &Server{controlToken: "test-control", mux: mux}
	for _, tc := range []struct {
		name, host, origin, site string
		cookie                   bool
		status                   int
	}{
		{"no-cookie", "127.0.0.1:8790", "", "", false, 403},
		{"same-origin", "127.0.0.1:8790", "http://127.0.0.1:8790", "same-origin", true, 204},
		{"cross-origin", "127.0.0.1:8790", "http://127.0.0.1:9999", "", true, 403},
		{"cross-site", "127.0.0.1:8790", "", "cross-site", true, 403},
		{"rebound-host", "untrusted.example:8790", "", "", true, 403},
		{"null-origin", "127.0.0.1:8790", "null", "", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tc.host+"/backend/api/v1/runs", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: controlCookie, Value: "test-control"})
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d", w.Code, tc.status)
			}
		})
	}
	if calls != 1 {
		t.Fatalf("unauthorized proxy dispatch: %d calls", calls)
	}
	for _, path := range []string{"/", "/desktop/api/status", "/.well-known/nr-intern-desktop"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8790"+path, nil))
		if got := len(w.Result().Cookies()); (got > 0) != (path == "/") {
			t.Fatalf("unexpected cookie bootstrap for %s", path)
		}
	}
}
