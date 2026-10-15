package profile

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func TestDNSProfileCellularOnly(t *testing.T) {
	o := DefaultDNS("kfc.example.com")
	o.ServerAddresses = []string{"177.0.143.37"}
	b, err := o.Build()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(b)
	for _, want := range []string{
		"<string>com.apple.dnsSettings.managed</string>",
		"<key>DNSProtocol</key>",
		"<string>TLS</string>",
		"<key>ServerName</key>",
		"<string>kfc.example.com</string>",
		"<string>177.0.143.37</string>",
		"<key>OnDemandRules</key>",
		"<key>InterfaceTypeMatch</key>",
		"<string>Cellular</string>",
		"<string>Connect</string>",
		"<string>Disconnect</string>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("dns profile missing %s", want)
		}
	}
	// 规则顺序必须是 Cellular Connect 在前、兜底 Disconnect 在后。
	if strings.Index(xml, "<string>Connect</string>") > strings.Index(xml, "<string>Disconnect</string>") {
		t.Fatal("OnDemandRules order broken: Connect must precede fallback Disconnect")
	}
	// DNS 模式不属于 Relay，不能包含 Relay 字段或国内域名排除名单。
	for _, banned := range []string{"HTTP2RelayURL", "ExcludedDomains", "RelayUUID"} {
		if strings.Contains(xml, banned) {
			t.Errorf("dns profile must not contain %s", banned)
		}
	}
}

// Decode the emitted plist as a tree so nested SSID arrays and rule ordering
// are checked semantically rather than by searching unrelated XML text.
type plistTestElement struct {
	XMLName  xml.Name
	Text     string             `xml:",chardata"`
	Children []plistTestElement `xml:",any"`
}

func (e plistTestElement) field(t *testing.T, key string) plistTestElement {
	t.Helper()
	for i, child := range e.Children {
		if child.XMLName.Local == "key" && child.Text == key && i+1 < len(e.Children) {
			return e.Children[i+1]
		}
	}
	t.Fatalf("missing plist field %q", key)
	return plistTestElement{}
}

func TestHotspotProfileRestrictsWiFiToExactSSIDs(t *testing.T) {
	o := DefaultDNS("gateway.example.com")
	o.HotspotSSIDs = []string{"Xiaomi 15 Pro", "中文 & <热点>"}
	b, err := o.Build()
	if err != nil {
		t.Fatal(err)
	}
	var root plistTestElement
	if err := xml.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	payload := root.Children[0].field(t, "PayloadContent").Children[0]
	rules := payload.field(t, "OnDemandRules").Children
	if len(rules) != 3 {
		t.Fatalf("want three rules, got %d", len(rules))
	}
	if rules[0].field(t, "InterfaceTypeMatch").Text != "Cellular" || rules[0].field(t, "Action").Text != "Connect" {
		t.Fatal("cellular behavior changed")
	}
	if rules[1].field(t, "InterfaceTypeMatch").Text != "WiFi" || rules[1].field(t, "Action").Text != "Connect" {
		t.Fatal("hotspot rule missing")
	}
	ssids := rules[1].field(t, "SSIDMatch").Children
	if len(ssids) != 2 || ssids[0].Text != "Xiaomi 15 Pro" || ssids[1].Text != "中文 & <热点>" {
		t.Fatal("exact SSIDs or escaping lost")
	}
	if len(rules[2].Children) != 2 || rules[2].field(t, "Action").Text != "Disconnect" {
		t.Fatal("unmatched networks must disconnect")
	}
	if strings.Contains(payload.field(t, "PayloadDescription").Text, "仅蜂窝") {
		t.Fatal("misleading cellular-only description")
	}
	// Adding a hotspot must not create a second independent DNS configuration.
	baseline, _ := DefaultDNS("gateway.example.com").Build()
	var original plistTestElement
	xml.Unmarshal(baseline, &original)
	originalPayload := original.Children[0].field(t, "PayloadContent").Children[0]
	if payload.field(t, "PayloadUUID").Text != originalPayload.field(t, "PayloadUUID").Text {
		t.Fatal("payload identity changed")
	}
}

func TestHotspotProfileRejectsUnusableSSIDs(t *testing.T) {
	for _, ssid := range []string{"", strings.Repeat("a", 33), "bad\x00name", "bad\nname", string([]byte{0xff})} {
		o := DefaultDNS("gateway.example.com")
		o.HotspotSSIDs = []string{ssid}
		if _, err := o.Build(); err == nil {
			t.Fatalf("accepted invalid SSID %q", ssid)
		}
	}
}

func TestDNSProfileStableIdentity(t *testing.T) {
	o := DefaultDNS("kfc.example.com")
	first, err := o.Build()
	if err != nil {
		t.Fatal(err)
	}
	second, err := o.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same host must produce byte-identical dns profile")
	}
}

func TestDNSProfileRequiresHost(t *testing.T) {
	if _, err := (DNSOptions{}).Build(); err == nil {
		t.Fatal("empty host must fail")
	}
}
