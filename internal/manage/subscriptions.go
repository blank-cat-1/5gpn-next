package manage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/subscription"
)

type SubscriptionStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Nodes     int    `json:"nodes"`
	Available int    `json:"available"`
	UpdatedAt int64  `json:"updated_at"`
}

// Subscriptions deliberately excludes the URL path/query and node credentials.
func (m *Manager) Subscriptions() []SubscriptionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]SubscriptionStatus, 0, len(m.Cfg.Subscriptions))
	for _, s := range m.Cfg.Subscriptions {
		st := SubscriptionStatus{ID: s.ID, Name: s.Name, UpdatedAt: s.UpdatedAt, Available: len(s.Candidates)}
		if u, err := url.Parse(s.URL); err == nil {
			st.Host = u.Hostname()
		}
		for _, e := range m.Cfg.Egress {
			if e.SubscriptionID == s.ID {
				st.Nodes++
			}
		}
		list = append(list, st)
	}
	return list
}

func subscriptionURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || len(raw) > 4096 || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("请输入有效的 HTTP/HTTPS 订阅链接（不支持用户名密码或片段）")
	}
	return u, nil
}

func (m *Manager) fetchSubscription(ctx context.Context, raw string) ([]subscription.Node, error) {
	u, err := subscriptionURL(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("订阅请求无效")
	}
	req.Header.Set("User-Agent", "Clash/5gpn-next")
	client := m.subClient
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return fmt.Errorf("too many redirects")
			}
			if _, err := subscriptionURL(r.URL.String()); err != nil {
				return err
			}
			if via[0].URL.Scheme == "https" && r.URL.Scheme != "https" {
				return fmt.Errorf("HTTPS downgrade refused")
			}
			return nil
		}}
	}
	resp, err := client.Do(req)
	// net/http errors contain complete credential-bearing URLs. Never return them.
	if err != nil {
		return nil, fmt.Errorf("下载订阅失败，请检查链接、证书及服务器网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("订阅服务器返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, subscription.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取订阅失败，请稍后重试")
	}
	return subscription.Parse(data)
}

// ChangeSubscription serializes add/update/remove. Fetching happens without the
// configuration lock; the eventual commit rechecks the latest configuration.
func (m *Manager) ChangeSubscription(ctx context.Context, action, id, name, rawURL string) (string, error) {
	if !m.subMu.TryLock() {
		return "", fmt.Errorf("订阅正在更新，请稍后再试")
	}
	defer m.subMu.Unlock()
	if action == "remove" {
		return m.removeSubscription(id)
	}
	var sub config.SubscriptionConfig
	switch action {
	case "add":
		u, err := subscriptionURL(rawURL)
		if err != nil {
			return "", err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = u.Hostname()
		}
		if len([]rune(name)) > 60 || strings.ContainsAny(name, "\r\n\x00") {
			return "", fmt.Errorf("订阅名称最多 60 个字符且不能换行")
		}
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("创建订阅 ID 失败")
		}
		sub = config.SubscriptionConfig{ID: "sub-" + hex.EncodeToString(b[:]), Name: name, URL: u.String()}
	case "update":
		m.mu.RLock()
		for _, s := range m.Cfg.Subscriptions {
			if s.ID == id {
				sub = s
				break
			}
		}
		m.mu.RUnlock()
		if sub.ID == "" {
			return "", fmt.Errorf("订阅不存在")
		}
	default:
		return "", fmt.Errorf("未知订阅操作")
	}
	nodes, err := m.fetchSubscription(ctx, sub.URL)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if action == "add" {
		if len(m.Cfg.Subscriptions) >= 8 {
			return "", fmt.Errorf("最多添加 8 个订阅")
		}
		for _, s := range m.Cfg.Subscriptions {
			if s.URL == sub.URL {
				return "", fmt.Errorf("该订阅已存在，请点击更新")
			}
		}
	}
	old := *m.Cfg
	// Saving a subscription only saves its catalogue. Refresh an existing
	// subscription's imported subset without automatically activating new nodes.
	selected := map[string]bool{}
	for _, e := range old.Egress {
		if e.SubscriptionID == sub.ID {
			selected[e.SubscriptionKey] = true
		}
	}
	sub.Candidates = nil
	var imported []subscription.Node
	for _, n := range nodes {
		hash := sha256.Sum256([]byte(n.Name))
		key := hex.EncodeToString(hash[:])
		sub.Candidates = append(sub.Candidates, config.SubscriptionNode{Key: key, Name: n.Name, Type: n.Type, Server: n.Server, Proxy: n.Proxy})
		if selected[key] {
			imported = append(imported, n)
		}
	}
	encoded, err := json.Marshal(sub.Candidates)
	if err != nil {
		return "", fmt.Errorf("保存订阅候选节点失败")
	}
	revision := sha256.Sum256(encoded)
	sub.Revision = hex.EncodeToString(revision[:])
	sub.UpdatedAt = time.Now().Unix()
	if err := m.commitSubscriptionLocked(sub, imported, action == "add"); err != nil {
		return "", err
	}
	return fmt.Sprintf("订阅已保存，共 %d 个候选节点，已导入 %d 个；请选择需要的节点导入", len(nodes), len(imported)), nil
}

