package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/config"
	"github.com/onprs/GiWifi-Auto/internal/control"
	"github.com/onprs/GiWifi-Auto/internal/daemon"
	"github.com/onprs/GiWifi-Auto/internal/eventlog"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(version) code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "giwifi-auto dev\n" {
		t.Fatalf("version 输出 = %q", stdout.String())
	}
}

func TestRunCheckJSON(t *testing.T) {
	path := t.TempDir() + "/config.json"
	content := `{
		"version": 1,
		"runtime": {
			"connectivity_url": "http://example.test",
			"portal_login_url": "http://portal.example.test/login",
			"check_interval_seconds": 30,
			"request_timeout_seconds": 10,
			"retry_initial_seconds": 5,
			"retry_max_seconds": 120,
			"max_concurrent_requests": 1,
			"log_level": "info",
			"event_buffer_size": 32,
			"control_socket": "/tmp/giwifi-auto.sock"
		},
		"accounts": [
			{"id": "disabled-sample", "display_name": "示例", "enabled": false},
			{"id": "enabled-sample", "display_name": "启用示例", "username": "user@example.test", "credential_ref": "uci:sample.password", "enabled": true}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "--config", path, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(check) code = %d, stderr = %q", code, stderr.String())
	}

	var result checkResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON 输出解析失败: %v; output=%q", err, stdout.String())
	}
	if !result.Valid || result.AccountCount != 2 || result.EnabledAccountCount != 1 {
		t.Fatalf("检查结果 = %+v", result)
	}
}

func TestCheckResultJSONIncludesZeroCounts(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := writeJSON(&stdout, checkResult{Valid: true, Version: 1}, &stderr); err != nil {
		t.Fatalf("writeJSON() 失败: %v", err)
	}
	for _, field := range []string{`"account_count":0`, `"enabled_account_count":0`} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("JSON 输出缺少稳定字段 %q: %q", field, stdout.String())
		}
	}
}

func TestRunProbeJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("Success"))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Runtime.ConnectivityURL = server.URL
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"probe", "--config", path, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(probe) code = %d, stderr = %q", code, stderr.String())
	}

	var result probeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON 输出解析失败: %v; output=%q", err, stdout.String())
	}
	if result.Status != "authenticated" || result.HTTPStatus != http.StatusOK || result.Redirected {
		t.Fatalf("探测结果 = %+v", result)
	}
}

func TestRunProbeDoesNotPrintRedirectURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Location", "/login?sign=synthetic")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Runtime.ConnectivityURL = server.URL
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"probe", "--config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(probe) code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "检测到 Portal 重定向") {
		t.Fatalf("输出未说明检测到重定向: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), server.URL) || strings.Contains(stdout.String(), "sign=synthetic") {
		t.Fatalf("输出泄露重定向地址: %q", stdout.String())
	}
}

func TestRunStatusAccountAndLogs(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	cfg.Accounts = []config.AccountConfig{{
		ID:            "cli-account",
		DisplayName:   "CLI 测试账号",
		Username:      "user@example.test",
		CredentialRef: "env:GIWIFI_CLI_PASSWORD",
		Enabled:       false,
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建监听器失败: %v", err)
	}
	service, err := daemon.New(cfg, "", daemon.Dependencies{})
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	server, err := control.NewServer(listener, service)
	if err != nil {
		t.Fatalf("创建控制服务失败: %v", err)
	}
	serverContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serverContext) }()

	cfg.Runtime.ControlSocket = "tcp://" + listener.Addr().String()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	var status daemon.StatusResponse
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--config", path, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status code = %d, stderr=%q", code, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || len(status.Accounts) != 1 {
		t.Fatalf("status 输出 = %q, 错误 = %v", stdout.String(), err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"account", "enable", "cli-account", "--config", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("account enable code = %d, stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"logs", "--config", path, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("logs code = %d, stderr=%q", code, stderr.String())
	}
	var events []eventlog.Event
	if err := json.Unmarshal(stdout.Bytes(), &events); err != nil || len(events) == 0 {
		t.Fatalf("logs 输出 = %q, 错误 = %v", stdout.String(), err)
	}
	if strings.Contains(stdout.String(), "user@example.test") || strings.Contains(stdout.String(), "GIWIFI_CLI_PASSWORD") {
		t.Fatalf("CLI 输出泄露账号或凭据引用: %q", stdout.String())
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("控制服务退出错误: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("控制服务未退出")
	}
}

func TestRunCheckRequiresConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"check"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(check) code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--config") {
		t.Fatalf("错误输出未提示 --config: %q", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unknown"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("未知命令 code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "未知命令") {
		t.Fatalf("错误输出 = %q", stderr.String())
	}
}
