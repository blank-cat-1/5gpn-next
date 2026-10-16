// Package subscription imports node definitions only. Remote routing, DNS,
// listeners and controllers never become part of the gateway configuration.
package subscription

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/w0ven/5gpn-next/internal/node"
	"gopkg.in/yaml.v3"
)

const MaxBytes = 4 << 20
const MaxNodes = 256

type Node struct {
	Name, Type, Server string
	Proxy              map[string]any
}

// Parse accepts Clash YAML, plain share links, and Base64 share-link lists.
// A malformed entry fails the whole refresh, preserving the existing nodes.
func Parse(data []byte) ([]Node, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("订阅超过 4 MiB 上限")
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if !bytes.Contains(data, []byte("://")) && !bytes.Contains(data, []byte("proxies:")) {
		compact := strings.Join(strings.Fields(string(data)), "")
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if decoded, err := enc.DecodeString(compact); err == nil {
				data = decoded
				break
			}
		}
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("订阅内容编码无效")
	}
	var proxies []map[string]any
	var labels []string
	// Share-link feeds start with a URI; URLs inside YAML must not select this branch.
	first := strings.TrimSpace(string(data))
	for _, line := range strings.Split(first, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			first = line
			break
		}
	}
	if i := strings.Index(first, "://"); i > 0 && !strings.ContainsAny(first[:i], " :{}[]") {
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			n, err := node.Parse(line)
			if err != nil {
				return nil, fmt.Errorf("第 %d 行节点链接无效或协议不支持", i+1)
			}
			rendered, err := n.MihomoConfig(1080)
			if err != nil {
				return nil, fmt.Errorf("第 %d 行节点参数不完整", i+1)
			}
			var doc struct {
				Proxies []map[string]any `yaml:"proxies"`
			}
			if err = yaml.Unmarshal([]byte(rendered), &doc); err != nil || len(doc.Proxies) != 1 {
				return nil, fmt.Errorf("第 %d 行节点无法转换", i+1)
			}
			proxies = append(proxies, doc.Proxies[0])
			labels = append(labels, strings.TrimSpace(n.Name))
			if len(proxies) > MaxNodes {
				return nil, fmt.Errorf("单个订阅最多支持 %d 个节点", MaxNodes)
			}
		}
	} else {
		var tree yaml.Node
		if err := yaml.Unmarshal(data, &tree); err != nil {
			return nil, fmt.Errorf("不是有效的 Clash YAML 或节点链接订阅")
		}
		count := 0
		if !boundedYAML(&tree, 0, &count) {
			return nil, fmt.Errorf("订阅 YAML 过于复杂或包含别名")
		}
		var doc struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if err := tree.Decode(&doc); err != nil {
			return nil, fmt.Errorf("订阅 proxies 格式无效")
		}
		proxies = doc.Proxies
		for _, p := range proxies {
			name, _ := p["name"].(string)
			labels = append(labels, strings.TrimSpace(name))
		}
	}
	if len(proxies) == 0 {
		return nil, fmt.Errorf("订阅没有可导入的节点；请使用节点链接或 Clash 格式")
	}
	if len(proxies) > MaxNodes {
		return nil, fmt.Errorf("单个订阅最多支持 %d 个节点", MaxNodes)
	}
	seen := map[string]bool{}
	result := make([]Node, 0, len(proxies))
	for i, p := range proxies {
		typ, _ := p["type"].(string)
		switch typ {
		case "ss", "vmess", "vless", "trojan", "hysteria2", "tuic", "socks5", "http":
		default:
			return nil, fmt.Errorf("第 %d 个节点协议暂不支持", i+1)
		}
		server, _ := p["server"].(string)
		port, err := strconv.Atoi(fmt.Sprint(p["port"]))
		if server == "" || strings.ContainsAny(server, "\r\n/\\") || err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("第 %d 个节点服务器或端口无效", i+1)
		}
		if unsafeProxy(p) {
			return nil, fmt.Errorf("第 %d 个节点包含本地文件或链式代理设置，暂不支持", i+1)
		}
		label := labels[i]
		if label == "" {
			label = net.JoinHostPort(server, strconv.Itoa(port))
		}
		if len([]rune(label)) > 160 || strings.ContainsAny(label, "\r\n\x00") {
			return nil, fmt.Errorf("第 %d 个节点名称无效", i+1)
		}
		if seen[label] {
			return nil, fmt.Errorf("订阅存在重复节点名称，请使用名称唯一的订阅")
		}
		seen[label] = true
		result = append(result, Node{Name: label, Type: typ, Server: net.JoinHostPort(server, strconv.Itoa(port)), Proxy: p})
	}
	return result, nil
}

func boundedYAML(n *yaml.Node, depth int, count *int) bool {
	*count++
	if depth > 20 || *count > 50000 || n.Kind == yaml.AliasNode {
		return false
	}
	for _, child := range n.Content {
		if !boundedYAML(child, depth+1, count) {
			return false
		}
	}
	return true
}

func unsafeProxy(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			switch k {
			case "dialer-proxy", "interface-name", "routing-mark", "certificate", "private-key", "ca", "ca-str":
				return true
			}
			if strings.HasSuffix(k, "-path") || unsafeProxy(val) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if unsafeProxy(val) {
				return true
			}
		}
	}
	return false
}

// Render gives every node its own loopback SOCKS listener in one mihomo process.
// The gateway continues to make every routing decision using its original engine.
func Render(nodes []Node, names []string, ports []int) ([]byte, error) {
	if len(nodes) != len(names) || len(nodes) != len(ports) || len(nodes) == 0 {
		return nil, fmt.Errorf("订阅节点映射无效")
	}
	proxies := make([]map[string]any, 0, len(nodes))
	listeners := make([]map[string]any, 0, len(nodes))
	for i, n := range nodes {
		p := make(map[string]any, len(n.Proxy))
		for k, v := range n.Proxy {
			p[k] = v
		}
		p["name"] = names[i]
		proxies = append(proxies, p)
		listeners = append(listeners, map[string]any{"name": names[i], "type": "socks", "listen": "127.0.0.1", "port": ports[i], "udp": true, "proxy": names[i]})
	}
	return yaml.Marshal(map[string]any{
		"mixed-port": 0, "socks-port": 0, "port": 0, "allow-lan": false,
		"bind-address": "127.0.0.1", "mode": "rule", "log-level": "warning", "ipv6": true,
		"profile":   map[string]any{"store-selected": false, "store-fake-ip": false},
		"listeners": listeners, "proxies": proxies, "rules": []string{"MATCH,REJECT"},
	})
}
