package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
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

// ConfigureAccount 保存账号配置，成功后替换运行器并立即开始新的账号循环。
func (service *Service) ConfigureAccount(ctx context.Context, request AccountConfigureRequest) error {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("账号配置上下文不能为空")
	}
	service.mu.RLock()
	path := service.configPath
	current := service.config
	started := service.started
	rootContext := service.rootCtx
	service.mu.RUnlock()
	candidate := current
	candidate.Accounts = append([]config.AccountConfig(nil), current.Accounts...)
	requestedID := strings.TrimSpace(request.ID)
	accountIndex := -1
	for index, existing := range candidate.Accounts {
		if existing.ID == requestedID && requestedID != "" {
			accountIndex = index
			break
		}
	}
	if requestedID != "" && accountIndex < 0 {
		return fmt.Errorf("账号 %q 不存在", requestedID)
	}

	var accountConfig config.AccountConfig
	if accountIndex >= 0 {
		accountConfig = candidate.Accounts[accountIndex]
		accountConfig.Username = request.Username
		accountConfig.Enabled = request.Enabled
	} else {
		generatedID, number := nextAccountID(candidate.Accounts)
		accountConfig = config.AccountConfig{
			ID:          generatedID,
			DisplayName: fmt.Sprintf("账号 %d", number),
			Username:    request.Username,
			Enabled:     request.Enabled,
			Priority:    100,
		}
	}
	if accountConfig.NetworkInterface == "" {
		accountConfig.NetworkInterface = automaticNetworkInterface(ctx, candidate.Accounts, accountConfig.ID)
	}
	passwordProvided := request.Password != ""
	if passwordProvided {
		credentialRef, err := config.CredentialReferenceForAccount(path, accountConfig.ID)
		if err != nil {
			return err
		}
		accountConfig.CredentialRef = credentialRef
	}
	if accountIndex >= 0 {
		candidate.Accounts[accountIndex] = accountConfig
	} else {
		candidate.Accounts = append(candidate.Accounts, accountConfig)
	}
	if err := candidate.Validate(); err != nil {
		return err
	}

	newLimiter := newRequestLimiter(candidate.Runtime.MaxConcurrentRequests)
	newRunners, newOrder, err := service.buildRunners(candidate, newLimiter)
	if err != nil {
		return err
	}
	if err := config.SaveAccountConfiguration(ctx, path, candidate, accountConfig.ID, request.Password, passwordProvided); err != nil {
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

	if err := service.events.Resize(candidate.Runtime.EventBufferSize); err != nil {
		return fmt.Errorf("调整事件缓冲区失败: %w", err)
	}
	service.mu.Lock()
	service.config = candidate
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

func nextAccountID(accounts []config.AccountConfig) (string, int) {
	used := make(map[string]struct{}, len(accounts))
	for _, accountConfig := range accounts {
		used[accountConfig.ID] = struct{}{}
	}
	for number := 1; ; number++ {
		id := fmt.Sprintf("account_%d", number)
		if _, exists := used[id]; !exists {
			return id, number
		}
	}
}

func automaticNetworkInterface(ctx context.Context, accounts []config.AccountConfig, currentID string) string {
	devices, err := config.DiscoverWANDevices(ctx)
	if err != nil {
		return ""
	}
	used := make(map[string]struct{}, len(accounts))
	for _, accountConfig := range accounts {
		if accountConfig.ID != currentID && accountConfig.NetworkInterface != "" {
			used[accountConfig.NetworkInterface] = struct{}{}
		}
	}
	for _, device := range devices {
		if _, exists := used[device]; !exists {
			return device
		}
	}
	return ""
}

// DeleteAccount 删除账号配置和运行器，成功后立即生效。
func (service *Service) DeleteAccount(ctx context.Context, id string) error {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("账号删除上下文不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("账号 ID 不能为空")
	}

	service.mu.RLock()
	path := service.configPath
	current := service.config
	started := service.started
	rootContext := service.rootCtx
	service.mu.RUnlock()
	accountIndex := -1
	for index, accountConfig := range current.Accounts {
		if accountConfig.ID == id {
			accountIndex = index
			break
		}
	}
	if accountIndex < 0 {
		return fmt.Errorf("账号 %q 不存在", id)
	}
	removed := current.Accounts[accountIndex]
	candidate := current
	candidate.Accounts = make([]config.AccountConfig, 0, len(current.Accounts)-1)
	candidate.Accounts = append(candidate.Accounts, current.Accounts[:accountIndex]...)
	candidate.Accounts = append(candidate.Accounts, current.Accounts[accountIndex+1:]...)
	if err := candidate.Validate(); err != nil {
		return err
	}

	newLimiter := newRequestLimiter(candidate.Runtime.MaxConcurrentRequests)
	newRunners, newOrder, err := service.buildRunners(candidate, newLimiter)
	if err != nil {
		return err
	}
	if err := config.DeleteAccountConfiguration(ctx, path, candidate, removed); err != nil {
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

	service.mu.Lock()
	service.config = candidate
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
