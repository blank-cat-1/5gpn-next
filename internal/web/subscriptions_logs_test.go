package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/w0ven/5gpn-next/internal/config"
)

func TestNewManagementAPIsKeepNetworkAndCSRFGuards(t *testing.T) {
	p, m := testPanel(t)
	p.AllowFrom = []netip.Prefix{netip.MustParsePrefix("172.22.0.0/16")}
	m.Cfg.Subscriptions = []config.SubscriptionConfig{{ID: "sub-0123456789abcdef", Name: "test", URL: "https://feed.example/private?token=TOKEN-secret"}}
	h := p.Handler()
	for _, endpoint := range []string{"/api/subscriptions", "/api/subscriptions?id=sub-0123456789abcdef", "/api/logs?lines=200", "/api/egress-latency"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			r := httptest.NewRequest(method, "https://gateway.example"+endpoint, bytes.NewBufferString(`{"action":"remove","id":"sub-0123456789abcdef"}`))
			r.RemoteAddr = "203.0.113.1:1234"
			r.Header.Set("X-Forwarded-For", "172.22.1.2")
			r.Header.Set("Origin", "https://gateway.example")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("public source bypassed guard: %s %s=%d", method, endpoint, w.Code)
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, "https://gateway.example/api/subscriptions", nil)
	r.RemoteAddr = "172.22.1.2:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "feed.example") || strings.Contains(w.Body.String(), "TOKEN-secret") || strings.Contains(w.Body.String(), "/private") {
		t.Fatalf("unsafe subscription list: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest(http.MethodPost, "https://gateway.example/api/subscriptions", bytes.NewBufferString(`{"action":"remove","id":"sub-0123456789abcdef"}`))
	r.RemoteAddr = "172.22.1.2:1234"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(m.Cfg.Subscriptions) != 1 {
		t.Fatal("cross-origin write passed")
	}
	for _, query := range []string{"0", "501", "-1", "200;id", "bad"} {
		r := httptest.NewRequest(http.MethodGet, "https://gateway.example/api/logs?lines="+query, nil)
		r.RemoteAddr = "172.22.1.2:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid log limit %q=%d", query, w.Code)
		}
	}
}
