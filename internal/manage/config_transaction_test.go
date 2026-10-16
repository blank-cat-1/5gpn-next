package manage

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/w0ven/5gpn-next/internal/config"
	"github.com/w0ven/5gpn-next/internal/egress"
	"github.com/w0ven/5gpn-next/internal/policy"
)

func TestConfigTransactionRequiresBackupAndRestoresThroughSandboxWriter(t *testing.T) {
	for _, mode := range []string{"backup-failure", "reload-failure", "restore-failure", "recovery-reload-failure"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := subscriptionManager(t)
			before, _ := os.ReadFile(m.ConfigPath)
			var writes []string
			m.configWrite = func(path string, data []byte, perm os.FileMode) error {
				writes = append(writes, path)
				if perm != 0o600 {
					t.Error("backup/restore is not private")
				}
				if mode == "backup-failure" || (mode == "restore-failure" && path == m.ConfigPath) {
					return os.ErrPermission
				}
				return config.WriteFileSandboxSafe(path, data, perm)
			}
			reloads := 0
			m.Reload = func() (*policy.Engine, *egress.Registry, error) {
				reloads++
				if reloads == 1 || mode == "recovery-reload-failure" {
					return nil, nil, errors.New("injected reload failure")
				}
				return policy.New(), egress.NewRegistry(), nil
			}
			m.Cfg.Final = "direct"
			m.mu.Lock()
			err := m.saveAndReloadLocked()
			m.mu.Unlock()
			if err == nil {
				t.Fatal("expected transaction failure")
			}
			after, _ := os.ReadFile(m.ConfigPath)
			if mode == "backup-failure" {
				if reloads != 0 || !bytes.Equal(before, after) || m.Cfg.Final != "proxy:manual" {
					t.Fatal("failed backup allowed configuration mutation")
				}
				return
			}
			if len(writes) != 2 || writes[0] != m.ConfigPath+".rollback" || writes[1] != m.ConfigPath {
				t.Fatal("backup and restore did not share sandbox-safe writer")
			}
			if mode == "restore-failure" || mode == "recovery-reload-failure" {
				if !errors.Is(err, errConfigRollback) {
					t.Fatal("incomplete restoration was reported as successful")
				}
				return
			}
			if reloads != 2 || !bytes.Equal(before, after) || m.Cfg.Final != "proxy:manual" {
				t.Fatal("original config was not restored exactly")
			}
		})
	}
}
