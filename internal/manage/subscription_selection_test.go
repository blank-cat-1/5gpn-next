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
	"strings"
	"testing"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/egress"
	"github.com/w0ven/5gpn-next/internal/policy"
)

func TestImportOnlySelectedCandidatesAndPersistCatalogue(t *testing.T) {
	m, runtime := subscriptionManager(t)
	feed := "socks5://user:SECRET-PASSWORD@tokyo.example:1080#Tokyo\nsocks5://singapore.example:1080#Singapore"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(feed)) }))
	defer srv.Close()
	if _, err := m.ChangeSubscription(context.Background(), "add", "", "test", srv.URL+"?token=SECRET-TOKEN"); err != nil {
		t.Fatal(err)
	}
	id := m.Cfg.Subscriptions[0].ID
	// The server can restart between saving a subscription and selecting nodes.
	loaded, err := config.Load(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Cfg = loaded
	list, err := m.SubscriptionCandidates(id)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), "SECRET-") || strings.Contains(string(encoded), "proxy") || strings.Contains(string(encoded), "password") {
		t.Fatal("candidate API leaks credentials")
	}
	if _, err := m.ImportSubscription(context.Background(), id, list.Revision, []string{list.Nodes[0].Key}); err != nil {
		t.Fatal(err)
	}
	if len(m.Cfg.Egress) != 3 || m.Cfg.Egress[2].DisplayName != "Tokyo" || bytes.Contains(runtime.data[id], []byte("singapore.example")) {
		t.Fatal("unselected node was activated")
	}
	nodeName, addr := m.Cfg.Egress[2].Name, m.Cfg.Egress[2].Addr
	if _, err := m.ImportSubscription(context.Background(), id, list.Revision, []string{list.Nodes[0].Key}); err != nil {
		t.Fatal(err)
	}
	if runtime.applies != 1 || len(m.Cfg.Egress) != 3 {
		t.Fatal("duplicate import restarted service or duplicated node")
	}
	feed += "\nsocks5://new.example:1080#New"
	if _, err := m.ChangeSubscription(context.Background(), "update", id, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(m.Cfg.Egress) != 3 || m.Cfg.Egress[2].Name != nodeName || m.Cfg.Egress[2].Addr != addr {
		t.Fatal("refresh changed imported subset or identity")
	}
	if _, err := m.ImportSubscription(context.Background(), id, list.Revision, []string{list.Nodes[1].Key}); err == nil {
		t.Fatal("stale catalogue import accepted")
	}
	current, _ := m.SubscriptionCandidates(id)
	if !current.Nodes[0].Imported || current.Nodes[1].Imported || current.Nodes[2].Imported {
		t.Fatal("imported flags incorrect")
	}
	for _, keys := range [][]string{nil, {"unknown"}, {current.Nodes[1].Key, current.Nodes[1].Key}} {
		if _, err := m.ImportSubscription(context.Background(), id, current.Revision, keys); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	if _, err := m.ImportSubscription(context.Background(), id, current.Revision, []string{current.Nodes[1].Key}); err != nil {
		t.Fatal(err)
	}
	if len(m.Cfg.Egress) != 4 || m.Cfg.Egress[2].Name != nodeName {
		t.Fatal("additional selection replaced previously imported node")
	}
}

func TestImportFailureRollsBackAndUnimportedSubscriptionNeedsNoRuntime(t *testing.T) {
	for _, failure := range []string{"start", "save", "reload"} {
		t.Run(failure, func(t *testing.T) {
			m, runtime := subscriptionManager(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("socks5://tokyo.example:1080#Tokyo"))
			}))
			defer srv.Close()
			if _, err := m.ChangeSubscription(context.Background(), "add", "", "test", srv.URL); err != nil {
				t.Fatal(err)
			}
			list, _ := m.SubscriptionCandidates(m.Cfg.Subscriptions[0].ID)
			path := m.ConfigPath
			before, _ := os.ReadFile(path)
			runtime.failApply = failure == "start"
			if failure == "save" {
				m.ConfigPath = filepath.Join(t.TempDir(), "missing", "config.json")
			}
			if failure == "reload" {
				m.Reload = func() (*policy.Engine, *egress.Registry, error) { return nil, nil, errors.New("private output") }
			}
			if _, err := m.ImportSubscription(context.Background(), list.ID, list.Revision, []string{list.Nodes[0].Key}); err == nil {
				t.Fatal("failed import succeeded")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) || len(m.Cfg.Egress) != 2 || len(runtime.data) != 0 || len(m.Cfg.Subscriptions[0].Candidates) != 1 {
				t.Fatal("failed import changed existing config or lost catalogue")
			}
			if failure != "start" && runtime.undos != 1 {
				t.Fatal("node runtime was not restored")
			}
		})
	}
	m, runtime := subscriptionManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("socks5://tokyo.example:1080#Tokyo"))
	}))
	defer srv.Close()
	if _, err := m.ChangeSubscription(context.Background(), "add", "", "test", srv.URL); err != nil {
		t.Fatal(err)
	}
	runtime.failRemove = true
	if _, err := m.ChangeSubscription(context.Background(), "remove", m.Cfg.Subscriptions[0].ID, "", ""); err != nil {
		t.Fatal("unimported subscription attempted to stop a service")
	}
}
