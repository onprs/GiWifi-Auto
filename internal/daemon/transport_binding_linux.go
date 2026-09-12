//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func bindHTTPTransportToInterface(transport *http.Transport, interfaceName string) error {
	if transport.DialContext != nil || transport.DialTLSContext != nil {
		return errors.New("HTTP Transport 已自定义拨号器，无法安全绑定网络接口")
	}

	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		mark, markFound := interfaceRoutingMark(ctx, interfaceName)
		dialer := &net.Dialer{
			ControlContext: func(_ context.Context, _, _ string, rawConn syscall.RawConn) error {
				var bindErr error
				var socketFD int
				if err := rawConn.Control(func(fd uintptr) {
					socketFD = int(fd)
					bindErr = unix.SetsockoptString(socketFD, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
				}); err != nil {
					return fmt.Errorf("访问底层连接失败: %w", err)
				}
				if bindErr != nil {
					return fmt.Errorf("设置 SO_BINDTODEVICE 失败: %w", bindErr)
				}
				if markFound {
					if err := unix.SetsockoptInt(socketFD, unix.SOL_SOCKET, unix.SO_MARK, int(mark)); err != nil {
						return fmt.Errorf("设置策略路由标记失败: %w", err)
					}
				}
				return nil
			},
		}
		if localAddress := interfaceTCPAddress(interfaceName, network, address); localAddress != nil {
			dialer.LocalAddr = localAddress
		}
		return dialer.DialContext(ctx, network, address)
	}
	return nil
}

func interfaceTCPAddress(interfaceName, network, address string) *net.TCPAddr {
	if network == "tcp6" {
		return nil
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return nil
		}
	}
	ip := interfaceIPv4Address(interfaceName)
	if ip == nil {
		return nil
	}
	return &net.TCPAddr{IP: ip}
}

func interfaceIPv4Address(interfaceName string) net.IP {
	networkInterface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil
	}
	addresses, err := networkInterface.Addrs()
	if err != nil {
		return nil
	}
	return selectInterfaceIPv4Address(addresses)
}

func interfaceRoutingMark(ctx context.Context, interfaceName string) (uint32, bool) {
	if ctx == nil || strings.TrimSpace(interfaceName) == "" {
		return 0, false
	}
	routeOutput, err := exec.CommandContext(ctx, "ip", "-4", "route", "show", "table", "all", "default", "dev", interfaceName).Output()
	if err != nil {
		return 0, false
	}
	ruleOutput, err := exec.CommandContext(ctx, "ip", "-4", "rule", "show").Output()
	if err != nil {
		return 0, false
	}
	return parseInterfaceRoutingMark(routeOutput, ruleOutput)
}

func parseInterfaceRoutingMark(routeOutput, ruleOutput []byte) (uint32, bool) {
	tables := make(map[string]struct{})
	for _, line := range strings.Split(string(routeOutput), "\n") {
		fields := strings.Fields(line)
		for index := 0; index+1 < len(fields); index++ {
			if fields[index] == "table" && fields[index+1] != "main" && fields[index+1] != "local" {
				tables[fields[index+1]] = struct{}{}
			}
		}
	}
	if len(tables) == 0 {
		return 0, false
	}

	for _, line := range strings.Split(string(ruleOutput), "\n") {
		fields := strings.Fields(line)
		mark := ""
		lookupTable := ""
		for index := 0; index+1 < len(fields); index++ {
			switch fields[index] {
			case "fwmark":
				mark = fields[index+1]
			case "lookup":
				lookupTable = fields[index+1]
			}
		}
		if mark == "" || lookupTable == "" {
			continue
		}
		if _, exists := tables[lookupTable]; !exists {
			continue
		}
		mark = strings.SplitN(mark, "/", 2)[0]
		value, err := strconv.ParseUint(mark, 0, 32)
		if err != nil || value == 0 {
			continue
		}
		return uint32(value), true
	}
	return 0, false
}

func selectInterfaceIPv4Address(addresses []net.Addr) net.IP {
	for _, address := range addresses {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		default:
			continue
		}
		if ipv4 := ip.To4(); ipv4 != nil && !ipv4.IsUnspecified() && !ipv4.IsLinkLocalUnicast() {
			return append(net.IP(nil), ipv4...)
		}
	}
	return nil
}
