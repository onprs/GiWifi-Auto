//go:build linux

package daemon

import (
	"net/http"
	"testing"
)

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
