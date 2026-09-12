package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/config"
	"github.com/onprs/GiWifi-Auto/internal/connectivity"
	"github.com/onprs/GiWifi-Auto/internal/credential"
	"github.com/onprs/GiWifi-Auto/internal/eventlog"
	"github.com/onprs/GiWifi-Auto/internal/portal"
)

// Dependencies 是守护进程可替换的外部依赖。
type Dependencies struct {
	Resolver         credential.Resolver
	TransportFactory func() http.RoundTripper
	Now              func() time.Time
	Jitter           func(time.Duration) time.Duration
}

// Service 是配置、账号运行器和事件流的唯一所有者。
type Service struct {
	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	config      config.Config
	configPath  string
	deps        Dependencies
	events      *eventlog.Store
	limiter     *requestLimiter
	runners     map[string]*account.Runner
	order       []string
	started     bool
	rootCtx     context.Context
	runCancel   context.CancelFunc
	wg          sync.WaitGroup
}

// New 创建一个尚未启动的守护进程服务。
func New(cfg config.Config, configPath string, deps Dependencies) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("创建守护进程服务前配置无效: %w", err)
	}
	store, err := eventlog.New(cfg.Runtime.EventBufferSize)
	if err != nil {
		return nil, fmt.Errorf("创建事件存储失败: %w", err)
	}
	if deps.Resolver == nil {
		deps.Resolver = credential.DefaultResolver{}
	}
	if deps.TransportFactory == nil {
		deps.TransportFactory = defaultTransport
	}

	service := &Service{
		config:     cfg,
		configPath: configPath,
		deps:       deps,
		events:     store,
		limiter:    newRequestLimiter(cfg.Runtime.MaxConcurrentRequests),
		runners:    make(map[string]*account.Runner, len(cfg.Accounts)),
		order:      make([]string, 0, len(cfg.Accounts)),
	}
	if err := service.rebuildRunners(cfg); err != nil {
		return nil, err
	}
	return service, nil
}

// Start 启动所有账号运行器并立即返回。
func (service *Service) Start(ctx context.Context) error {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("守护进程上下文不能为空")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.started {
		return errors.New("守护进程已经启动")
	}
	service.rootCtx = ctx
	service.runContextLocked()
	return nil
}

// Stop 取消所有账号运行器并等待其退出。
func (service *Service) Stop() {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	service.mu.Lock()
	cancel := service.runCancel
	service.runCancel = nil
	service.started = false
	service.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	service.wg.Wait()
}

// Reload 重新读取并校验配置，成功后替换全部账号运行器。
func (service *Service) Reload(ctx context.Context) error {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("重载配置上下文不能为空")
	}
	service.mu.RLock()
	path := service.configPath
	started := service.started
	rootContext := service.rootCtx
	controlSocket := service.config.Runtime.ControlSocket
	service.mu.RUnlock()
	if path == "" {
		return errors.New("重载配置需要配置文件路径")
	}
	cfg, err := config.Load(ctx, path)
	if err != nil {
		return err
	}
	if started && cfg.Runtime.ControlSocket != controlSocket {
		return errors.New("守护进程运行期间不能重载控制 Socket 地址，请重启服务")
	}
	newLimiter := newRequestLimiter(cfg.Runtime.MaxConcurrentRequests)
	newRunners, newOrder, err := service.buildRunners(cfg, newLimiter)
	if err != nil {
		return err
	}

	service.mu.Lock()
	oldCancel := service.runCancel
	service.runCancel = nil
	service.started = false
	service.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	service.wg.Wait()

	if err := service.events.Resize(cfg.Runtime.EventBufferSize); err != nil {
		return fmt.Errorf("调整事件缓冲区失败: %w", err)
	}
	service.mu.Lock()
	service.config = cfg
	service.limiter = newLimiter
	service.runners = newRunners
	service.order = newOrder
	if started && rootContext != nil && rootContext.Err() == nil {
		service.rootCtx = rootContext
		service.runContextLocked()
	}
	service.mu.Unlock()
	return nil
}

// Status 返回按配置顺序排列的账号状态。
func (service *Service) Status() []account.Snapshot {
	service.mu.RLock()
	defer service.mu.RUnlock()
	result := make([]account.Snapshot, 0, len(service.order))
	for _, id := range service.order {
		if runner, exists := service.runners[id]; exists {
			result = append(result, runner.Snapshot())
		}
	}
	return result
}

// Trigger 立即唤醒指定账号。
func (service *Service) Trigger(id string) error {
	service.mu.RLock()
	runner, exists := service.runners[id]
	service.mu.RUnlock()
	if !exists {
		return fmt.Errorf("账号 %q 不存在", id)
	}
	runner.Trigger()
	return nil
}

