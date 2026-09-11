package daemon

import "context"

// requestLimiter 是所有账号共享的有界请求信号量。
type requestLimiter struct {
	slots chan struct{}
}

func newRequestLimiter(limit int) *requestLimiter {
	return &requestLimiter{slots: make(chan struct{}, limit)}
}

func (limiter *requestLimiter) Acquire(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	select {
	case limiter.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (limiter *requestLimiter) Release() {
	select {
	case <-limiter.slots:
	default:
	}
}
