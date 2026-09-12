package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
)

// DiscoverWANDevices 按 OpenWrt network 配置顺序返回可用于 WAN 的设备名。
func DiscoverWANDevices(ctx context.Context) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("网络接口发现上下文不能为空")
	}
	output, err := runUCI(ctx, "show", "network")
	if err != nil {
		return nil, fmt.Errorf("读取 OpenWrt 网络配置失败: %w", err)
	}

	return parseWANDevices(output)
}

func parseWANDevices(data []byte) ([]string, error) {
	type networkSection struct {
		name   string
		proto  string
		device string
	}
	sections := make([]networkSection, 0)
	sectionIndex := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		left, rawValue, found := strings.Cut(line, "=")
		if !found || !strings.HasPrefix(left, "network.") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(left, "network."), ".")
		if len(parts) != 2 {
			continue
		}
		sectionName, option := parts[0], parts[1]
		index, exists := sectionIndex[sectionName]
		if !exists {
			index = len(sections)
			sectionIndex[sectionName] = index
			sections = append(sections, networkSection{name: sectionName})
		}
		value := strings.Trim(strings.TrimSpace(rawValue), "'\"")
		switch option {
		case "proto":
			sections[index].proto = value
		case "device", "ifname":
			if sections[index].device == "" {
				sections[index].device = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("解析 OpenWrt 网络配置失败: %w", err)
	}

	devices := make([]string, 0, len(sections))
	seen := make(map[string]struct{})
	for _, section := range sections {
		if !isWANSection(section.name, section.proto) || section.device == "" {
			continue
		}
		if _, exists := seen[section.device]; exists {
			continue
		}
		seen[section.device] = struct{}{}
		devices = append(devices, section.device)
	}
	return devices, nil
}

func isWANSection(name, proto string) bool {
	if name == "wan" {
		return proto != "dhcpv6" && proto != "none"
	}
	if !strings.HasPrefix(name, "wan") || name == "wan6" || proto == "dhcpv6" || proto == "none" {
		return false
	}
	for _, character := range name[len("wan"):] {
		if (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return len(name) > len("wan")
}
