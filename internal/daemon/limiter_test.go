package daemon

import (
	"context"
	"testing"
	"time"
)

func TestRequestLimiterHonorsCapacityAndCancellation(t *testing.T) {
	limiter := newRequestLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("第一次获取并发许可失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := limiter.Acquire(ctx); err == nil {
		t.Fatal("达到容量后仍获取到了并发许可")
	} else if ctx.Err() == nil {
		t.Fatalf("并发许可错误未由上下文触发: %v", err)
	}

	limiter.Release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := limiter.Acquire(ctx); err != nil {
		t.Fatalf("释放后无法重新获取并发许可: %v", err)
	}
	limiter.Release()
}

func TestRequestLimiterDoesNotReleaseUnheldSlot(t *testing.T) {
	limiter := newRequestLimiter(1)
	limiter.Release()
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("未持有许可时 Release 破坏了信号量: %v", err)
	}
	limiter.Release()
}
