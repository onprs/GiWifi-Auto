package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/config"
)

func TestServiceCompletesAuthenticationAndVerification(t *testing.T) {
	const password = "synthetic-service-password"
	t.Setenv("GIWIFI_SERVICE_PASSWORD", password)
	var loginRequests atomic.Int32
	var verifiedRequests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/check":
			if _, err := request.Cookie("session"); err == nil {
				verifiedRequests.Add(1)
				_, _ = writer.Write([]byte("Success"))
				return
			}
			writer.Header().Set("Location", "/portal?sign=synthetic-sign")
			writer.WriteHeader(http.StatusFound)
		case "/portal":
			writer.Header().Set("Set-Cookie", "session=synthetic-session; Path=/")
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write([]byte(serviceLoginPage()))
		case "/gportal/Web/loginAction":
			loginRequests.Add(1)
			if _, err := request.Cookie("session"); err != nil {
				http.Error(writer, "missing session", http.StatusUnauthorized)
				return
			}
			if err := request.ParseForm(); err != nil || request.Form.Get("data") == "" || request.Form.Get("iv") != "1234567890abcdef" {
				http.Error(writer, "invalid payload", http.StatusBadRequest)
				return
			}
			if strings.Contains(request.Form.Get("data"), password) {
				http.Error(writer, "plaintext credential", http.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":1}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Runtime.ConnectivityURL = server.URL + "/check"
	cfg.Runtime.PortalLoginURL = server.URL + "/gportal/Web/loginAction"
	cfg.Runtime.RequestTimeoutSeconds = 2
	cfg.Runtime.CheckIntervalSeconds = 3600
	cfg.Accounts = []config.AccountConfig{{
		ID:            "service-account",
		DisplayName:   "服务测试账号",
		Username:      "user@example.test",
		CredentialRef: "env:GIWIFI_SERVICE_PASSWORD",
		Enabled:       true,
	}}

	service, err := New(cfg, "", Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("Start() 失败: %v", err)
	}
	waitForServiceState(t, service, account.StateAuthenticated)
	service.Stop()

	if loginRequests.Load() != 1 || verifiedRequests.Load() == 0 {
		t.Fatalf("请求次数 login=%d verify=%d", loginRequests.Load(), verifiedRequests.Load())
	}
	statusJSON, err := json.Marshal(service.Status())
	if err != nil {
		t.Fatalf("状态序列化失败: %v", err)
	}
	if strings.Contains(string(statusJSON), password) || strings.Contains(string(statusJSON), "user@example.test") {
		t.Fatalf("状态 JSON 泄露敏感信息: %s", statusJSON)
	}
	for _, event := range service.RecentEvents(0) {
		if strings.Contains(event.Message, password) {
			t.Fatalf("事件泄露密码: %+v", event)
		}
	}
}

func TestServiceLimitsConcurrentNetworkRequests(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		active.Add(-1)
		_, _ = writer.Write([]byte("Success"))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Runtime.ConnectivityURL = server.URL
	cfg.Runtime.PortalLoginURL = server.URL + "/login"
	cfg.Runtime.MaxConcurrentRequests = 1
	cfg.Runtime.CheckIntervalSeconds = 3600
	cfg.Accounts = []config.AccountConfig{
		{ID: "limited-one", DisplayName: "并发账号一", Username: "one@example.test", CredentialRef: "env:GIWIFI_ONE", Enabled: true},
		{ID: "limited-two", DisplayName: "并发账号二", Username: "two@example.test", CredentialRef: "env:GIWIFI_TWO", Enabled: true},
	}
	service, err := New(cfg, "", Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("Start() 失败: %v", err)
	}
	waitForServiceStates(t, service, 2, account.StateAuthenticated)
	service.Stop()
	if got := maximum.Load(); got > 1 {
		t.Fatalf("最大网络请求并发数 = %d, want <= 1", got)
	}
}

func TestServiceConfigureAccountPersistsAccountAndPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	if err := config.SaveFileAtomic(path, cfg); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	service, err := New(cfg, path, Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	request := AccountConfigureRequest{
		Username: "user@example.test",
		Password: "tui-password",
		Enabled:  true,
	}
	if err := service.ConfigureAccount(context.Background(), request); err != nil {
		t.Fatalf("ConfigureAccount() 失败: %v", err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("读取保存配置失败: %v", err)
	}
	if len(loaded.Accounts) != 1 || loaded.Accounts[0].ID != "account_1" || loaded.Accounts[0].DisplayName != "账号 1" || !loaded.Accounts[0].Enabled {
		t.Fatalf("保存的账号 = %+v", loaded.Accounts)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置文件失败: %v", err)
	}
	if strings.Contains(string(data), request.Password) {
		t.Fatal("密码被写入 JSON 配置")
	}
	credentialRef := loaded.Accounts[0].CredentialRef
	if !strings.HasPrefix(credentialRef, "file:") {
		t.Fatalf("凭据引用 = %q, want file 引用", credentialRef)
	}
	credentialData, err := os.ReadFile(strings.TrimPrefix(credentialRef, "file:"))
	if err != nil {
		t.Fatalf("读取凭据文件失败: %v", err)
	}
	if strings.TrimSpace(string(credentialData)) != request.Password {
		t.Fatalf("凭据文件内容不正确: %q", credentialData)
	}
	statusJSON, err := json.Marshal(service.configuration())
	if err != nil {
		t.Fatalf("序列化配置视图失败: %v", err)
	}
	if strings.Contains(string(statusJSON), request.Password) {
		t.Fatal("配置视图泄露密码")
	}
}

func TestServiceDeleteAccountRemovesConfigurationAndRunner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	credentialRef, err := config.CredentialReferenceForAccount(path, "remove-account")
	if err != nil {
		t.Fatalf("生成凭据引用失败: %v", err)
	}
	cfg := config.Default()
	cfg.Accounts = []config.AccountConfig{{
		ID:            "remove-account",
		DisplayName:   "待删除账号",
		Username:      "user@example.test",
		CredentialRef: credentialRef,
	}}
	if err := config.SaveAccountConfiguration(context.Background(), path, cfg, "remove-account", "delete-password", true); err != nil {
		t.Fatalf("准备账号失败: %v", err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("读取准备配置失败: %v", err)
	}
	service, err := New(loaded, path, Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	if err := service.DeleteAccount(context.Background(), "remove-account"); err != nil {
		t.Fatalf("DeleteAccount() 失败: %v", err)
	}
	if len(service.Status()) != 0 {
		t.Fatalf("删除后的运行器状态 = %+v", service.Status())
	}
	deleted, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("读取删除后的配置失败: %v", err)
	}
	if len(deleted.Accounts) != 0 {
		t.Fatalf("删除后的配置 = %+v", deleted.Accounts)
	}
	if _, err := os.Stat(strings.TrimPrefix(credentialRef, "file:")); !os.IsNotExist(err) {
		t.Fatalf("凭据侧车删除状态 = %v", err)
	}
}

func TestServiceSetEnabledPersistsValidatedConfig(t *testing.T) {
	path := t.TempDir() + "/config.json"
	cfg := config.Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	cfg.Accounts = []config.AccountConfig{{
		ID:            "persisted-account",
		DisplayName:   "持久化测试账号",
		Username:      "user@example.test",
		CredentialRef: "env:GIWIFI_SERVICE_PASSWORD",
		Enabled:       false,
	}}
	if err := config.SaveFileAtomic(path, cfg); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}
	service, err := New(cfg, path, Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}

	if err := service.SetEnabled(context.Background(), "persisted-account", true); err != nil {
		t.Fatalf("SetEnabled(true) 失败: %v", err)
	}
	loaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("读取持久化配置失败: %v", err)
	}
	if !loaded.Accounts[0].Enabled || !service.Status()[0].Enabled {
		t.Fatalf("启用状态未持久化: loaded=%v status=%v", loaded.Accounts[0].Enabled, service.Status()[0].Enabled)
	}
	if err := service.SetEnabled(context.Background(), "persisted-account", false); err != nil {
		t.Fatalf("SetEnabled(false) 失败: %v", err)
	}
}

func TestServiceSetEnabledDoesNotMutateAfterPersistenceFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	cfg.Accounts = []config.AccountConfig{{
		ID:            "failed-persist",
		DisplayName:   "持久化失败账号",
		Username:      "user@example.test",
		CredentialRef: "env:GIWIFI_FAILED_PERSIST",
		Enabled:       false,
	}}
	path := filepath.Join(t.TempDir(), "missing", "config.json")
	service, err := New(cfg, path, Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	if err := service.SetEnabled(context.Background(), "failed-persist", true); err == nil {
		t.Fatal("配置持久化失败时未返回错误")
	}
	if service.Status()[0].Enabled {
		t.Fatal("持久化失败后内存账号被错误启用")
	}
}

func TestServiceAllowsEnablingWithoutPortalEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.AccountConfig{{
		ID:            "invalid-enable",
		DisplayName:   "测试账号",
		Username:      "user@example.test",
		CredentialRef: "env:GIWIFI_SERVICE_PASSWORD",
	}}
	service, err := New(configWithDisabledAccount(cfg), "", Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	if err := service.SetEnabled(context.Background(), "invalid-enable", true); err != nil {
		t.Fatalf("没有填写 Portal 地址时应允许启用账号: %v", err)
	}
}

func configWithDisabledAccount(cfg config.Config) config.Config {
	cfg.Accounts[0].Enabled = false
	return cfg
}

func waitForServiceState(t *testing.T, service *Service, state account.State) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		status := service.Status()
		if len(status) == 1 && status[0].State == state {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("等待服务状态 %q 超时，当前为 %+v", state, status)
		case <-ticker.C:
		}
	}
}

func waitForServiceStates(t *testing.T, service *Service, count int, state account.State) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		status := service.Status()
		matched := 0
		for _, item := range status {
			if item.State == state {
				matched++
			}
		}
		if len(status) == count && matched == count {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("等待 %d 个账号状态 %q 超时，当前为 %+v", count, state, status)
		case <-ticker.C:
		}
	}
}

func serviceLoginPage() string {
	return `<html><body><form><input value="synthetic-sign" name="sign" type="hidden"><input name="sta_vlan" value="vlan" type="hidden"><input name="sta_port" value="port" type="hidden"><input name="sta_ip" value="192.0.2.10" type="hidden"><input name="nas_ip" value="192.0.2.1" type="hidden"><input name="nas_name" value="synthetic-nas" type="hidden"><input name="last_url" value="http://example.test/" type="hidden"><input name="request_ip" value="192.0.2.10" type="hidden"><input name="device_mode" value="mode" type="hidden"><input name="device_type" value="type" type="hidden"><input name="device_os_type" value="os" type="hidden"><input name="is_mobile" value="0" type="hidden"><input name="iv" value="1234567890abcdef" type="hidden"><input name="login_type" value="1" type="hidden"></form></body></html>`
}
