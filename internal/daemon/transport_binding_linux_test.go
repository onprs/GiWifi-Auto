//go:build linux

package daemon

import (
	"net"
	"net/http"
	"testing"
)

func TestSelectInterfaceIPv4Address(t *testing.T) {
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)},
		&net.IPNet{IP: net.ParseIP("169.254.1.2"), Mask: net.CIDRMask(16, 32)},
		&net.IPAddr{IP: net.ParseIP("10.20.1.159")},
	}

	got := selectInterfaceIPv4Address(addresses)
	if got == nil || got.String() != "10.20.1.159" {
		t.Fatalf("选中的接口 IPv4 地址 = %v", got)
	}
}

func TestInterfaceTCPAddressSkipsIPv6Destination(t *testing.T) {
	if got := interfaceTCPAddress("lo", "tcp6", "[::1]:80"); got != nil {
		t.Fatalf("IPv6 目标不应绑定 IPv4 本地地址: %v", got)
	}
}
func TestParseInterfaceRoutingMark(t *testing.T) {
	routeOutput := []byte("default via 10.20.1.1 dev eth1.103 table 3 proto static\n")
	ruleOutput := []byte("2003: from all fwmark 0x300/0x3f00 lookup 3\n")

	mark, found := parseInterfaceRoutingMark(routeOutput, ruleOutput)
	if !found || mark != 0x300 {
		t.Fatalf("接口策略路由标记 = %#x, %v", mark, found)
	}
}
func TestDefaultTransportCanBindAccountInterface(t *testing.T) {
	base := defaultTransport()
	transport, ok := base.(*http.Transport)
	if !ok {
		t.Fatalf("默认 Transport 类型 = %T", base)
	}
	if transport.DialContext != nil || transport.DialTLSContext != nil {
		t.Fatal("默认 Transport 不应预先设置绕过网卡绑定的拨号器")
	}
	if _, err := bindAccountTransport(base, "eth1"); err != nil {
		t.Fatalf("默认 Transport 绑定网络接口失败: %v", err)
	}
}
func TestBindAccountTransportClonesAndAddsDialer(t *testing.T) {
	base := &http.Transport{}
	got, err := bindAccountTransport(base, "eth1")
	if err != nil {
		t.Fatalf("绑定网络接口失败: %v", err)
	}
	bound, ok := got.(*http.Transport)
	if !ok {
		t.Fatalf("绑定后 Transport 类型 = %T", got)
	}
	if bound == base {
		t.Fatal("绑定网络接口时应复制 Transport")
	}
	if bound.DialContext == nil {
		t.Fatal("绑定网络接口时未设置拨号器")
	}
	if base.DialContext != nil {
		t.Fatal("绑定网络接口时不应修改原 Transport")
	}
}
