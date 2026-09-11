package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveFileAtomicWritesValidatedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.Accounts = []AccountConfig{{ID: "sample", DisplayName: "示例"}}

	if err := SaveFileAtomic(path, cfg); err != nil {
		t.Fatalf("SaveFileAtomic() 失败: %v", err)
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() 失败: %v", err)
	}
	if len(loaded.Accounts) != 1 || loaded.Accounts[0].ID != "sample" {
		t.Fatalf("保存结果 = %+v", loaded)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat() 失败: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("配置权限 = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestSaveFileAtomicRejectsInvalidConfigWithoutCreatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.Version = CurrentVersion + 1

	err := SaveFileAtomic(path, cfg)
	if err == nil || !strings.Contains(err.Error(), "校验") {
		t.Fatalf("SaveFileAtomic() 错误 = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("非法配置不应创建目标文件，Stat 错误 = %v", statErr)
	}
}

func TestSaveFileAtomicKeepsPreviousFileWhenDirectoryIsInvalid(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(directory, "config.json")
	if err := SaveFileAtomic(path, Default()); err == nil {
		t.Fatal("不存在的配置目录未返回错误")
	}
}
