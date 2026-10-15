package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/policy"
)

func TestPreviousCustomFlagsDoNotOverrideOriginalRouting(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.json")
	// Users may upgrade from the first custom build without rewriting config.
	data := []byte(`{
		"gateway":{"listen":":20443","cert_file":"fixture.pem","key_file":"fixture.key"},
		"dns":{"gateway_ip":"172.22.0.1"},
		"panel":{"enabled":true,"telegram_auth":true},
		"egress":[{"name":"chosen","type":"socks5","addr":"127.0.0.1:7891"},{"name":"custom","type":"socks5","addr":"127.0.0.1:7892"}],
		"routing_mode":"direct", "final":"proxy:chosen",
		"rules":["DOMAIN-SUFFIX,special.example,proxy:custom"]
	}`)
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	e := readyEngine()
	if err := applyRules(cfg, e); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host   string
		action policy.Action
		egress string
	}{
		{"special.example:443", policy.ActionProxy, "custom"},
		{"foreign.example:443", policy.ActionProxy, "chosen"},
		{"www.baidu.com:443", policy.ActionDirect, ""},
		{"dns.google:443", policy.ActionBlock, ""},
	} {
		if got := e.Match(target(t, tc.host)); got.Action != tc.action || got.Egress != tc.egress {
			t.Fatalf("previous custom flags changed %s: %+v", tc.host, got)
		}
	}
	after, err := os.ReadFile(filename)
	if err != nil || string(after) != string(data) {
		t.Fatal("loading existing config rewrote it")
	}
}
