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

	dialer := &net.Dialer{
		ControlContext: func(_ context.Context, _, _ string, rawConn syscall.RawConn) error {
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
		},
	}
	transport.DialContext = dialer.DialContext
	return nil
}
