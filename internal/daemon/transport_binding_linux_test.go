//go:build linux

package daemon

import (
	"net/http"
	"testing"
)

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
