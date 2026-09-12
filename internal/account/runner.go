package account

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/config"
	"github.com/onprs/GiWifi-Auto/internal/connectivity"
	"github.com/onprs/GiWifi-Auto/internal/credential"
	"github.com/onprs/GiWifi-Auto/internal/portal"
)

// State 是账号运行器的可观测状态。
type State string

const (
	StateDisabled       State = "disabled"
	StateIdle           State = "idle"
	StateChecking       State = "checking"
	StateOffline        State = "offline"
	StatePortal         State = "portal"
	StateAuthenticating State = "authenticating"
	StateAuthenticated  State = "authenticated"
	StateBackoff        State = "backoff"
	StateError          State = "error"
)

// Probe 是连通性探测依赖。
type Probe interface {
	Check(context.Context, string) (connectivity.Result, error)
}

// RequestLimiter 限制所有账号共享的网络请求并发数。
type RequestLimiter interface {
	Acquire(context.Context) error
	Release()
}

// Authenticator 是 Portal 登录依赖。
type Authenticator interface {
	Authenticate(context.Context, *url.URL, string, string) error
}

// RunnerConfig 是单个账号运行器的配置快照。
type RunnerConfig struct {
	Account         config.AccountConfig
	ConnectivityURL string
	CheckInterval   time.Duration
	RequestTimeout  time.Duration
	RetryInitial    time.Duration
	RetryMax        time.Duration
}

// Dependencies 是运行器的外部依赖和可注入时钟。
type Dependencies struct {
	Probe         Probe
	Authenticator Authenticator
	Credentials   credential.Resolver
	Limiter       RequestLimiter
	Now           func() time.Time
	Jitter        func(time.Duration) time.Duration
	Publish       func(Event)
}

// Event 描述一次账号状态或操作变化。
type Event struct {
	At            time.Time
	AccountID     string
	State         State
	Operation     string
	Category      string
	Message       string
	RetryCount    int
	NextAttemptAt *time.Time
}

// Snapshot 是不含用户名和凭据的账号状态摘要。
type Snapshot struct {
	ID            string              `json:"id"`
	DisplayName   string              `json:"display_name"`
	Enabled       bool                `json:"enabled"`
	State         State               `json:"state"`
	LastResult    connectivity.Status `json:"last_result,omitempty"`
	LastError     string              `json:"last_error,omitempty"`
	ErrorCategory string              `json:"error_category,omitempty"`
	RetryCount    int                 `json:"retry_count"`
	LastSuccessAt *time.Time          `json:"last_success_at,omitempty"`
	NextAttemptAt *time.Time          `json:"next_attempt_at,omitempty"`
}

// Runner 拥有一个账号的状态、退避和生命周期。
type Runner struct {
	mu          sync.RWMutex
	config      RunnerConfig
	deps        Dependencies
	snapshot    Snapshot
	cycleCancel context.CancelFunc
	trigger     chan struct{}
}

// New 创建一个账号运行器。
func New(runtime RunnerConfig, deps Dependencies) (*Runner, error) {
	if deps.Probe == nil {
		return nil, errors.New("账号运行器缺少连通性探测器")
	}
	if deps.Authenticator == nil {
		return nil, errors.New("账号运行器缺少 Portal 认证器")
	}
	if deps.Credentials == nil {
		return nil, errors.New("账号运行器缺少凭据解析器")
	}
	if runtime.Account.ID == "" || runtime.ConnectivityURL == "" {
		return nil, errors.New("账号运行器配置不完整")
	}
	if runtime.CheckInterval <= 0 || runtime.RetryInitial <= 0 || runtime.RetryMax <= 0 {
		return nil, errors.New("账号运行器时间参数必须大于 0")
	}
	if runtime.RequestTimeout < 0 {
		return nil, errors.New("账号运行器请求超时不能小于 0")
	}
	if runtime.RequestTimeout == 0 {
		runtime.RequestTimeout = 10 * time.Second
	}
	if runtime.RetryInitial > runtime.RetryMax {
		return nil, errors.New("账号运行器退避上限不能小于初始值")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Jitter == nil {
		deps.Jitter = secureJitter
	}

	state := StateIdle
	if !runtime.Account.Enabled {
		state = StateDisabled
	}
	return &Runner{
		config: runtime,
		deps:   deps,
		snapshot: Snapshot{
			ID:          runtime.Account.ID,
			DisplayName: runtime.Account.DisplayName,
			Enabled:     runtime.Account.Enabled,
			State:       state,
		},
		trigger: make(chan struct{}, 1),
	}, nil
}

// Run 启动账号循环，直到 ctx 取消。所有等待均可被取消或手动唤醒。
func (r *Runner) Run(ctx context.Context) {
	if ctx == nil {
		return
	}
	for {
		if !r.isEnabled() {
			r.emit(StateDisabled, "idle", "", "账号已停用", nil)
			if !r.waitForTrigger(ctx) {
				return
			}
			continue
		}

		cycleContext, cancel := r.startCycle(ctx)
		delay, waitForTrigger := r.step(cycleContext)
		cancel()
		if waitForTrigger {
			if !r.waitForTrigger(ctx) {
				return
			}
			continue
		}
		if !r.wait(ctx, delay) {
			return
		}
	}
}

// Trigger 唤醒当前账号的下一次检查，重复触发会合并。
func (r *Runner) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// SetEnabled 修改运行时启停状态。持久化由上层服务负责。
func (r *Runner) SetEnabled(enabled bool) {
	now := r.deps.Now()
	var activeCancel context.CancelFunc
	r.mu.Lock()
	r.config.Account.Enabled = enabled
	r.snapshot.Enabled = enabled
	r.snapshot.RetryCount = 0
	r.snapshot.NextAttemptAt = nil
	if !enabled {
		r.snapshot.State = StateDisabled
		r.snapshot.LastError = ""
		r.snapshot.ErrorCategory = ""
	} else {
		r.snapshot.State = StateIdle
	}
	event := Event{
		At:        now,
		AccountID: r.config.Account.ID,
		State:     r.snapshot.State,
		Operation: "set_enabled",
		Message:   "账号状态已更新",
	}
	activeCancel = r.cycleCancel
	r.mu.Unlock()
	if activeCancel != nil {
		activeCancel()
	}
	r.publish(event)
	r.Trigger()
}

// Snapshot 返回当前账号状态的副本。
func (r *Runner) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneSnapshot(r.snapshot)
}

