package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
)

var subID = regexp.MustCompile(`^sub-[a-f0-9]{16}$`)
var subKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (c *Config) validateSubscriptions() error {
	if len(c.Subscriptions) > 8 {
		return fmt.Errorf("最多添加 8 个订阅")
	}
	ids := map[string]bool{}
	for _, s := range c.Subscriptions {
		if !subID.MatchString(s.ID) || ids[s.ID] {
			return fmt.Errorf("订阅 ID 无效或重复")
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("订阅 URL 无效")
		}
		ids[s.ID] = true
		if len(s.Candidates) > 256 {
			return fmt.Errorf("订阅候选节点过多")
		}
		candidates := map[string]bool{}
		for _, n := range s.Candidates {
			if !subKey.MatchString(n.Key) || n.Name == "" || len(n.Proxy) == 0 || candidates[n.Key] {
				return fmt.Errorf("订阅候选节点无效或重复")
			}
			candidates[n.Key] = true
		}
		if len(s.Candidates) > 0 && !subKey.MatchString(s.Revision) {
			return fmt.Errorf("订阅候选版本无效")
		}
	}
	keys := map[string]bool{}
	ports := map[string]bool{}
	for _, e := range c.Egress {
		if e.SubscriptionID == "" {
			continue
		}
		host, port, err := net.SplitHostPort(e.Addr)
		p, perr := strconv.Atoi(port)
		if !ids[e.SubscriptionID] || e.Type != "socks5" || !subKey.MatchString(e.SubscriptionKey) || err != nil || host != "127.0.0.1" || perr != nil || p < 1024 || p > 65535 {
			return fmt.Errorf("订阅节点元数据无效")
		}
		key := e.SubscriptionID + ":" + e.SubscriptionKey
		if keys[key] || ports[port] {
			return fmt.Errorf("订阅节点重复或监听端口冲突")
		}
		keys[key], ports[port] = true, true
	}
	for _, e := range c.Egress {
		if e.SubscriptionID != "" {
			continue
		}
		host, port, err := net.SplitHostPort(e.Addr)
		if err == nil && (host == "127.0.0.1" || host == "localhost" || host == "::1") && ports[port] {
			return fmt.Errorf("订阅节点与已有节点端口冲突")
		}
	}
	return nil
}
