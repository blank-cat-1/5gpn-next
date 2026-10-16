package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/egress"
	"github.com/w0ven/5gpn-next/internal/policy"
)

type fakeSubscriptionRuntime struct {
	data                  map[string][]byte
	failApply, failRemove bool
	applies, undos        int
}

func (f *fakeSubscriptionRuntime) Apply(id string, data []byte, _ []string) (func() error, error) {
	if f.failApply {
		return nil, errors.New("test startup failure")
	}
	old, exists := f.data[id]
	f.data[id] = append([]byte(nil), data...)
	f.applies++
	return func() error {
		f.undos++
		if exists {
			f.data[id] = old
		} else {
			delete(f.data, id)
		}
		return nil
	}, nil
}
func (f *fakeSubscriptionRuntime) Remove(id string) error {
	if f.failRemove {
		return errors.New("test stop failure")
	}
	delete(f.data, id)
	return nil
}

func subscriptionManager(t *testing.T) (*Manager, *fakeSubscriptionRuntime) {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.CertFile = "test-cert.pem"
	cfg.Gateway.KeyFile = "test-key.pem"
	cfg.DNS.GatewayIP = "172.22.0.1"
	cfg.Egress = append(cfg.Egress, config.EgressConfig{Name: "manual", Type: "socks5", Addr: "127.0.0.1:7895", DisplayName: "原有节点"})
	cfg.Final = "proxy:manual"
	cfg.Rules = []string{"DOMAIN-SUFFIX,keep.example,proxy:manual"}
	cfg.DNS.HotspotSSIDs = []string{"Example Hotspot"}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	m := New(path, cfg, policy.New(), egress.NewRegistry())
	f := &fakeSubscriptionRuntime{data: map[string][]byte{}}
	m.subRuntime = f
	return m, f
}

func TestSubscriptionLifecyclePreservesOriginalAndStableNodeReferences(t *testing.T) {
	m, runtime := subscriptionManager(t)
	originalRules := append([]string(nil), m.Cfg.Rules...)
	feed := "socks5://user:secret@a.example:1080#Tokyo\nsocks5://b.example:1080#Singapore"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "Clash/5gpn-next" {
			t.Error("missing Clash UA")
		}
		_, _ = w.Write([]byte(feed))
	}))
	defer srv.Close()
	if _, err := m.ChangeSubscription(context.Background(), "add", "", "我的订阅", srv.URL+"/private?token=TOKEN-secret"); err != nil {
		t.Fatal(err)
	}
	if len(m.Cfg.Egress) != 2 || len(m.Cfg.Subscriptions) != 1 || runtime.applies != 0 {
		t.Fatal("saving subscription activated unselected nodes")
	}
	id := m.Cfg.Subscriptions[0].ID
	candidates, err := m.SubscriptionCandidates(id)
	if err != nil || len(candidates.Nodes) != 2 {
		t.Fatal("missing saved candidates")
	}
	if _, err := m.ImportSubscription(context.Background(), id, candidates.Revision, []string{candidates.Nodes[0].Key, candidates.Nodes[1].Key}); err != nil {
		t.Fatal(err)
	}
	tokyo, singapore := m.Cfg.Egress[2], m.Cfg.Egress[3]
	if tokyo.Addr != "127.0.0.1:7896" || tokyo.SubscriptionID != id {
		t.Fatal("port or metadata mismatch")
	}
	m.Cfg.Final = "proxy:" + tokyo.Name
	m.Cfg.Rules = append(m.Cfg.Rules, "DOMAIN-SUFFIX,pin.example,proxy:"+tokyo.Name)
	if err := m.Cfg.Save(m.ConfigPath); err != nil {
		t.Fatal(err)
	}
	feed = "socks5://b.example:1080#Singapore\nsocks5://changed.example:1081#Tokyo\nsocks5://c.example:1080#New"
	if _, err := m.ChangeSubscription(context.Background(), "update", id, "", ""); err != nil {
		t.Fatal(err)
	}
	if m.Cfg.Egress[2].Name != singapore.Name || m.Cfg.Egress[3].Name != tokyo.Name || m.Cfg.Egress[3].Addr != tokyo.Addr || m.Cfg.Final != "proxy:"+tokyo.Name {
		t.Fatal("refresh changed stable identifiers, listener or selection")
	}
	if m.Cfg.Egress[3].Server != "changed.example:1081" {
		t.Fatal("node endpoint not refreshed")
	}
	if len(m.Cfg.Egress) != 4 || len(m.Cfg.Subscriptions[0].Candidates) != 3 {
		t.Fatal("refresh auto-imported a newly discovered node")
	}
	public, _ := json.Marshal(m.Subscriptions())
	if strings.Contains(string(public), "TOKEN-secret") || strings.Contains(string(public), "/private") || strings.Contains(string(public), "user:secret") {
		t.Fatal("subscription API exposes credentials")
	}
	before, _ := os.ReadFile(m.ConfigPath)
	feed = "socks5://b.example:1080#Singapore"
	if _, err := m.ChangeSubscription(context.Background(), "update", id, "", ""); err == nil {
		t.Fatal("removed a pinned node")
	}
	after, _ := os.ReadFile(m.ConfigPath)
	if !bytes.Equal(before, after) || runtime.applies != 2 {
		t.Fatal("failed update mutated state")
	}
	if _, err := m.ChangeSubscription(context.Background(), "remove", id, "", ""); err == nil {
		t.Fatal("removed subscription referenced by rules")
	}
	if err := m.RemoveEgress(tokyo.Name); err == nil {
		t.Fatal("individual deletion could stop shared subscription service")
	}
	m.Cfg.Final = "proxy:manual"
	m.Cfg.Rules = originalRules
	if err := m.Cfg.Save(m.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ChangeSubscription(context.Background(), "remove", id, "", ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Subscriptions) != 0 || len(loaded.Egress) != 2 || loaded.Egress[1].Name != "manual" || !reflect.DeepEqual(loaded.Rules, originalRules) || loaded.Final != "proxy:manual" || loaded.DNS.HotspotSSIDs[0] != "Example Hotspot" || len(runtime.data) != 0 {
		t.Fatal("original configuration was changed")
	}
}