func (r *Runner) startCycle(parent context.Context) (context.Context, context.CancelFunc) {
	cycleContext, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.cycleCancel = cancel
	r.mu.Unlock()
	return cycleContext, func() {
		cancel()
		r.mu.Lock()
		r.cycleCancel = nil
		r.mu.Unlock()
	}
}

func (r *Runner) check(ctx context.Context) (connectivity.Result, error) {
	if r.deps.Limiter != nil {
		if err := r.deps.Limiter.Acquire(ctx); err != nil {
			return connectivity.Result{Status: connectivity.StatusOffline}, err
		}
		defer r.deps.Limiter.Release()
	}
	return r.deps.Probe.Check(ctx, r.config.ConnectivityURL)
}

func (r *Runner) authenticateRequest(ctx context.Context, pageURL *url.URL, username, password string) error {
	if r.deps.Limiter != nil {
		if err := r.deps.Limiter.Acquire(ctx); err != nil {
			return err
		}
		defer r.deps.Limiter.Release()
	}
	return r.deps.Authenticator.Authenticate(ctx, pageURL, username, password)
}

func (r *Runner) step(ctx context.Context) (time.Duration, bool) {
	if !r.isEnabled() {
		return 0, true
	}
	r.emit(StateChecking, "check", "", "正在检查网络", nil)
	result, err := r.check(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return 0, true
		}
		category := errorCategory(err, string(connectivity.CategoryNetwork))
		if category == string(connectivity.CategoryNetwork) || result.Status == connectivity.StatusOffline {
			r.emit(StateOffline, "check", category, "网络暂时不可达", nil)
			return r.scheduleBackoff(category, "等待网络恢复")
		}
		r.emit(StateError, "check", category, "网络探测失败，需要检查配置或协议", nil)
		return 0, true
	}

	r.setLastResult(result.Status)
	switch result.Status {
	case connectivity.StatusAuthenticated:
		r.markAuthenticated("check")
		return r.config.CheckInterval, false
	case connectivity.StatusOffline:
		r.emit(StateOffline, "check", string(connectivity.CategoryNetwork), "网络暂时不可达", nil)
		return r.scheduleBackoff(string(connectivity.CategoryNetwork), "等待网络恢复")
	case connectivity.StatusPortal:
		return r.authenticate(ctx, result)
	default:
		r.emit(StateError, "check", "protocol", "网络探测返回未知状态", nil)
		return 0, true
	}
}

func (r *Runner) authenticate(ctx context.Context, result connectivity.Result) (time.Duration, bool) {
	r.emit(StatePortal, "discover", "", "检测到认证 Portal", nil)
	if result.RedirectURL == nil {
		r.emit(StateError, "discover", string(portal.CategoryPageParse), "Portal 未提供登录页面地址", nil)
		return 0, true
	}

	credentialContext, cancel := context.WithTimeout(ctx, r.config.RequestTimeout)
	password, err := r.deps.Credentials.Resolve(credentialContext, r.config.Account.CredentialRef)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return 0, true
		}
		category := errorCategory(err, string(credential.CategoryUnavailable))
		r.emit(StateError, "credential", category, "无法读取账号凭据", nil)
		return 0, true
	}

	r.emit(StateAuthenticating, "authenticate", "", "正在提交认证请求", nil)
	if err := r.authenticateRequest(ctx, result.RedirectURL, r.config.Account.Username, password); err != nil {
		if ctx.Err() != nil {
			return 0, true
		}
		category := errorCategory(err, "internal")
		if category == string(portal.CategoryAuthentication) {
			return r.scheduleBackoff(category, "认证未被接受")
		}
		if category == string(portal.CategoryNetwork) {
			r.emit(StateOffline, "authenticate", category, "认证网络暂时不可达", nil)
			return r.scheduleBackoff(category, "等待认证网络恢复")
		}
		r.emit(StateError, "authenticate", category, "认证请求失败，需要检查配置或协议", nil)
		return 0, true
	}

	r.emit(StateChecking, "verify", "", "正在复检网络状态", nil)
	verified, err := r.check(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return 0, true
		}
		category := errorCategory(err, string(connectivity.CategoryNetwork))
		if category == string(connectivity.CategoryNetwork) || verified.Status == connectivity.StatusOffline {
			r.emit(StateOffline, "verify", category, "认证后网络仍不可达", nil)
			return r.scheduleBackoff(category, "等待网络恢复")
		}
		r.emit(StateError, "verify", category, "认证后网络复检失败", nil)
		return 0, true
	}
	r.setLastResult(verified.Status)
	if verified.Status != connectivity.StatusAuthenticated {
		return r.scheduleBackoff(string(portal.CategoryAuthentication), "认证后复检仍未通过")
	}

	r.markAuthenticated("verify")
	return r.config.CheckInterval, false
}

