package manage

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

var subscriptionIDPattern = regexp.MustCompile(`^sub-[a-f0-9]{16}$`)

type subscriptionRuntime interface {
	Apply(id string, data []byte, addresses []string) (undo func() error, err error)
	Remove(id string) error
}

type localSubscriptionRuntime struct{}

func (m *Manager) subscriptionRuntime() subscriptionRuntime {
	if m.subRuntime != nil {
		return m.subRuntime
	}
	return localSubscriptionRuntime{}
}

func (localSubscriptionRuntime) Apply(id string, data []byte, addresses []string) (func() error, error) {
	if !subscriptionIDPattern.MatchString(id) {
		return nil, fmt.Errorf("订阅 ID 无效")
	}
	if err := ensureMihomo(); err != nil {
		return nil, fmt.Errorf("安装订阅协议栈失败")
	}
	if err := ensureMihomoUnit(); err != nil {
		return nil, fmt.Errorf("准备订阅服务失败")
	}
	dir := filepath.Join(egressDir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("创建订阅目录失败")
	}
	path := filepath.Join(dir, "config.yaml")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取原订阅配置失败")
	}
	existed := err == nil
	// No secret-bearing config or mihomo parser output is returned to the browser.
	f, err := os.CreateTemp(dir, "pending-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("创建订阅配置失败")
	}
	staged := f.Name()
	defer os.Remove(staged)
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return nil, fmt.Errorf("写入订阅配置失败")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Validation runs in its own data directory and cannot modify live state.
	checkDir, err := os.MkdirTemp(dir, "check-")
	if err != nil {
		return nil, fmt.Errorf("创建订阅校验目录失败")
	}
	defer os.RemoveAll(checkDir)
	cmd := exec.CommandContext(ctx, "/usr/local/bin/mihomo", "-t", "-d", checkDir, "-f", staged)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("订阅节点未通过 mihomo 配置校验，原订阅保持不变")
	}
	unit := "mihomo-5gpn@" + id + ".service"
	undo := func() error {
		if existed {
			if err := atomicSubscriptionFile(path, old); err != nil {
				return err
			}
			return systemctl("restart", unit)
		}
		if err := systemctl("disable", "--now", unit); err != nil {
			return err
		}
		return os.RemoveAll(dir)
	}
	if err := atomicSubscriptionFile(path, data); err != nil {
		return nil, fmt.Errorf("应用订阅配置失败")
	}
	startErr := systemctl("enable", unit)
	if startErr == nil {
		startErr = systemctl("restart", unit)
	}
	if startErr == nil {
		startErr = waitSubscriptionListeners(addresses)
	}
	if startErr != nil {
		if err := undo(); err != nil {
			return nil, fmt.Errorf("启动订阅服务失败且恢复失败，请检查服务状态")
		}
		return nil, fmt.Errorf("订阅服务未能启动，已恢复原配置")
	}
	return undo, nil
}

func atomicSubscriptionFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func waitSubscriptionListeners(addresses []string) error {
	deadline := time.Now().Add(8 * time.Second)
	for _, addr := range addresses {
		for {
			conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if err == nil {
				_ = conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
				_, err = conn.Write([]byte{5, 1, 0})
				var reply [2]byte
				if err == nil {
					_, err = io.ReadFull(conn, reply[:])
				}
				_ = conn.Close()
				if err == nil && reply == [2]byte{5, 0} {
					break
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("订阅监听端口未就绪")
			}
			time.Sleep(80 * time.Millisecond)
		}
	}
	return nil
}

func (localSubscriptionRuntime) Remove(id string) error {
	if !subscriptionIDPattern.MatchString(id) {
		return fmt.Errorf("订阅 ID 无效")
	}
	dir := filepath.Join(egressDir, id)
	path := filepath.Join(dir, "config.yaml")
	old, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	unit := "mihomo-5gpn@" + id + ".service"
	if err := systemctl("disable", "--now", unit); err != nil {
		_ = systemctl("enable", "--now", unit)
		return err
	}
	// The generated ID is checked before resolving or recursively deleting.
	if err := os.RemoveAll(dir); err != nil {
		// A partial filesystem cleanup must not leave a restored configuration
		// pointing at a stopped or unconfigured service.
		if len(old) > 0 {
			if rerr := os.MkdirAll(dir, 0o750); rerr != nil {
				return fmt.Errorf("删除目录失败且无法恢复目录")
			}
			if rerr := atomicSubscriptionFile(path, old); rerr != nil {
				return fmt.Errorf("删除目录失败且无法恢复配置")
			}
			if rerr := systemctl("enable", "--now", unit); rerr != nil {
				return fmt.Errorf("删除目录失败且无法恢复服务")
			}
		}
		return err
	}
	return nil
}
