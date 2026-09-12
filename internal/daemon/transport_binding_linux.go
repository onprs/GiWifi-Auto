//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"

	"golang.org/x/sys/unix"
)

func bindHTTPTransportToInterface(transport *http.Transport, interfaceName string) error {
	if transport.DialContext != nil || transport.DialTLSContext != nil {
		return errors.New("HTTP Transport 已自定义拨号器，无法安全绑定网络接口")
	}

	control := func(_ context.Context, _, _ string, rawConn syscall.RawConn) error {
		var bindErr error
		if err := rawConn.Control(func(fd uintptr) {
			bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
		}); err != nil {
			return fmt.Errorf("访问底层连接失败: %w", err)
		}
		if bindErr != nil {
			return fmt.Errorf("设置 SO_BINDTODEVICE 失败: %w", bindErr)
		}
		return nil
	}

	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{ControlContext: control}
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