// commitSubscriptionLocked applies only the explicitly imported subset.
func (m *Manager) commitSubscriptionLocked(sub config.SubscriptionConfig, nodes []subscription.Node, add bool) error {
	old := *m.Cfg
	next := old
	next.Egress = make([]config.EgressConfig, 0, len(old.Egress)+len(nodes))
	next.Subscriptions = append([]config.SubscriptionConfig(nil), old.Subscriptions...)
	previous := map[string]config.EgressConfig{}
	usedNames := map[string]bool{}
	port := 7891
	for _, e := range old.Egress {
		usedNames[e.Name] = true
		if p, ok := localPort(e.Addr); ok && p >= port {
			port = p + 1
		}
		if e.SubscriptionID == sub.ID {
			previous[e.SubscriptionKey] = e
		} else {
			next.Egress = append(next.Egress, e)
		}
	}
	newNames := map[string]bool{}
	names, ports, addrs := make([]string, 0, len(nodes)), make([]int, 0, len(nodes)), make([]string, 0, len(nodes))
	for _, n := range nodes {
		hash := sha256.Sum256([]byte(n.Name))
		key := hex.EncodeToString(hash[:])
		e, exists := previous[key]
		if !exists {
			internal := sub.ID + "-" + key[:16]
			if usedNames[internal] {
				return fmt.Errorf("订阅节点名称冲突")
			}
			if port > 65535 {
				return fmt.Errorf("没有可用的本机节点端口")
			}
			e = config.EgressConfig{Name: internal, Type: "socks5", Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), DisplayName: n.Name, SubscriptionID: sub.ID, SubscriptionKey: key}
			port++
		}
		p, ok := localPort(e.Addr)
		if !ok {
			return fmt.Errorf("已有订阅节点地址无效")
		}
		e.Proto, e.Server = n.Type, n.Server
		// Existing nodes retain their probe result; new nodes get tested by the
		// regular startup probe or the user's node test, never assumed reachable.
		next.Egress = append(next.Egress, e)
		newNames[e.Name] = true
		names, ports, addrs = append(names, e.Name), append(ports, p), append(addrs, e.Addr)
	}
	for _, e := range previous {
		if !newNames[e.Name] && referencesEgress(old, e.Name) {
			return fmt.Errorf("订阅已移除正在被默认出口或自定义规则使用的节点 %q，请先调整引用；本次更新未应用", displayOf(e))
		}
	}
	if add {
		next.Subscriptions = append(next.Subscriptions, sub)
	} else {
		found := false
		for i := range next.Subscriptions {
			if next.Subscriptions[i].ID == sub.ID {
				next.Subscriptions[i] = sub
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("订阅不存在")
		}
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("订阅配置校验失败")
	}
	undo := func() error { return nil }
	if len(nodes) > 0 {
		data, err := subscription.Render(nodes, names, ports)
		if err != nil {
			return err
		}
		undo, err = m.subscriptionRuntime().Apply(sub.ID, data, addrs)
		if err != nil {
			return err
		}
	}
	m.Cfg = &next
	if err := m.saveAndReloadLocked(); err != nil {
		m.Cfg = &old
		if rerr := undo(); rerr != nil {
			return fmt.Errorf("保存失败，订阅服务恢复失败，请检查服务状态")
		}
		if errors.Is(err, errConfigRollback) {
			return fmt.Errorf("配置恢复未完成，请检查服务状态；订阅协议配置已恢复")
		}
		return fmt.Errorf("保存或重载失败，已恢复原订阅和配置")
	}
	if len(nodes) == 0 && len(previous) > 0 {
		if err := m.subscriptionRuntime().Remove(sub.ID); err != nil {
			m.Cfg = &old
			if rerr := m.saveAndReloadLocked(); rerr != nil {
				return fmt.Errorf("停止订阅服务失败且恢复配置失败")
			}
			return fmt.Errorf("停止订阅服务失败，已恢复订阅配置，请检查服务状态")
		}
	}
	m.forgetRemovedSubscriptionNodes(previous, newNames)
	return nil
}

