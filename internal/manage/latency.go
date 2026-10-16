package manage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/egress"
)

const latencyConcurrency = 4

type NodeLatency struct {
	State     string `json:"state"` // queued | testing | ok | error
	MS        int64  `json:"ms"`
	CheckedAt int64  `json:"checked_at,omitempty"`
}
type EgressTestProgress struct {
	Running bool `json:"running"`
	Total   int  `json:"total"`
	Done    int  `json:"done"`
}
type cachedNodeLatency struct {
	NodeLatency
	fingerprint string
}
type latencyTarget struct{ name, addr, fingerprint string }

func latencyFingerprint(e config.EgressConfig, subs []config.SubscriptionConfig) string {
	data, _ := json.Marshal(e)
	for _, s := range subs {
		if s.ID == e.SubscriptionID {
			data = append(data, s.Revision...)
			break
		}
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (m *Manager) nodeLatency(name, fingerprint string) *NodeLatency {
	m.latencyMu.Lock()
	defer m.latencyMu.Unlock()
	if value, ok := m.latencyResults[name]; ok && value.fingerprint == fingerprint {
		result := value.NodeLatency
		return &result
	}
	return nil
}
func (m *Manager) LatencyProgress() EgressTestProgress {
	m.latencyMu.Lock()
	defer m.latencyMu.Unlock()
	return m.latencyProgress
}

func (m *Manager) latencyTarget(name string) (latencyTarget, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.Cfg.Egress {
		if e.Name == name {
			return latencyTarget{name: e.Name, addr: e.Addr, fingerprint: latencyFingerprint(e, m.Cfg.Subscriptions)}, true
		}
	}
	return latencyTarget{}, false
}

// recordLatency discards results from an exit that was removed or updated while
// its test was running. It never writes transient measurements to config.json.
func (m *Manager) recordLatency(target latencyTarget, value NodeLatency) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.Cfg.Egress {
		if e.Name != target.name {
			continue
		}
		if latencyFingerprint(e, m.Cfg.Subscriptions) != target.fingerprint {
			return
		}
		m.latencyMu.Lock()
		defer m.latencyMu.Unlock()
		if m.latencyResults == nil {
			m.latencyResults = map[string]cachedNodeLatency{}
		}
		m.latencyResults[target.name] = cachedNodeLatency{NodeLatency: value, fingerprint: target.fingerprint}
		return
	}
}

func (m *Manager) runLatencyTest(target latencyTarget) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	m.latencyMu.Lock()
	if m.latencySlots == nil {
		m.latencySlots = make(chan struct{}, latencyConcurrency)
	}
	slots, probe := m.latencySlots, m.latencyProbe
	m.latencyMu.Unlock()
	if probe == nil {
		probe = measureHTTPDelay
	}
	m.recordLatency(target, NodeLatency{State: "testing"})
	var duration time.Duration
	var err error
	select {
	case slots <- struct{}{}:
		duration, err = probe(ctx, target.addr)
		<-slots
	case <-ctx.Done():
		err = ctx.Err()
	}
	value := NodeLatency{State: "ok", MS: max(1, duration.Milliseconds()), CheckedAt: time.Now().Unix()}
	if err != nil {
		value.State, value.MS = "error", 0
	}
	m.recordLatency(target, value)
	if err != nil {
		return 0, fmt.Errorf("节点测速失败或超时")
	}
	return duration, nil
}

func (m *Manager) testNamedEgress(name string) (time.Duration, error) {
	target, found := m.latencyTarget(name)
	if !found {
		return 0, fmt.Errorf("出口不存在")
	}
	return m.runLatencyTest(target)
}

// StartEgressLatencyTests returns immediately. At most four tests (including
// individual tests) can dial at once; results become visible as each finishes.
func (m *Manager) StartEgressLatencyTests() (EgressTestProgress, error) {
	m.mu.RLock()
	var targets []latencyTarget
	for _, e := range m.Cfg.Egress {
		if e.Type != "direct" {
			targets = append(targets, latencyTarget{name: e.Name, addr: e.Addr, fingerprint: latencyFingerprint(e, m.Cfg.Subscriptions)})
		}
	}
	m.latencyMu.Lock()
	if m.latencyProgress.Running {
		state := m.latencyProgress
		m.latencyMu.Unlock()
		m.mu.RUnlock()
		return state, fmt.Errorf("节点正在测速，请等待本次完成")
	}
	if len(targets) == 0 {
		m.latencyMu.Unlock()
		m.mu.RUnlock()
		return EgressTestProgress{}, fmt.Errorf("没有可测速的节点，请先添加或导入节点")
	}
	m.latencyProgress = EgressTestProgress{Running: true, Total: len(targets)}
	state := m.latencyProgress
	// Discard measurements for deleted exits on the next batch.
	m.latencyResults = make(map[string]cachedNodeLatency, len(targets))
	for _, target := range targets {
		m.latencyResults[target.name] = cachedNodeLatency{NodeLatency: NodeLatency{State: "queued"}, fingerprint: target.fingerprint}
	}
	m.latencyMu.Unlock()
	m.mu.RUnlock()
	go func() {
		jobs := make(chan latencyTarget)
		var workers sync.WaitGroup
		for i := 0; i < min(latencyConcurrency, len(targets)); i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for target := range jobs {
					current, found := m.latencyTarget(target.name)
					if found && current.fingerprint == target.fingerprint {
						_, _ = m.runLatencyTest(target)
					}
					m.latencyMu.Lock()
					m.latencyProgress.Done++
					m.latencyMu.Unlock()
				}
			}()
		}
		for _, target := range targets {
			jobs <- target
		}
		close(jobs)
		workers.Wait()
		m.latencyMu.Lock()
		m.latencyProgress.Running = false
		m.latencyMu.Unlock()
	}()
	return state, nil
}

// measureHTTPDelay measures a complete HTTPS request through the selected exit,
// including SOCKS/TCP/TLS. It is not ICMP ping or the phone-to-gateway delay.
func measureHTTPDelay(ctx context.Context, addr string) (time.Duration, error) {
	transport := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second}
	if addr != "" {
		transport.DialContext = egress.NewSocks5("latency", addr, false).DialContext
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, target := range []string{"https://cp.cloudflare.com/generate_204", "https://www.gstatic.com/generate_204"} {
		requestCtx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
		req, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
		start := time.Now()
		resp, err := client.Do(req)
		if err == nil {
			_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if err == nil && (resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK) {
				duration := time.Since(start)
				cancel()
				return duration, nil
			}
		}
		cancel()
		if ctx.Err() != nil {
			break
		}
	}
	return 0, fmt.Errorf("测速目标未响应")
}
