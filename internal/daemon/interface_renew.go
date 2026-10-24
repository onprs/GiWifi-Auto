package daemon

import (
	"context"
	"time"
)

// InterfaceRenewer 让指定网络设备重新获取地址，返回执行重连的网络接口名。
type InterfaceRenewer func(ctx context.Context, device string) (string, error)

// interfaceRenewTriggerDelay 是接口重建后唤醒账号的等待时间。
// netifd 的 ifup 会先拆除接口再重新获取地址，立即探测会得到一次无意义的离线结果。
var interfaceRenewTriggerDelay = 5 * time.Second
