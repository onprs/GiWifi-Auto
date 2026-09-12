package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onprs/GiWifi-Auto/internal/credential"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() 校验失败: %v", err)
	}
}

func TestLoadFileAcceptsValidConfig(t *testing.T) {
	path := writeConfig(t, `{
		"version": 1,
		"runtime": {
			"connectivity_url": "https://connectivity.example.test/ping",
			"portal_login_url": "http://portal.example.test/gportal/Web/loginAction",
			"check_interval_seconds": 30,
			"request_timeout_seconds": 10,
			"retry_initial_seconds": 5,
			"retry_max_seconds": 120,
			"max_concurrent_requests": 2,
			"log_level": "info",
			"event_buffer_size": 64,
			"control_socket": "/tmp/giwifi-auto.sock"
		},
		"accounts": [{
			"id": "campus-primary",
			"display_name": "测试账号",
			"username": "user@example.test",
			"credential_ref": "uci:giwifi-auto.campus-primary.password",
			"enabled": true,
			"priority": 10,
			"network_interface": "br-lan"
		}]
	}`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() 失败: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("版本 = %d, want %d", cfg.Version, CurrentVersion)
	}
	if len(cfg.Accounts) != 1 || !cfg.Accounts[0].Enabled {
		t.Fatalf("账号配置未按预期加载: %+v", cfg.Accounts)
	}
}

func TestLoadFileRejectsUnknownField(t *testing.T) {
	path := writeConfig(t, `{
		"version": 1,
		"runtime": {
			"connectivity_url": "http://example.test",
			"check_interval_seconds": 30,
			"request_timeout_seconds": 10,
			"retry_initial_seconds": 5,
			"retry_max_seconds": 120,
			"max_concurrent_requests": 1,
			"log_level": "info",
			"event_buffer_size": 32,
			"control_socket": "/tmp/giwifi-auto.sock",
			"unexpected": true
		},
		"accounts": []
	}`)

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("LoadFile() 错误 = %v, 应拒绝未知字段", err)
	}
}

func TestLoadFileRejectsTrailingJSON(t *testing.T) {
	valid := `{
		"version": 1,
		"runtime": {
			"connectivity_url": "http://example.test",
			"check_interval_seconds": 30,
			"request_timeout_seconds": 10,
			"retry_initial_seconds": 5,
			"retry_max_seconds": 120,
			"max_concurrent_requests": 1,
			"log_level": "info",
			"event_buffer_size": 32,
			"control_socket": "/tmp/giwifi-auto.sock"
		},
		"accounts": []
	}`
	path := writeConfig(t, valid+"\n{}")

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "多段 JSON") {
		t.Fatalf("LoadFile() 错误 = %v, 应拒绝尾随 JSON", err)
	}
}

func TestLoadFileRejectsOversizedConfig(t *testing.T) {
	path := writeConfig(t, string(bytes.Repeat([]byte(" "), maxConfigFileSize+1)))

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("LoadFile() 错误 = %v, 应拒绝超大配置", err)
	}
}

func TestValidateReportsAllRelevantIssues(t *testing.T) {
	cfg := Default()
	cfg.Runtime.ConnectivityURL = "ftp://user@example.test"
	cfg.Runtime.RetryInitialSeconds = 20
	cfg.Runtime.RetryMaxSeconds = 10
	cfg.Runtime.LogLevel = "verbose"
	cfg.Runtime.ControlSocket = "relative.sock"
	cfg.Accounts = []AccountConfig{
		{ID: "Bad ID", DisplayName: "", Enabled: true, Priority: -1},
		{ID: "account", DisplayName: "账号", Enabled: true, Username: "user", CredentialRef: "ref"},
		{ID: "account", DisplayName: "另一个账号", Enabled: false},
	}

	var issues ValidationErrors
	err := cfg.Validate()
	if !errors.As(err, &issues) {
		t.Fatalf("Validate() 错误类型 = %T, want ValidationErrors", err)
	}
	message := err.Error()
	for _, want := range []string{
		"runtime.connectivity_url",
		"runtime.retry_max_seconds",
		"runtime.log_level",
		"runtime.control_socket",
		"accounts[0].id",
		"accounts[0].display_name",
		"accounts[0].username",
		"accounts[0].credential_ref",
		"accounts[1].id",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("校验错误缺少 %q: %s", want, message)
		}
	}
}

func TestValidateRejectsResourceExhaustionValues(t *testing.T) {
	cfg := Default()
	cfg.Runtime.MaxConcurrentRequests = maxConcurrentRequests + 1
	cfg.Runtime.EventBufferSize = maxEventBufferSize + 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("资源上限配置未被拒绝")
	}
}

