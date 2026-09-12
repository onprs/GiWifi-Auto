//go:build !linux

package daemon

import (
	"errors"
	"net/http"
)

func bindHTTPTransportToInterface(_ *http.Transport, _ string) error {
	return errors.New("按网络接口绑定账号仅支持 Linux/OpenWrt")
}