func TestSubscriptionFailuresRollbackDiskAndRuntime(t *testing.T) {
	for _, failure := range []string{"fetch", "parse", "save", "reload"} {
		t.Run(failure, func(t *testing.T) {
			m, runtime := subscriptionManager(t)
			before, _ := os.ReadFile(m.ConfigPath)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "fetch" {
					http.Error(w, "TOKEN-private", 403)
					return
				}
				if failure == "parse" {
					_, _ = w.Write([]byte("<html>TOKEN-private</html>"))
					return
				}
				_, _ = w.Write([]byte("socks5://a.example:1080#Tokyo"))
			}))
			defer srv.Close()
			runtime.failApply = failure == "start"
			if failure == "reload" {
				m.Reload = func() (*policy.Engine, *egress.Registry, error) { return nil, nil, errors.New("TOKEN-private") }
			}
			path := m.ConfigPath
			if failure == "save" {
				m.ConfigPath = filepath.Join(t.TempDir(), "missing", "config.json")
			}
			_, err := m.ChangeSubscription(context.Background(), "add", "", "test", srv.URL+"?token=TOKEN-private")
			if err == nil {
				t.Fatal("failure unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), "TOKEN-private") {
				t.Fatal("error leaked credentials")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) || len(m.Cfg.Egress) != 2 || len(m.Cfg.Subscriptions) != 0 || len(runtime.data) != 0 {
				t.Fatal("failure left config/runtime modified")
			}
			if runtime.applies != 0 {
				t.Fatal("saving a catalogue touched the node runtime")
			}
		})
	}
}

func TestSubscriptionRemoveFailureAndRequestBoundaries(t *testing.T) {
	m, runtime := subscriptionManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("socks5://a.example:1080#Tokyo")) }))
	defer srv.Close()
	if _, err := m.ChangeSubscription(context.Background(), "add", "", "test", srv.URL); err != nil {
		t.Fatal(err)
	}
	candidates, _ := m.SubscriptionCandidates(m.Cfg.Subscriptions[0].ID)
	if _, err := m.ImportSubscription(context.Background(), candidates.ID, candidates.Revision, []string{candidates.Nodes[0].Key}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(m.ConfigPath)
	runtime.failRemove = true
	if _, err := m.ChangeSubscription(context.Background(), "remove", m.Cfg.Subscriptions[0].ID, "", ""); err == nil {
		t.Fatal("stop failure accepted")
	}
	after, _ := os.ReadFile(m.ConfigPath)
	if !bytes.Equal(before, after) || len(m.Cfg.Subscriptions) != 1 || len(runtime.data) != 1 {
		t.Fatal("remove failure changed state")
	}
	for _, raw := range []string{"file:///etc/passwd", "https://user:secret@example.com", "https://example.com/#fragment", "not a URL"} {
		if _, err := m.ChangeSubscription(context.Background(), "add", "", "", raw); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
	m.subMu.Lock()
	if _, err := m.ChangeSubscription(context.Background(), "add", "", "", srv.URL); err == nil {
		t.Fatal("simultaneous subscription mutation accepted")
	}
	m.subMu.Unlock()
}
