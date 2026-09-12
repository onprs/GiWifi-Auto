package account

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/config"
	"github.com/onprs/GiWifi-Auto/internal/connectivity"
	"github.com/onprs/GiWifi-Auto/internal/credential"
	"github.com/onprs/GiWifi-Auto/internal/portal"
)

func TestRunnerCompletesPortalAuthenticationAndVerification(t *testing.T) {
	portalURL, err := url.Parse("http://portal.example.test/login?synthetic=1")
	if err != nil {
		t.Fatalf("解析 Portal 地址失败: %v", err)
	}
	probe := &sequenceProbe{results: []probeResult{
		{result: connectivity.Result{Status: connectivity.StatusPortal, RedirectURL: portalURL}},
		{result: connectivity.Result{Status: connectivity.StatusAuthenticated}},
	}}
	resolver := &testResolver{value: "synthetic-password"}
	authenticator := &recordingAuthenticator{}
	runner, err := New(testRunnerConfig(true), Dependencies{
		Probe:         probe,
		Authenticator: authenticator,
		Credentials:   resolver,
		Jitter:        func(time.Duration) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	waitForState(t, runner, StateAuthenticated)
	cancel()
	waitForDone(t, done)

	if probe.calls() != 2 {
		t.Fatalf("探测次数 = %d, want 2", probe.calls())
	}
	if resolver.getReference() != "uci:sample.account.password" {
		t.Fatalf("凭据引用 = %q", resolver.getReference())
	}
	username, password := authenticator.credentials()
	if username != "user@example.test" || password != "synthetic-password" {
		t.Fatalf("认证参数异常: username=%q password=%q", username, password)
	}
	snapshot := runner.Snapshot()
	if snapshot.RetryCount != 0 || snapshot.LastResult != connectivity.StatusAuthenticated || snapshot.LastSuccessAt == nil {
		t.Fatalf("成功状态摘要 = %+v", snapshot)
	}
}

func TestRunnerBacksOffAfterRejectedAuthentication(t *testing.T) {
	portalURL, _ := url.Parse("http://portal.example.test/login")
	probe := &sequenceProbe{results: []probeResult{{result: connectivity.Result{Status: connectivity.StatusPortal, RedirectURL: portalURL}}}}
	authenticator := &recordingAuthenticator{err: categorizedError{category: string(portal.CategoryAuthentication)}}
	runner, err := New(testRunnerConfig(true), Dependencies{
		Probe:         probe,
		Authenticator: authenticator,
		Credentials:   &testResolver{value: "synthetic-password"},
		Jitter:        func(time.Duration) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	waitForState(t, runner, StateBackoff)
	cancel()
	waitForDone(t, done)

	snapshot := runner.Snapshot()
	if snapshot.RetryCount != 1 || snapshot.ErrorCategory != string(portal.CategoryAuthentication) {
		t.Fatalf("退避状态摘要 = %+v", snapshot)
	}
	if snapshot.LastError != "认证未被接受" {
		t.Fatalf("认证错误摘要 = %q", snapshot.LastError)
	}
}

func TestRunnerBacksOffAfterNetworkAuthenticationFailure(t *testing.T) {
	portalURL, _ := url.Parse("http://portal.example.test/login")
	probe := &sequenceProbe{results: []probeResult{{result: connectivity.Result{Status: connectivity.StatusPortal, RedirectURL: portalURL}}}}
	runner, err := New(testRunnerConfig(true), Dependencies{
		Probe:         probe,
		Authenticator: &recordingAuthenticator{err: categorizedError{category: string(portal.CategoryNetwork)}},
		Credentials:   &testResolver{value: "synthetic-password"},
		Jitter:        func(time.Duration) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	waitForState(t, runner, StateBackoff)
	cancel()
	waitForDone(t, done)

	snapshot := runner.Snapshot()
	if snapshot.RetryCount != 1 || snapshot.ErrorCategory != string(portal.CategoryNetwork) {
		t.Fatalf("认证网络错误退避状态摘要 = %+v", snapshot)
	}
	if snapshot.LastError != "等待认证网络恢复" {
		t.Fatalf("认证网络错误摘要 = %q", snapshot.LastError)
	}
}
func TestRunnerWaitsForTriggerAfterNonRetryableError(t *testing.T) {
	probe := &sequenceProbe{results: []probeResult{{err: categorizedError{category: string(connectivity.CategoryProtocol)}}}}
	runner, err := New(testRunnerConfig(true), Dependencies{
		Probe:         probe,
		Authenticator: &recordingAuthenticator{},
		Credentials:   &testResolver{value: "synthetic-password"},
		Jitter:        func(time.Duration) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	waitForState(t, runner, StateError)
	if probe.calls() != 1 {
		t.Fatalf("不可恢复错误被重复探测: %d", probe.calls())
	}
	runner.Trigger()
	cancel()
	waitForDone(t, done)
}

func TestRunnerCancellationStopsBlockedRequest(t *testing.T) {
	probe := &blockingProbe{started: make(chan struct{})}
	runner, err := New(testRunnerConfig(true), Dependencies{
		Probe:         probe,
		Authenticator: &recordingAuthenticator{},
		Credentials:   &testResolver{value: "synthetic-password"},
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	select {
	case <-probe.started:
	case <-time.After(time.Second):
		t.Fatal("探测器未启动")
	}
	cancel()
	waitForDone(t, done)
}

func TestRunnerSetEnabledCancelsActiveRequest(t *testing.T) {
	probe := &blockingProbe{started: make(chan struct{}), finished: make(chan struct{})}
	runner, err := New(testRunnerConfig(true), Dependencies{
		Probe:         probe,
		Authenticator: &recordingAuthenticator{},
		Credentials:   &testResolver{value: "synthetic-password"},
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	select {
	case <-probe.started:
	case <-time.After(time.Second):
		t.Fatal("探测器未启动")
	}

	runner.SetEnabled(false)
	select {
	case <-probe.finished:
	case <-time.After(time.Second):
		t.Fatal("停用账号后活动请求未取消")
	}
	cancel()
	waitForDone(t, done)
}

func TestRunnerSetEnabledWakesDisabledRunner(t *testing.T) {
	probe := &sequenceProbe{results: []probeResult{{result: connectivity.Result{Status: connectivity.StatusAuthenticated}}}}
	runner, err := New(testRunnerConfig(false), Dependencies{
		Probe:         probe,
		Authenticator: &recordingAuthenticator{},
		Credentials:   &testResolver{value: "synthetic-password"},
	})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	waitForState(t, runner, StateDisabled)

	runner.SetEnabled(true)
	waitForState(t, runner, StateAuthenticated)
	runner.SetEnabled(false)
	waitForState(t, runner, StateDisabled)
	cancel()
	waitForDone(t, done)
}

func TestBackoffDelayIsCappedAndJittered(t *testing.T) {
	jitter := func(maximum time.Duration) time.Duration { return maximum }
	initial := time.Second
	maximum := 5 * time.Second
	cases := []struct {
		retry int
		want  time.Duration
	}{
		{retry: 1, want: 1250 * time.Millisecond},
		{retry: 2, want: 2500 * time.Millisecond},
		{retry: 3, want: 5 * time.Second},
		{retry: 4, want: 5 * time.Second},
	}
	for _, testCase := range cases {
		if got := backoffDelay(initial, maximum, testCase.retry, jitter); got != testCase.want {
			t.Errorf("retry=%d delay=%s, want %s", testCase.retry, got, testCase.want)
		}
	}
}

func testRunnerConfig(enabled bool) RunnerConfig {
	return RunnerConfig{
		Account: config.AccountConfig{
			ID:            "sample",
			DisplayName:   "示例账号",
			Username:      "user@example.test",
			CredentialRef: "uci:sample.account.password",
			Enabled:       enabled,
		},
		ConnectivityURL: "http://connectivity.example.test",
		CheckInterval:   time.Hour,
		RetryInitial:    time.Second,
		RetryMax:        5 * time.Second,
	}
}

func waitForState(t *testing.T, runner *Runner, state State) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if runner.Snapshot().State == state {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("等待状态 %q 超时，当前为 %q", state, runner.Snapshot().State)
		case <-ticker.C:
		}
	}
}

func waitForDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("运行器未在取消后退出")
	}
}

type probeResult struct {
	result connectivity.Result
	err    error
}

type sequenceProbe struct {
	mu      sync.Mutex
	results []probeResult
	count   int
}

func (probe *sequenceProbe) Check(context.Context, string) (connectivity.Result, error) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	index := probe.count
	probe.count++
	if index >= len(probe.results) {
		return connectivity.Result{Status: connectivity.StatusAuthenticated}, nil
	}
	return probe.results[index].result, probe.results[index].err
}

func (probe *sequenceProbe) calls() int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.count
}

type blockingProbe struct {
	started  chan struct{}
	finished chan struct{}
}

func (probe *blockingProbe) Check(ctx context.Context, _ string) (connectivity.Result, error) {
	close(probe.started)
	<-ctx.Done()
	if probe.finished != nil {
		close(probe.finished)
	}
	return connectivity.Result{Status: connectivity.StatusOffline}, ctx.Err()
}

type testResolver struct {
	mu        sync.Mutex
	value     string
	reference string
}

func (resolver *testResolver) Resolve(_ context.Context, reference string) (string, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.reference = reference
	return resolver.value, nil
}

func (resolver *testResolver) getReference() string {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return resolver.reference
}

type recordingAuthenticator struct {
	mu       sync.Mutex
	username string
	password string
	err      error
}

func (authenticator *recordingAuthenticator) Authenticate(_ context.Context, _ *url.URL, username, password string) error {
	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.username = username
	authenticator.password = password
	return authenticator.err
}

func (authenticator *recordingAuthenticator) credentials() (string, string) {
	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	return authenticator.username, authenticator.password
}

type categorizedError struct {
	category string
}

func (err categorizedError) Error() string {
	return "synthetic categorized error"
}

func (err categorizedError) ErrorCategory() string {
	return err.category
}

var _ credential.Resolver = (*testResolver)(nil)
