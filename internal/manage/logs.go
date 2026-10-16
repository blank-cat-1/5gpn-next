package manage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxJournalBytes = 2 << 20

type ServiceLogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type ServiceLogs struct {
	Unit    string            `json:"unit"`
	Entries []ServiceLogEntry `json:"entries"`
	ReadAt  string            `json:"read_at"`
}

var logURL = regexp.MustCompile(`(?i)(?:https?|ss|ssr|vmess|vless|trojan|hysteria2?|hy2|tuic|socks5)://[^\s<>"']+`)
var logSecret = regexp.MustCompile(`(?i)(token|password|passwd|secret|uuid|authorization)(["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
var logAuthorization = regexp.MustCompile(`(?i)authorization(["']?\s*[:=]\s*)[^\r\n]+`)
var logANSI = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// ServiceLogs reads a fixed systemd unit only, with a bounded time and output.
// Neither file paths nor journalctl flags can be supplied by the caller.
func (m *Manager) ServiceLogs(ctx context.Context, lines int) (ServiceLogs, error) {
	result := ServiceLogs{Unit: "5gpn-next.service", Entries: []ServiceLogEntry{}, ReadAt: time.Now().UTC().Format(time.RFC3339)}
	if lines < 1 || lines > 500 {
		return result, fmt.Errorf("日志条数必须在 1–500 之间")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	read := m.journalRead
	if read == nil {
		read = readServiceJournal
	}
	data, err := read(ctx, lines)
	if err != nil || len(data) > maxJournalBytes {
		return result, fmt.Errorf("暂时无法读取服务日志，请确认 systemd journal 可用且服务有读取权限")
	}
	m.mu.RLock()
	secrets := []string{m.Cfg.Bot.Token}
	for _, s := range m.Cfg.Subscriptions {
		secrets = append(secrets, s.URL)
	}
	m.mu.RUnlock()
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxJournalBytes)
	for scanner.Scan() {
		var row struct {
			Message  string `json:"MESSAGE"`
			Stamp    string `json:"__REALTIME_TIMESTAMP"`
			Priority string `json:"PRIORITY"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		msg := row.Message
		for _, secret := range secrets {
			if secret != "" {
				msg = strings.ReplaceAll(msg, secret, "[已隐藏]")
			}
		}
		// URL paths, queries and userinfo may carry subscription or Bot tokens.
		msg = logURL.ReplaceAllString(msg, "[链接已隐藏]")
		msg = logAuthorization.ReplaceAllString(msg, "authorization$1[已隐藏]")
		msg = logSecret.ReplaceAllString(msg, "$1$2[已隐藏]")
		msg = logANSI.ReplaceAllString(msg, "")
		msg = strings.Map(func(r rune) rune {
			if r < 32 && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, msg)
		if len(msg) > 8192 {
			msg = string([]rune(msg)[:min(len([]rune(msg)), 2048)]) + "…[已截断]"
		}
		entry := ServiceLogEntry{Level: "info", Message: msg}
		if stamp, err := strconv.ParseInt(row.Stamp, 10, 64); err == nil && stamp > 0 {
			entry.Time = time.UnixMicro(stamp).UTC().Format(time.RFC3339)
		}
		if p, err := strconv.Atoi(row.Priority); err == nil {
			switch {
			case p <= 3:
				entry.Level = "error"
			case p == 4:
				entry.Level = "warning"
			case p == 7:
				entry.Level = "debug"
			}
		}
		result.Entries = append(result.Entries, entry)
		if len(result.Entries) > lines {
			result.Entries = result.Entries[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("服务日志记录过大，暂时无法显示")
	}
	return result, nil
}

type cappedJournalBuffer struct{ bytes.Buffer }

func (b *cappedJournalBuffer) Write(p []byte) (int, error) {
	if len(p) > maxJournalBytes-b.Len() {
		return 0, fmt.Errorf("journal output too large")
	}
	return b.Buffer.Write(p)
}

func readServiceJournal(ctx context.Context, lines int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "journalctl", "--unit=5gpn-next.service", "--no-pager", "--quiet", "--output=json", "--output-fields=MESSAGE,__REALTIME_TIMESTAMP,PRIORITY", "-n", strconv.Itoa(lines))
	var out cappedJournalBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