func TestValidateRejectsEndpointUserInfoAndFragment(t *testing.T) {
	cfg := Default()
	cfg.Runtime.ConnectivityURL = "https://user@example.test/check#fragment"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() 应拒绝包含用户信息或 fragment 的 URL")
	}
	message := err.Error()
	if !strings.Contains(message, "不能包含用户信息") && !strings.Contains(message, "不能包含 fragment") {
		t.Fatalf("Validate() 错误未说明 URL 限制: %s", message)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	return path
}

func TestConfigJSONRoundTripDoesNotAddSecrets(t *testing.T) {
	cfg := Default()
	cfg.Accounts = []AccountConfig{{
		ID:            "sample",
		DisplayName:   "示例",
		Username:      "user@example.test",
		CredentialRef: "uci:giwifi-auto.sample.password",
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if strings.Contains(string(data), `"password"`) {
		t.Fatal("配置序列化不应出现 password 字段")
	}
}

func TestDeleteJSONAccountConfigurationRemovesAccountAndCredential(t *testing.T) {
	path := t.TempDir() + "/config.json"
	credentialRef, err := CredentialReferenceForAccount(path, "remove_account")
	if err != nil {
		t.Fatalf("CredentialReferenceForAccount() 失败: %v", err)
	}
	cfg := Default()
	cfg.Accounts = []AccountConfig{{
		ID:            "remove_account",
		DisplayName:   "待删除账号",
		Username:      "user@example.test",
		CredentialRef: credentialRef,
	}}
	if err := SaveAccountConfiguration(context.Background(), path, cfg, "remove_account", "delete-password", true); err != nil {
		t.Fatalf("准备账号失败: %v", err)
	}
	if err := DeleteAccountConfiguration(context.Background(), path, Default(), cfg.Accounts[0]); err != nil {
		t.Fatalf("DeleteAccountConfiguration() 失败: %v", err)
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("读取删除后的配置失败: %v", err)
	}
	if len(loaded.Accounts) != 0 {
		t.Fatalf("删除后的账号 = %+v", loaded.Accounts)
	}
	if _, err := os.Stat(strings.TrimPrefix(credentialRef, "file:")); !os.IsNotExist(err) {
		t.Fatalf("凭据文件删除状态 = %v", err)
	}
}
func TestParseUCISectionExists(t *testing.T) {
	data := []byte("giwifi-credentials.account_1=credential\ngiwifi-credentials.account_1.password='hidden'\ngiwifi-credentials.other=credential\n")
	if !parseUCISectionExists(data, "giwifi-credentials", "account_1") {
		t.Fatal("应识别已存在的 UCI 凭据 section")
	}
	if parseUCISectionExists(data, "giwifi-credentials", "missing") {
		t.Fatal("不应识别不存在的 UCI 凭据 section")
	}
	if parseUCISectionExists(data, "other-package", "account_1") {
		t.Fatal("不应跨 UCI package 识别 section")
	}
}
func TestEnsureUCICredentialFileCreatesRestrictedPackage(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "giwifi-auto")
	created, err := ensureUCICredentialFile(configPath)
	if err != nil {
		t.Fatalf("创建 UCI 凭据文件失败: %v", err)
	}
	if !created {
		t.Fatal("首次创建 UCI 凭据文件时应返回 created=true")
	}
	credentialPath := credentialFilePath(configPath)
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("读取 UCI 凭据文件失败: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("UCI 凭据文件权限 = %o, want 600", info.Mode().Perm())
	}
	created, err = ensureUCICredentialFile(configPath)
	if err != nil {
		t.Fatalf("重复确保 UCI 凭据文件失败: %v", err)
	}
	if created {
		t.Fatal("已存在的 UCI 凭据文件不应返回 created=true")
	}
}

func TestSaveJSONAccountConfigurationStoresPasswordOutsideConfig(t *testing.T) {
	path := t.TempDir() + "/config.json"
	password := "tui-password"
	credentialRef, err := CredentialReferenceForAccount(path, "new_account")
	if err != nil {
		t.Fatalf("CredentialReferenceForAccount() 失败: %v", err)
	}
	cfg := Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	cfg.Accounts = []AccountConfig{{
		ID:            "new_account",
		DisplayName:   "新账号",
		Username:      "user@example.test",
		CredentialRef: credentialRef,
		Enabled:       true,
	}}
	if err := SaveAccountConfiguration(context.Background(), path, cfg, "new_account", password, true); err != nil {
		t.Fatalf("SaveAccountConfiguration() 失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if strings.Contains(string(data), password) {
		t.Fatal("密码被写入配置文件")
	}
	resolved, err := (credential.DefaultResolver{}).Resolve(context.Background(), credentialRef)
	if err != nil {
		t.Fatalf("读取侧车凭据失败: %v", err)
	}
	if resolved != password {
		t.Fatalf("侧车凭据 = %q, want %q", resolved, password)
	}
}
