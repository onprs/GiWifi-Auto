//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// defaultInterfaceRenewer 通过 netifd 对设备所在的网络接口重新执行一次 ifup。
func defaultInterfaceRenewer(ctx context.Context, device string) (string, error) {
	name, err := netifdInterfaceName(ctx, device)
	if err != nil {
		return "", err
	}
	ifup, err := exec.LookPath("ifup")
	if err != nil {
		return "", errors.New("当前系统缺少 ifup 命令，无法重新获取地址")
	}
	output, err := exec.CommandContext(ctx, ifup, name).CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(output)); message != "" {
			return "", fmt.Errorf("重新获取 %s 的地址失败: %s", name, message)
		}
		return "", fmt.Errorf("重新获取 %s 的地址失败: %w", name, err)
	}
	return name, nil
}

type netifdInterface struct {
	Name     string `json:"interface"`
	Device   string `json:"device"`
	L3Device string `json:"l3_device"`
}

type netifdDump struct {
	Interfaces []netifdInterface `json:"interface"`
}

// netifdInterfaceName 按设备名查找 netifd 中的网络接口名。
func netifdInterfaceName(ctx context.Context, device string) (string, error) {
	output, err := exec.CommandContext(ctx, "ubus", "call", "network.interface", "dump").Output()
	if err != nil {
		return "", errors.New("读取网络接口失败，当前系统可能不支持重新获取地址")
	}
	var dump netifdDump
	if err := json.Unmarshal(output, &dump); err != nil {
		return "", errors.New("解析网络接口列表失败")
	}
	name, found := matchNetifdInterface(dump, device)
	if !found {
		return "", fmt.Errorf("未找到绑定 %s 的网络接口", device)
	}
	return name, nil
}

// matchNetifdInterface 优先按三层设备匹配，其次按配置设备匹配。
func matchNetifdInterface(dump netifdDump, device string) (string, bool) {
	if strings.TrimSpace(device) == "" {
		return "", false
	}
	for _, item := range dump.Interfaces {
		if item.L3Device == device {
			return item.Name, true
		}
	}
	for _, item := range dump.Interfaces {
		if item.Device == device {
			return item.Name, true
		}
	}
	return "", false
}
