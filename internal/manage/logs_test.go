package manage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/w0ven/5gpn-next/internal/config"
)

func TestServiceLogsRedactCredentialsAndKeepLatestBoundedRecords(t *testing.T) {
	m, _ := subscriptionManager(t)
	m.Cfg.Bot.Token = "123456:BOT-TOKEN"
	m.Cfg.Subscriptions = []config.SubscriptionConfig{{URL: "https://feed.example/private?token=SUB-TOKEN"}}
	var lines []string
	for _, row := range []map[string]string{
		{"MESSAGE": "older entry", "PRIORITY": "6", "__REALTIME_TIMESTAMP": "1700000000000000"},
		{"MESSAGE": "failed https://api.telegram.org/bot123456:BOT-TOKEN/getUpdates token=TOKEN-SECRET password=PASSWORD-SECRET Authorization: Bearer AUTH-SECRET", "PRIORITY": "3", "__REALTIME_TIMESTAMP": "1700000001000000"},
		{"MESSAGE": "\x1b[31mhttps://feed.example/private?token=SUB-TOKEN vless://UUID-SECRET@host:443 uuid=UUID-SECRET\x00", "PRIORITY": "4", "__REALTIME_TIMESTAMP": "1700000002000000"},
	} {
		data, _ := json.Marshal(row)
		lines = append(lines, string(data))
	}
	m.journalRead = func(ctx context.Context, n int) ([]byte, error) {
		if n != 2 {
			t.Error("line limit not forwarded")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded command duration")
		}
		return []byte(strings.Join(lines, "\n")), nil
	}
	logs, err := m.ServiceLogs(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if logs.Unit != "5gpn-next.service" || len(logs.Entries) != 2 || logs.Entries[0].Level != "error" || logs.Entries[1].Level != "warning" || logs.Entries[0].Time != "2023-11-14T22:13:21Z" {
		t.Fatalf("incorrect log records: %+v", logs)
	}
	encoded, _ := json.Marshal(logs)
	for _, secret := range []string{"BOT-TOKEN", "SUB-TOKEN", "TOKEN-SECRET", "PASSWORD-SECRET", "UUID-SECRET", "AUTH-SECRET", "older entry", "u001b", "u0000"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("log response contains secret/control data %q", secret)
		}
	}
}

func TestServiceLogsLimitsEmptyAndUnavailable(t *testing.T) {
	m, _ := subscriptionManager(t)
	m.journalRead = func(context.Context, int) ([]byte, error) { return []byte(""), nil }
	logs, err := m.ServiceLogs(context.Background(), 200)
	if err != nil || logs.Entries == nil || len(logs.Entries) != 0 {
		t.Fatal("empty journal should be a usable empty list")
	}
	for _, n := range []int{-1, 0, 501} {
		if _, err := m.ServiceLogs(context.Background(), n); err == nil {
			t.Fatal("unbounded line request")
		}
	}
	m.journalRead = func(context.Context, int) ([]byte, error) { return nil, errors.New("TOKEN-private-command-output") }
	if _, err := m.ServiceLogs(context.Background(), 200); err == nil || strings.Contains(err.Error(), "TOKEN-private") {
		t.Fatal("unsafe command error")
	}
	m.journalRead = func(context.Context, int) ([]byte, error) { return make([]byte, maxJournalBytes+1), nil }
	if _, err := m.ServiceLogs(context.Background(), 200); err == nil {
		t.Fatal("unbounded output")
	}
	var buffer cappedJournalBuffer
	if _, err := buffer.Write(make([]byte, maxJournalBytes+1)); err == nil || buffer.Len() != 0 {
		t.Fatal("command writer did not bound output")
	}
}