func referencesEgress(cfg config.Config, name string) bool {
	if cfg.Final == "proxy:"+name {
		return true
	}
	for _, rule := range cfg.Rules {
		if strings.HasSuffix(strings.TrimSpace(rule), ",proxy:"+name) {
			return true
		}
	}
	return false
}

func (m *Manager) removeSubscription(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := *m.Cfg
	next := old
	next.Egress = nil
	next.Subscriptions = nil
	found := false
	for _, s := range old.Subscriptions {
		if s.ID == id {
			found = true
		} else {
			next.Subscriptions = append(next.Subscriptions, s)
		}
	}
	if !found {
		return "", fmt.Errorf("订阅不存在")
	}
	removed := map[string]config.EgressConfig{}
	for _, e := range old.Egress {
		if e.SubscriptionID != id {
			next.Egress = append(next.Egress, e)
			continue
		}
		if referencesEgress(old, e.Name) {
			return "", fmt.Errorf("节点 %q 仍被默认出口或自定义规则使用，请先调整后再删除订阅", displayOf(e))
		}
		removed[e.Name] = e
	}
	// Keep the old process running until persistence succeeds, so failure leaves
	// all listeners and existing configuration intact.
	m.Cfg = &next
	if err := m.saveAndReloadLocked(); err != nil {
		m.Cfg = &old
		if errors.Is(err, errConfigRollback) {
			return "", fmt.Errorf("删除订阅失败且配置恢复未完成，请检查服务状态")
		}
		return "", fmt.Errorf("保存失败，未删除订阅")
	}
	if len(removed) > 0 {
		if err := m.subscriptionRuntime().Remove(id); err != nil {
			m.Cfg = &old
			if rerr := m.saveAndReloadLocked(); rerr != nil {
				return "", fmt.Errorf("停止订阅服务失败，恢复配置失败，请检查服务状态")
			}
			return "", fmt.Errorf("停止订阅服务失败，已恢复订阅配置")
		}
	}
	m.forgetRemovedSubscriptionNodes(removed, nil)
	return "已删除订阅及其导入节点，其他节点和规则保持不变", nil
}

func (m *Manager) forgetRemovedSubscriptionNodes(previous map[string]config.EgressConfig, kept map[string]bool) {
	if m.Health == nil {
		return
	}
	for _, e := range previous {
		if !kept[e.Name] {
			m.Health.ForgetEgress(e.Name)
		}
	}
	_ = m.Health.Save()
}
