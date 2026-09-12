package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"strings"
)

func interfaceGatewayURL(ctx context.Context, interfaceName string) (string, error) {
	if ctx == nil {
		return "", errors.New("网关查询上下文不能为空")
	}
	if strings.TrimSpace(interfaceName) == "" {
		return "", errors.New("网络接口名称不能为空")
	}

	command := exec.CommandContext(ctx, "ip", "-4", "route", "show", "table", "all", "default", "dev", interfaceName)
	command.Stderr = io.Discard
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("读取网络接口默认网关失败: %w", err)
	}
	return parseInterfaceGatewayURL(output)
}

func parseInterfaceGatewayURL(output []byte) (string, error) {
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		for index := 0; index+1 < len(fields); index++ {
			if fields[index] != "via" {
				continue
			}
			gateway := net.ParseIP(fields[index+1])
			if gateway == nil || gateway.To4() == nil {
				continue
			}
			return (&url.URL{Scheme: "http", Host: gateway.To4().String(), Path: "/"}).String(), nil
		}
	}
	return "", errors.New("未找到网络接口默认网关")
}
