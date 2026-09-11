package config

import (
	"context"
	"fmt"
	"strings"
)

// SaveAccountEnabled 按配置格式持久化账号启用状态。
func SaveAccountEnabled(ctx context.Context, path string, cfg Config, id string, enabled bool) error {
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		return SaveFileAtomic(path, cfg)
	}
	if ctx == nil {
		return fmt.Errorf("配置更新上下文不能为空")
	}
	return UpdateUCIAccountEnabled(ctx, path, id, enabled)
}
