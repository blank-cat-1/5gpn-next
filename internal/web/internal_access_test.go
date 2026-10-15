package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// Both browser and Mini App use the original network boundary, without a
// Telegram identity header; spoofed forwarding headers cannot bypass it.
func TestBrowserAndMiniAppRetainInternalAccessBoundary(t *testing.T) {
	panel, mgr := testPanel(t)
	panel.AllowFrom = []netip.Prefix{netip.MustParsePrefix("172.22.0.0/16")}
	h := panel.Handler()
	for _, endpoint := range []string{"/", "/style.css", "/app.js", "/api/status", "/api/rules", "/api/health"} {
		for _, source := range []struct {
			addr string
			code int
		}{
			{"172.22.9.8:12345", http.StatusOK},
			{"127.0.0.1:12345", http.StatusOK},
			{"203.0.113.5:12345", http.StatusForbidden},
		} {
			t.Run(endpoint+source.addr, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "https://gateway.example"+endpoint, nil)
				r.RemoteAddr = source.addr
				r.Header.Set("X-Forwarded-For", "172.22.9.8")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != source.code {
					t.Fatalf("status=%d want=%d", w.Code, source.code)
				}
			})
		}
	}
	for _, source := range []string{"203.0.113.5:12345", "172.22.9.8:12345"} {
		r := httptest.NewRequest(http.MethodPost, "https://gateway.example/api/adblock", bytes.NewBufferString(`{"action":"allow","domain":"safe.example"}`))
		r.RemoteAddr = source
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://gateway.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusOK
		if source == "203.0.113.5:12345" {
			want = http.StatusForbidden
			if len(mgr.AdAllowlist()) != 0 {
				t.Fatal("public request changed configuration")
			}
		}
		if w.Code != want {
			t.Fatalf("write from %s: status=%d body=%s", source, w.Code, w.Body)
		}
	}
	if got := mgr.AdAllowlist(); len(got) != 1 || got[0] != "safe.example" {
		t.Fatalf("internal browser write did not persist: %v", got)
	}
}

func TestRemovedRoutingEndpointCannotChangeOriginalBehavior(t *testing.T) {
	panel, mgr := testPanel(t)
	r := httptest.NewRequest(http.MethodPost, "/api/routing", bytes.NewBufferString(`{"mode":"direct"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	panel.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("removed mode endpoint status=%d", w.Code)
	}
	data, err := json.Marshal(mgr.Status("test"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"routing_mode"`)) {
		t.Fatal("status still advertises routing modes")
	}
}
