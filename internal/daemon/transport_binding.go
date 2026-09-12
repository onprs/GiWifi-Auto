package daemon

import (
	"fmt"
	"net/http"
	"strings"
)

// bindAccountTransport 为指定网卡复制并配置一个 HTTP Transport。
func bindAccountTransport(base http.RoundTripper, interfaceName string) (http.RoundTripper, error) {
	if base == nil {
		return nil, fmt.Errorf("HTTP Transport 不能为空")
	}
	if interfaceName == "" {
		return base, nil
	}
	if strings.TrimSpace(interfaceName) != interfaceName {
		return nil, fmt.Errorf("网络接口名称不能包含首尾空白")
	}

	transport, ok := base.(*http.Transport)
	if !ok || transport == nil {
		return nil, fmt.Errorf("按网络接口绑定要求使用 *http.Transport")
	}
	bound := transport.Clone()
	if err := bindHTTPTransportToInterface(bound, interfaceName); err != nil {
		return nil, err
	}
	return bound, nil
}
