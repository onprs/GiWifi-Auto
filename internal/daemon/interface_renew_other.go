//go:build !linux

package daemon

import (
	"context"
	"errors"
)

// defaultInterfaceRenewer 在非 Linux 系统上不可用。
func defaultInterfaceRenewer(context.Context, string) (string, error) {
	return "", errors.New("当前系统不支持重新获取地址")
}
