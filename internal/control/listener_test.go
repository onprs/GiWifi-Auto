package control

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnixListenerSetsPermissionAndCleansUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 OpenWrt 使用的 Unix Socket 权限语义")
	}
	path := filepath.Join(t.TempDir(), "giwifi-auto.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() 失败: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() 失败: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Socket 权限 = %o, want 600", info.Mode().Perm())
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() 失败: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Socket 文件未清理，Stat 错误 = %v", err)
	}
}

func TestUnixListenerDoesNotRemoveRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 OpenWrt 使用的 Unix Socket")
	}
	path := filepath.Join(t.TempDir(), "giwifi-auto.sock")
	if err := os.WriteFile(path, []byte("do-not-remove"), 0o600); err != nil {
		t.Fatalf("写入占位文件失败: %v", err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("普通文件被当作 Socket 删除")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("占位文件被删除: %v", err)
	}
}
