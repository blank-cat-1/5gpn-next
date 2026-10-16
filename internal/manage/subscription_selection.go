package manage

import (
	"context"
	"fmt"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/subscription"
)

type SubscriptionCandidate struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Server   string `json:"server"`
	Imported bool   `json:"imported"`
}
type SubscriptionCandidates struct {
	ID       string                  `json:"id"`
	Name     string                  `json:"name"`
	Revision string                  `json:"revision"`
	Nodes    []SubscriptionCandidate `json:"nodes"`
}

func (m *Manager) SubscriptionCandidates(id string) (SubscriptionCandidates, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := SubscriptionCandidates{Nodes: []SubscriptionCandidate{}}
	for _, s := range m.Cfg.Subscriptions {
		if s.ID != id {
			continue
		}
		result.ID, result.Name, result.Revision = s.ID, s.Name, s.Revision
		imported := map[string]bool{}
		for _, e := range m.Cfg.Egress {
			if e.SubscriptionID == id {
				imported[e.SubscriptionKey] = true
			}
		}
		for _, n := range s.Candidates {
			result.Nodes = append(result.Nodes, SubscriptionCandidate{Key: n.Key, Name: n.Name, Type: n.Type, Server: n.Server, Imported: imported[n.Key]})
		}
		return result, nil
	}
	return result, fmt.Errorf("订阅不存在")
}

// ImportSubscription adds only the selected candidates, retaining previously
// imported exits. A revision check prevents importing a changed catalogue.
func (m *Manager) ImportSubscription(ctx context.Context, id, revision string, keys []string) (string, error) {
	if !m.subMu.TryLock() {
		return "", fmt.Errorf("订阅正在更新，请稍后再试")
	}
	defer m.subMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("导入请求已取消")
	}
	if len(keys) == 0 || len(keys) > subscription.MaxNodes {
		return "", fmt.Errorf("请先选择需要导入的节点")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var sub config.SubscriptionConfig
	for _, s := range m.Cfg.Subscriptions {
		if s.ID == id {
			sub = s
			break
		}
	}
	if sub.ID == "" {
		return "", fmt.Errorf("订阅不存在")
	}
	if revision == "" || sub.Revision != revision {
		return "", fmt.Errorf("订阅已更新，请重新打开节点列表后选择")
	}
	wanted := map[string]bool{}
	known := map[string]bool{}
	for _, c := range sub.Candidates {
		known[c.Key] = true
	}
	for _, key := range keys {
		if !known[key] || wanted[key] {
			return "", fmt.Errorf("选择中有无效或重复节点，请重新选择")
		}
		wanted[key] = true
	}
	added := 0
	for _, e := range m.Cfg.Egress {
		if e.SubscriptionID == id {
			wanted[e.SubscriptionKey] = true
		}
	}
	var selected []subscription.Node
	for _, c := range sub.Candidates {
		if !wanted[c.Key] {
			continue
		}
		selected = append(selected, subscription.Node{Name: c.Name, Type: c.Type, Server: c.Server, Proxy: c.Proxy})
	}
	previous := map[string]bool{}
	for _, e := range m.Cfg.Egress {
		if e.SubscriptionID == id {
			previous[e.SubscriptionKey] = true
		}
	}
	for _, key := range keys {
		if !previous[key] {
			added++
		}
	}
	if added == 0 {
		return "所选节点已导入，无需重复添加", nil
	}
	if err := m.commitSubscriptionLocked(sub, selected, false); err != nil {
		return "", err
	}
	return fmt.Sprintf("已导入 %d 个节点，未选择的节点保留在订阅中", added), nil
}
