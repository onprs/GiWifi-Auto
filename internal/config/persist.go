package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SaveFileAtomic 校验并原子替换 JSON 配置文件，失败时不覆盖原文件。
func SaveFileAtomic(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置保存前校验失败: %w", err)
	}
	if path == "" {
		return fmt.Errorf("配置路径不能为空")
	}

	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".giwifi-config-*.tmp")
	if err != nil {
		return fmt.Errorf("创建配置临时文件失败: %w", err)
	}
	temporaryPath := file.Name()
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("设置配置临时文件权限失败: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("编码配置失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步配置临时文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭配置临时文件失败: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("原子替换配置文件失败: %w", err)
	}
	removeTemporary = false

	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("同步配置目录失败: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return nil
}