// SetEnabled 更新账号启用状态，并在配置路径存在时原子保存。
func (service *Service) SetEnabled(ctx context.Context, id string, enabled bool) error {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("更新账号状态上下文不能为空")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	runner, exists := service.runners[id]
	if !exists {
		return fmt.Errorf("账号 %q 不存在", id)
	}
	candidate := service.config
	candidate.Accounts = append([]config.AccountConfig(nil), service.config.Accounts...)
	found := false
	for index := range candidate.Accounts {
		if candidate.Accounts[index].ID == id {
			candidate.Accounts[index].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("账号 %q 不存在", id)
	}
	if err := candidate.Validate(); err != nil {
		return fmt.Errorf("更新账号状态后配置无效: %w", err)
	}
	if service.configPath != "" {
		if err := config.SaveAccountEnabled(ctx, service.configPath, candidate, id, enabled); err != nil {
			return err
		}
	}
	service.config = candidate
	runner.SetEnabled(enabled)
	return nil
}

// RecentEvents 返回近期结构化事件。
func (service *Service) RecentEvents(limit int) []eventlog.Event {
	return service.events.Recent(limit)
}

// WaitEvents 等待序号之后的新事件，不阻塞事件生产者。
func (service *Service) WaitEvents(ctx context.Context, after uint64, limit int) ([]eventlog.Event, error) {
	return service.events.Wait(ctx, after, limit)
}

// Subscribe 订阅后续事件。
func (service *Service) Subscribe(buffer int) (*eventlog.Subscription, error) {
	return service.events.Subscribe(buffer)
}

func (service *Service) rebuildRunners(cfg config.Config) error {
	service.mu.RLock()
	limiter := service.limiter
	service.mu.RUnlock()
	runners, order, err := service.buildRunners(cfg, limiter)
	if err != nil {
		return err
	}
	service.mu.Lock()
	service.runners = runners
	service.order = order
	service.mu.Unlock()
	return nil
}

func (service *Service) buildRunners(cfg config.Config, limiter *requestLimiter) (map[string]*account.Runner, []string, error) {
	refererURL, err := url.Parse(cfg.Runtime.ConnectivityURL)
	if err != nil {
		return nil, nil, fmt.Errorf("解析连通性地址失败: %w", err)
	}
	runners := make(map[string]*account.Runner, len(cfg.Accounts))
	order := make([]string, 0, len(cfg.Accounts))
	for _, accountConfig := range cfg.Accounts {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, nil, fmt.Errorf("为账号 %q 创建 Cookie Jar 失败: %w", accountConfig.ID, err)
		}
		transport := service.deps.TransportFactory()
		if transport == nil {
			return nil, nil, fmt.Errorf("为账号 %q 创建 HTTP Transport 失败", accountConfig.ID)
		}
		transport, err = bindAccountTransport(transport, accountConfig.NetworkInterface)
		if err != nil {
			return nil, nil, fmt.Errorf("为账号 %q 绑定网络接口失败: %w", accountConfig.ID, err)
		}

		probe := connectivity.New()
		probe.Transport = transport
		probe.CookieJar = jar
		probe.Timeout = time.Duration(cfg.Runtime.RequestTimeoutSeconds) * time.Second

		var loginEndpoint *url.URL
		if cfg.Runtime.PortalLoginURL != "" {
			loginEndpoint, err = url.Parse(cfg.Runtime.PortalLoginURL)
			if err != nil {
				return nil, nil, fmt.Errorf("解析 Portal 登录端点失败: %w", err)
			}
		}
		portalClient := portal.NewClient(loginEndpoint)
		portalClient.Transport = transport
		portalClient.CookieJar = jar
		portalClient.Timeout = time.Duration(cfg.Runtime.RequestTimeoutSeconds) * time.Second
		portalClient.PageReferer = refererURL

		runner, err := account.New(account.RunnerConfig{
			Account:         accountConfig,
			ConnectivityURL: cfg.Runtime.ConnectivityURL,
			CheckInterval:   time.Duration(cfg.Runtime.CheckIntervalSeconds) * time.Second,
			RequestTimeout:  time.Duration(cfg.Runtime.RequestTimeoutSeconds) * time.Second,
			RetryInitial:    time.Duration(cfg.Runtime.RetryInitialSeconds) * time.Second,
			RetryMax:        time.Duration(cfg.Runtime.RetryMaxSeconds) * time.Second,
		}, account.Dependencies{
			Probe:         probe,
			Authenticator: portalClient,
			Credentials:   service.deps.Resolver,
			Limiter:       limiter,
			Now:           service.deps.Now,
			Jitter:        service.deps.Jitter,
			Publish:       service.publish,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("创建账号 %q 运行器失败: %w", accountConfig.ID, err)
		}
		runners[accountConfig.ID] = runner
		order = append(order, accountConfig.ID)
	}
	return runners, order, nil
}

func (service *Service) runContextLocked() {
	service.started = true
	runContext, cancel := context.WithCancel(service.rootCtx)
	service.runCancel = cancel
	for _, id := range service.order {
		runner := service.runners[id]
		service.wg.Add(1)
		go func() {
			defer service.wg.Done()
			runner.Run(runContext)
		}()
	}
}

func (service *Service) publish(event account.Event) {
	service.events.Append(eventlog.Event{
		At:            event.At,
		AccountID:     event.AccountID,
		State:         string(event.State),
		Operation:     event.Operation,
		Category:      event.Category,
		Message:       event.Message,
		RetryCount:    event.RetryCount,
		NextAttemptAt: event.NextAttemptAt,
	})
}

func defaultTransport() http.RoundTripper {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return transport.Clone()
	}
	return http.DefaultTransport
}
