package daemon

import (
	"net/http"
	"testing"
)

type transportRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn transportRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestBindAccountTransportWithoutInterfaceKeepsTransport(t *testing.T) {
	base := &http.Transport{}
	got, err := bindAccountTransport(base, "")
	if err != nil {
		t.Fatalf("无网络接口绑定时返回错误: %v", err)
	}
	if got != base {
		t.Fatal("无网络接口绑定时不应复制 Transport")
	}
}

func TestBindAccountTransportRequiresHTTPTransport(t *testing.T) {
	_, err := bindAccountTransport(transportRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, nil
	}), "eth1")
	if err == nil {
		t.Fatal("非 HTTP Transport 未被拒绝")
	}
}
