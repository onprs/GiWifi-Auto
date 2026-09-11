package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/config"
	"github.com/onprs/GiWifi-Auto/internal/control"
	"github.com/onprs/GiWifi-Auto/internal/eventlog"
)

func TestServiceImplementsControlMethods(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	cfg.Accounts = []config.AccountConfig{{
		ID:            "control-account",
		DisplayName:   "控制测试账号",
		Username:      "user@example.test",
		CredentialRef: "env:GIWIFI_CONTROL_PASSWORD",
		Enabled:       false,
	}}
	service, err := New(cfg, "", Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建监听器失败: %v", err)
	}
	server, err := control.NewServer(listener, service)
	if err != nil {
		t.Fatalf("NewServer() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	var status StatusResponse
	callContext, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()
	if err := control.Call(callContext, "tcp://"+listener.Addr().String(), MethodStatus, struct{}{}, &status); err != nil {
		t.Fatalf("status 调用失败: %v", err)
	}
	if len(status.Accounts) != 1 || status.Accounts[0].ID != "control-account" {
		t.Fatalf("status = %+v", status)
	}

	if err := control.Call(callContext, "tcp://"+listener.Addr().String(), MethodAccountEnable, AccountRequest{ID: "control-account"}, nil); err != nil {
		t.Fatalf("account.enable 调用失败: %v", err)
	}
	if !service.Status()[0].Enabled {
		t.Fatal("account.enable 未更新运行状态")
	}
	if err := control.Call(callContext, "tcp://"+listener.Addr().String(), MethodAccountTrigger, AccountRequest{ID: "control-account"}, nil); err != nil {
		t.Fatalf("account.trigger 调用失败: %v", err)
	}
	if err := control.Call(callContext, "tcp://"+listener.Addr().String(), MethodAccountDisable, AccountRequest{ID: "control-account"}, nil); err != nil {
		t.Fatalf("account.disable 调用失败: %v", err)
	}

	var logs []eventlog.Event
	if err := control.Call(callContext, "tcp://"+listener.Addr().String(), MethodRecentLogs, LogsRequest{Limit: 10}, &logs); err != nil {
		t.Fatalf("logs.recent 调用失败: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("logs.recent 未返回账号操作事件")
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	waited := make(chan []eventlog.Event, 1)
	waitFailure := make(chan error, 1)
	go func() {
		var events []eventlog.Event
		err := control.Call(waitContext, "tcp://"+listener.Addr().String(), MethodWaitLogs, WaitLogsRequest{AfterSequence: logs[len(logs)-1].Sequence, Limit: 1}, &events)
		if err != nil {
			waitFailure <- err
			return
		}
		waited <- events
	}()
	service.events.Append(eventlog.Event{AccountID: "control-account", Message: "等待测试事件"})
	select {
	case events := <-waited:
		if len(events) != 1 || events[0].Message != "等待测试事件" {
			t.Fatalf("logs.wait 结果 = %+v", events)
		}
	case err := <-waitFailure:
		t.Fatalf("logs.wait 失败: %v", err)
	case <-time.After(time.Second):
		t.Fatal("logs.wait 未返回新事件")
	}

	_, remoteErr := callRawMethod(t, listener, control.Request{Version: control.ProtocolVersion, ID: "invalid", Method: MethodRecentLogs, Params: json.RawMessage(`{"limit":1,"extra":true}`)})
	if remoteErr == nil || remoteErr.Code != "invalid_params" {
		t.Fatalf("未知参数错误 = %+v", remoteErr)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() 错误: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("控制服务未退出")
	}
}

func TestServiceReloadReplacesAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.Runtime.PortalLoginURL = "http://portal.example.test/login"
	cfg.Accounts = []config.AccountConfig{{ID: "before", DisplayName: "之前", Enabled: false}}
	if err := config.SaveFileAtomic(path, cfg); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	service, err := New(cfg, path, Dependencies{})
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	cfg.Accounts = []config.AccountConfig{{ID: "after", DisplayName: "之后", Enabled: false}}
	if err := config.SaveFileAtomic(path, cfg); err != nil {
		t.Fatalf("写入新配置失败: %v", err)
	}
	if err := service.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() 失败: %v", err)
	}
	status := service.Status()
	if len(status) != 1 || status[0].ID != "after" {
		t.Fatalf("重载状态 = %+v", status)
	}
}

func callRawMethod(t *testing.T, listener net.Listener, request control.Request) (*control.Response, *control.RemoteError) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("连接控制服务失败: %v", err)
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatalf("发送原始请求失败: %v", err)
	}
	var response control.Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatalf("读取原始响应失败: %v", err)
	}
	if response.Error == nil {
		return &response, nil
	}
	return &response, &control.RemoteError{Code: response.Error.Code, Message: response.Error.Message}
}
