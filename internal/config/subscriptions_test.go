package config

import (
	"strings"
	"testing"
)

func TestSubscriptionMetadataCannotAddressArbitraryUnitsOrPorts(t *testing.T) {
	makeConfig := func() *Config {
		c := Default()
		c.Gateway.CertFile, c.Gateway.KeyFile = "cert", "key"
		c.DNS.GatewayIP = "172.22.0.1"
		c.Subscriptions = []SubscriptionConfig{{ID: "sub-0123456789abcdef", Name: "test", URL: "https://feed.example/?token=secret"}}
		c.Egress = append(c.Egress, EgressConfig{Name: "subscription-node", Type: "socks5", Addr: "127.0.0.1:7891", SubscriptionID: c.Subscriptions[0].ID, SubscriptionKey: strings.Repeat("a", 64)})
		return c
	}
	if err := makeConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"path":                 func(c *Config) { c.Subscriptions[0].ID = "../../system" },
		"foreign-unit":         func(c *Config) { c.Subscriptions[0].ID = "sshd" },
		"missing-subscription": func(c *Config) { c.Subscriptions = nil },
		"public-listener":      func(c *Config) { c.Egress[1].Addr = "0.0.0.0:7891" },
		"privileged-port":      func(c *Config) { c.Egress[1].Addr = "127.0.0.1:443" },
		"duplicate-port": func(c *Config) {
			c.Egress = append(c.Egress, EgressConfig{Name: "manual", Type: "socks5", Addr: "127.0.0.1:7891"})
		},
		"duplicate-id": func(c *Config) { c.Subscriptions = append(c.Subscriptions, c.Subscriptions[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			c := makeConfig()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("unsafe subscription metadata accepted")
			}
		})
	}
}
