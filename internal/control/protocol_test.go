package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestServerAndClientRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建测试监听器失败: %v", err)
	}
	server, err := NewServer(listener, HandlerFunc(func(_ context.Context, request Request) (interface{}, *RPCError) {
		if request.Method != "status" {
			return nil, &RPCError{Code: "not_found", Message: "方法不存在"}
		}
		return map[string]string{"state": "ok"}, nil
	}))
	if err != nil {
		t.Fatalf("NewServer() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	var result map[string]string
	callContext, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()
	if err := Call(callContext, "tcp://"+listener.Addr().String(), "status", map[string]string{}, &result); err != nil {
		t.Fatalf("Call() 失败: %v", err)
	}
	if result["state"] != "ok" {
		t.Fatalf("结果 = %+v", result)
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

func TestClientReceivesRemoteError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建测试监听器失败: %v", err)
	}
	server, err := NewServer(listener, HandlerFunc(func(context.Context, Request) (interface{}, *RPCError) {
		return nil, &RPCError{Code: "denied", Message: "操作被拒绝"}
	}))
	if err != nil {
		t.Fatalf("NewServer() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	callContext, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()
	err = Call(callContext, "tcp://"+listener.Addr().String(), "status", struct{}{}, nil)
	var remoteErr *RemoteError
	if !errors.As(err, &remoteErr) || remoteErr.Code != "denied" {
		t.Fatalf("错误 = %T %v", err, err)
	}
}

func TestServerRejectsInvalidRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建测试监听器失败: %v", err)
	}
	server, err := NewServer(listener, HandlerFunc(func(context.Context, Request) (interface{}, *RPCError) {
		return nil, nil
	}))
	if err != nil {
		t.Fatalf("NewServer() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("连接测试服务失败: %v", err)
	}
	defer connection.Close()
	_, _ = connection.Write([]byte(`{"version":99,"id":"x","method":"status"}` + "\n"))
	var response Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatalf("读取错误响应失败: %v", err)
	}
	if response.Error == nil || response.Error.Code != "invalid_request" {
		t.Fatalf("错误响应 = %+v", response)
	}
}

func TestCallHonorsCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建测试监听器失败: %v", err)
	}
	server, err := NewServer(listener, HandlerFunc(func(ctx context.Context, request Request) (interface{}, *RPCError) {
		<-ctx.Done()
		return nil, &RPCError{Code: "timeout", Message: "请求超时"}
	}))
	if err != nil {
		t.Fatalf("NewServer() 失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	callContext, callCancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		callDone <- Call(callContext, "tcp://"+listener.Addr().String(), "slow", struct{}{}, nil)
	}()
	time.Sleep(20 * time.Millisecond)
	callCancel()
	select {
	case err := <-callDone:
		if err == nil || !strings.Contains(err.Error(), "control") && !errors.Is(err, context.Canceled) {
			t.Fatalf("取消错误 = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() 未响应取消")
	}
}

func TestLoopbackAddressValidation(t *testing.T) {
	for _, address := range []string{
		"tcp://127.0.0.1:8080",
		"tcp://localhost:8080",
		"tcp://[::1]:8080",
	} {
		if _, err := parseLoopbackTCPAddress(address); err != nil {
			t.Errorf("地址 %q 未通过: %v", address, err)
		}
	}
	for _, address := range []string{
		"tcp://0.0.0.0:8080",
		"tcp://127.0.0.1",
		"tcp://127.0.0.1:0",
		"tcp://127.0.0.1:8080/path",
	} {
		if _, err := parseLoopbackTCPAddress(address); err == nil {
			t.Errorf("地址 %q 未被拒绝", address)
		}
	}
}
