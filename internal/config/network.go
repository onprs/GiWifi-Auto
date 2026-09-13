package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// DiscoverWANDevices 按运行时 IPv4 默认路由返回可用于认证的出口设备名。
//
// 默认路由比 UCI section 名称更接近实际数据路径，因此不依赖 wan、VLAN
// 编号或父网卡命名。只有当前确实拥有 IPv4 地址的设备才会参与自动分配。
func DiscoverWANDevices(ctx context.Context) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("网络接口发现上下文不能为空")
	}

	output, err := runIPRoute(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("读取运行时 IPv4 默认路由失败: %w", err)
	}

	candidates := parseDefaultRouteDevices(output)
	devices := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if interfaceHasIPv4(candidate) {
			devices = append(devices, candidate)
		}
	}
	if len(devices) == 0 {
		return nil, errors.New("未发现拥有 IPv4 地址的默认路由接口")
	}
	return devices, nil
}

func runIPRoute(ctx context.Context) ([]byte, error) {
	command := exec.CommandContext(ctx, "ip", "-4", "route", "show", "table", "all", "default")
	command.Stderr = io.Discard
	var output limitedCommandBuffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		if output.tooLarge {
			return nil, errors.New("运行时路由输出超过大小限制")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if output.tooLarge {
		return nil, errors.New("运行时路由输出超过大小限制")
	}
	return output.Bytes(), nil
}

func parseDefaultRouteDevices(data []byte) []string {
	type routeDevice struct {
		name   string
		metric int
	}

	devices := make(map[string]routeDevice)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}

		device := ""
		metric := 0
		for index := 0; index+1 < len(fields); index++ {
			switch fields[index] {
			case "dev":
				device = fields[index+1]
			case "metric":
				if parsed, err := strconv.Atoi(fields[index+1]); err == nil && parsed >= 0 {
					metric = parsed
				}
			}
		}
		if device == "" {
			continue
		}

		current, exists := devices[device]
		if !exists || metric < current.metric {
			devices[device] = routeDevice{name: device, metric: metric}
		}
	}

	ordered := make([]routeDevice, 0, len(devices))
	for _, device := range devices {
		ordered = append(ordered, device)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].metric != ordered[right].metric {
			return ordered[left].metric < ordered[right].metric
		}
		return ordered[left].name < ordered[right].name
	})

	result := make([]string, 0, len(ordered))
	for _, device := range ordered {
		result = append(result, device.name)
	}
	return result
}

func interfaceHasIPv4(name string) bool {
	networkInterface, err := net.InterfaceByName(name)
	if err != nil {
		return false
	}
	addresses, err := networkInterface.Addrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		default:
			parsed, _, parseErr := net.ParseCIDR(address.String())
			if parseErr != nil {
				continue
			}
			ip = parsed
		}
		if ipv4 := ip.To4(); ipv4 != nil && !ipv4.IsUnspecified() && !ipv4.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}
