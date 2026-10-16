package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/w0ven/5gpn-next/internal/config"
)

func waitLatencyBatch(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !m.LatencyProgress().Running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("latency job did not finish")
}

func TestBulkLatencyIsBoundedReportsEachNodeAndNeverChangesConfig(t *testing.T) {
	m, _ := subscriptionManager(t)
	for i := 0; i < 9; i++ {
		m.Cfg.Egress = append(m.Cfg.Egress, config.EgressConfig{Name: fmt.Sprintf("node%d", i), Type: "socks5", Addr: fmt.Sprintf("127.0.0.1:%d", 7900+i)})
	}
	before, _ := os.ReadFile(m.ConfigPath)
	started := make(chan string, 16)
	release := make(chan struct{})
	var active, peak atomic.Int64
	m.latencyProbe = func(ctx context.Context, addr string) (time.Duration, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("probe lacks timeout")
		}
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		started <- addr
		<-release
		if addr == "127.0.0.1:7901" {
			return 0, errors.New("https://secret.invalid/?token=private")
		}
		return 87 * time.Millisecond, nil
	}
	state, err := m.StartEgressLatencyTests()
	if err != nil || !state.Running || state.Total != 10 {
		t.Fatalf("bad initial progress: %+v %v", state, err)
	}
	for i := 0; i < latencyConcurrency; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers not started")
		}
	}
	if _, err := m.StartEgressLatencyTests(); err == nil {
		t.Fatal("overlapping batch accepted")
	}
	select {
	case <-started:
		t.Fatal("more than four probes ran at once")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	waitLatencyBatch(t, m)
	state = m.LatencyProgress()
	if state.Done != 10 || peak.Load() > latencyConcurrency {
		t.Fatalf("incorrect final progress/concurrency: %+v %d", state, peak.Load())
	}
	status := m.Status("test")
	for _, e := range status.Egress {
		if e.Name == "DIRECT" {
			if e.Latency != nil {
				t.Fatal("batch tested DIRECT")
			}
			continue
		}
		if e.Latency == nil || e.Latency.CheckedAt == 0 {
			t.Fatal("missing per-node result")
		}
		if e.Name == "node1" {
			if e.Latency.State != "error" || e.Latency.MS != 0 {
				t.Fatal("failed node got a success delay")
			}
		} else if e.Latency.State != "ok" || e.Latency.MS != 87 {
			t.Fatal("incorrect measured delay")
		}
	}
	after, _ := os.ReadFile(m.ConfigPath)
	if !bytes.Equal(before, after) || m.Cfg.Final != "proxy:manual" || len(m.Cfg.Rules) != 1 {
		t.Fatal("latency measurement changed routing/config")
	}
}

func TestLatencyIgnoresStaleResultsAndIndividualTestUpdatesDisplay(t *testing.T) {
	m, _ := subscriptionManager(t)
	started := make(chan struct{})
	release := make(chan struct{})
	m.latencyProbe = func(context.Context, string) (time.Duration, error) {
		close(started)
		<-release
		return 50 * time.Millisecond, nil
	}
	if _, err := m.StartEgressLatencyTests(); err != nil {
		t.Fatal(err)
	}
	<-started
	m.mu.Lock()
	m.Cfg.Egress[1].Server = "changed.example:443"
	m.mu.Unlock()
	close(release)
	waitLatencyBatch(t, m)
	if m.Status("test").Egress[1].Latency != nil {
		t.Fatal("old result assigned to changed node")
	}
	m.latencyProbe = func(context.Context, string) (time.Duration, error) { return 123 * time.Millisecond, nil }
	if duration, err := m.TestEgress("manual"); err != nil || duration != 123*time.Millisecond {
		t.Fatalf("single test failed: %v %v", duration, err)
	}
	if result := m.Status("test").Egress[1].Latency; result == nil || result.State != "ok" || result.MS != 123 {
		t.Fatal("single test did not update node delay")
	}
	m.latencyProbe = func(context.Context, string) (time.Duration, error) { return 0, errors.New("PRIVATE-PASSWORD") }
	if _, err := m.TestEgress("manual"); err == nil || strings.Contains(err.Error(), "PRIVATE-PASSWORD") {
		t.Fatal("failed test leaked credentials")
	}
	if m.Status("test").Egress[1].Latency.State != "error" {
		t.Fatal("failed re-test retained previous success")
	}
	m.mu.Lock()
	m.Cfg.Egress = m.Cfg.Egress[:1]
	m.mu.Unlock()
	if _, err := m.StartEgressLatencyTests(); err == nil {
		t.Fatal("empty node list started tests")
	}
}
