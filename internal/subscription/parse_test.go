package subscription

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFeedFormatsAndIsolatedRuntime(t *testing.T) {
	links := "# nodes\nsocks5://user:secret@node.example:1080#Tokyo\ntrojan://password@other.example:443#Singapore\n"
	yamlFeed := "proxies:\n  - {name: Tokyo, type: socks5, server: node.example, port: 1080, username: user, password: secret}\n  - {name: Singapore, type: trojan, server: other.example, port: 443, password: password}\nexternal-controller: 0.0.0.0:9090\nlisteners: [{name: injected, type: http, port: 1234}]\ndns: {enable: true, listen: 0.0.0.0:53}\nrules: [MATCH,DIRECT]\n"
	for name, feed := range map[string]string{"links": links, "base64": base64.StdEncoding.EncodeToString([]byte(links)), "base64url": base64.RawURLEncoding.EncodeToString([]byte(links)), "clash": yamlFeed} {
		t.Run(name, func(t *testing.T) {
			nodes, err := Parse([]byte(feed))
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != 2 || nodes[0].Name != "Tokyo" || nodes[1].Type != "trojan" {
				t.Fatalf("unexpected nodes: %+v", nodes)
			}
			data, err := Render(nodes, []string{"one", "two"}, []int{8001, 8002})
			if err != nil {
				t.Fatal(err)
			}
			var runtime map[string]any
			if err := yaml.Unmarshal(data, &runtime); err != nil {
				t.Fatal(err)
			}
			if runtime["external-controller"] != nil || runtime["dns"] != nil || runtime["tun"] != nil || runtime["proxy-providers"] != nil {
				t.Fatalf("remote settings leaked into runtime")
			}
			listeners := runtime["listeners"].([]any)
			if len(listeners) != 2 {
				t.Fatalf("listeners=%v", listeners)
			}
			for i, listener := range listeners {
				l := listener.(map[string]any)
				if l["listen"] != "127.0.0.1" || l["type"] != "socks" || l["proxy"] != []string{"one", "two"}[i] {
					t.Fatalf("unsafe or incorrect listener: %v", l)
				}
			}
			if !strings.Contains(string(data), "MATCH,REJECT") {
				t.Fatal("missing fail-closed fallback")
			}
		})
	}
}

func TestInvalidFeedFailsAsWholeAndHidesSecrets(t *testing.T) {
	for name, feed := range map[string]string{
		"empty": "", "html": "<html>please login</html>", "unsupported": "proxies: [{name: x, type: ssh, server: localhost, port: 22}]",
		"duplicate": "socks5://a.example:1080#same\nsocks5://b.example:1080#same", "partial": "socks5://a.example:1080#ok\nvless://TOKEN-invalid",
		"alias":     "proxies: [&one {name: x, type: socks5, server: x, port: 1}, *one]",
		"file":      "proxies: [{name: x, type: trojan, server: x, port: 443, password: TOKEN-secret, certificate: /etc/passwd}]",
		"chain":     "proxies: [{name: x, type: socks5, server: x, port: 1080, dialer-proxy: DIRECT}]",
		"too-large": strings.Repeat("x", MaxBytes+1), "invalid-port": "proxies: [{name: x, type: socks5, server: x, port: 65536}]",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(feed)); err == nil {
				t.Fatal("invalid feed accepted")
			} else if strings.Contains(err.Error(), "TOKEN-") {
				t.Fatal("credentials in error")
			}
		})
	}
	var feed strings.Builder
	for i := 0; i <= MaxNodes; i++ {
		fmt.Fprintf(&feed, "socks5://node.example:1080#node%d\n", i)
	}
	if _, err := Parse([]byte(feed.String())); err == nil {
		t.Fatal("unbounded node count")
	}
}