func (r *Runner) scheduleBackoff(category, message string) (time.Duration, bool) {
	now := r.deps.Now()
	r.mu.Lock()
	retryCount := r.snapshot.RetryCount + 1
	r.snapshot.RetryCount = retryCount
	delay := backoffDelay(r.config.RetryInitial, r.config.RetryMax, retryCount, r.deps.Jitter)
	next := now.Add(delay)
	r.snapshot.State = StateBackoff
	r.snapshot.LastError = message
	r.snapshot.ErrorCategory = category
	r.snapshot.NextAttemptAt = &next
	event := Event{
		At:            now,
		AccountID:     r.config.Account.ID,
		State:         StateBackoff,
		Operation:     "retry",
		Category:      category,
		Message:       message,
		RetryCount:    retryCount,
		NextAttemptAt: &next,
	}
	r.mu.Unlock()
	r.publish(event)
	return delay, false
}

func (r *Runner) markAuthenticated(operation string) {
	now := r.deps.Now()
	next := now.Add(r.config.CheckInterval)
	r.mu.Lock()
	r.snapshot.State = StateAuthenticated
	r.snapshot.RetryCount = 0
	r.snapshot.LastError = ""
	r.snapshot.ErrorCategory = ""
	r.snapshot.LastSuccessAt = &now
	r.snapshot.NextAttemptAt = &next
	event := Event{
		At:            now,
		AccountID:     r.config.Account.ID,
		State:         StateAuthenticated,
		Operation:     operation,
		Message:       "网络已确认可用",
		NextAttemptAt: &next,
	}
	r.mu.Unlock()
	r.publish(event)
}

func (r *Runner) setLastResult(status connectivity.Status) {
	r.mu.Lock()
	r.snapshot.LastResult = status
	r.mu.Unlock()
}

func (r *Runner) emit(state State, operation, category, message string, next *time.Time) {
	now := r.deps.Now()
	r.mu.Lock()
	r.snapshot.State = state
	r.snapshot.NextAttemptAt = cloneTime(next)
	if category == "" {
		r.snapshot.LastError = ""
		r.snapshot.ErrorCategory = ""
	} else {
		r.snapshot.LastError = message
		r.snapshot.ErrorCategory = category
	}
	event := Event{
		At:            now,
		AccountID:     r.config.Account.ID,
		State:         state,
		Operation:     operation,
		Category:      category,
		Message:       message,
		RetryCount:    r.snapshot.RetryCount,
		NextAttemptAt: cloneTime(next),
	}
	r.mu.Unlock()
	r.publish(event)
}

func (r *Runner) publish(event Event) {
	if r.deps.Publish != nil {
		r.deps.Publish(event)
	}
}

func (r *Runner) isEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config.Account.Enabled
}

func (r *Runner) wait(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-r.trigger:
		return true
	case <-timer.C:
		return true
	}
}

func (r *Runner) waitForTrigger(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-r.trigger:
		return true
	}
}

func errorCategory(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	var categorized interface{ ErrorCategory() string }
	if errors.As(err, &categorized) && categorized.ErrorCategory() != "" {
		return categorized.ErrorCategory()
	}
	return fallback
}

func backoffDelay(initial, maximum time.Duration, retryCount int, jitter func(time.Duration) time.Duration) time.Duration {
	delay := initial
	for index := 1; index < retryCount && delay < maximum; index++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	jitterLimit := delay / 4
	if jitterLimit > 0 && jitter != nil {
		extra := jitter(jitterLimit)
		if extra > maximum-delay {
			extra = maximum - delay
		}
		delay += extra
	}
	return delay
}

func secureJitter(maximum time.Duration) time.Duration {
	if maximum <= 0 {
		return 0
	}
	var data [8]byte
	if _, err := cryptorand.Read(data[:]); err != nil {
		return 0
	}
	return time.Duration(binary.LittleEndian.Uint64(data[:]) % uint64(maximum+1))
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.LastSuccessAt = cloneTime(snapshot.LastSuccessAt)
	snapshot.NextAttemptAt = cloneTime(snapshot.NextAttemptAt)
	return snapshot
}
