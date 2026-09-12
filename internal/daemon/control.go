package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/control"
)

const (
	MethodPing             = "ping"
	MethodStatus           = "status"
	MethodConfig           = "config"
	MethodRecentLogs       = "logs.recent"
	MethodWaitLogs         = "logs.wait"
	MethodAccountTrigger   = "account.trigger"
	MethodAccountEnable    = "account.enable"
	MethodAccountDisable   = "account.disable"
	MethodAccountConfigure = "account.configure"
	MethodReload           = "reload"
)

// StatusResponse 是 status 方法的返回值。
type StatusResponse struct {
	Accounts []account.Snapshot `json:"accounts"`
}

// ConfigResponse 是 TUI 使用的非敏感配置视图。
type ConfigResponse struct {
	Accounts []AccountConfigView `json:"accounts"`
}

// AccountConfigView 是不包含凭据引用和密码的账号配置视图。
type AccountConfigView struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	Username         string `json:"username"`
	Enabled          bool   `json:"enabled"`
	Priority         int    `json:"priority"`
	NetworkInterface string `json:"network_interface"`
	HasCredential    bool   `json:"has_credential"`
}

// AccountConfigureRequest 是 TUI 保存账号配置的请求。Password 只允许出现在请求中。
type AccountConfigureRequest struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}

// LogsRequest 是 logs.recent 方法的参数。
type LogsRequest struct {
	Limit int `json:"limit"`
}

// WaitLogsRequest 是 logs.wait 方法的参数。
type WaitLogsRequest struct {
	AfterSequence uint64 `json:"after_sequence"`
	Limit         int    `json:"limit"`
}

// AccountRequest 是账号操作方法的参数。
type AccountRequest struct {
	ID string `json:"id"`
}

// Service.Handle 将守护进程能力适配到本地控制协议。
func (service *Service) Handle(ctx context.Context, request control.Request) (interface{}, *control.RPCError) {
	switch request.Method {
	case MethodPing:
		return struct {
			OK bool `json:"ok"`
		}{OK: true}, nil
	case MethodStatus:
		return StatusResponse{Accounts: service.Status()}, nil
	case MethodConfig:
		return service.configuration(), nil
	case MethodRecentLogs:
		var params LogsRequest
		if rpcError := decodeParams(request, &params); rpcError != nil {
			return nil, rpcError
		}
		if params.Limit < 0 || params.Limit > 1000 {
			return nil, &control.RPCError{Code: "invalid_params", Message: "日志数量必须在 0 到 1000 之间"}
		}
		return service.RecentEvents(params.Limit), nil
	case MethodWaitLogs:
		var params WaitLogsRequest
		if rpcError := decodeParams(request, &params); rpcError != nil {
			return nil, rpcError
		}
		if params.Limit < 0 || params.Limit > 1000 {
			return nil, &control.RPCError{Code: "invalid_params", Message: "日志数量必须在 0 到 1000 之间"}
		}
		if params.Limit == 0 {
			params.Limit = 100
		}
		events, err := service.WaitEvents(ctx, params.AfterSequence, params.Limit)
		if err != nil {
			if ctx != nil && ctx.Err() != nil {
				return nil, &control.RPCError{Code: "request_cancelled", Message: "日志等待已取消"}
			}
			return nil, &control.RPCError{Code: "logs_wait_failed", Message: "等待日志失败"}
		}
		return events, nil
	case MethodAccountTrigger:
		var params AccountRequest
		if rpcError := decodeParams(request, &params); rpcError != nil {
			return nil, rpcError
		}
		if params.ID == "" {
			return nil, &control.RPCError{Code: "invalid_params", Message: "账号 ID 不能为空"}
		}
		if !service.hasAccount(params.ID) {
			return nil, &control.RPCError{Code: "account_not_found", Message: "账号不存在"}
		}
		if err := service.Trigger(params.ID); err != nil {
			return nil, &control.RPCError{Code: "account_operation_failed", Message: "唤醒账号失败"}
		}
		return struct{}{}, nil
	case MethodAccountEnable:
		return service.setEnabledFromRequest(ctx, request, true)
	case MethodAccountDisable:
		return service.setEnabledFromRequest(ctx, request, false)
	case MethodAccountConfigure:
		var params AccountConfigureRequest
		if rpcError := decodeParams(request, &params); rpcError != nil {
			return nil, rpcError
		}
		if err := service.ConfigureAccount(ctx, params); err != nil {
			return nil, &control.RPCError{Code: "account_configure_failed", Message: "账号配置未保存: " + err.Error()}
		}
		return struct{}{}, nil
	case MethodReload:
		if err := service.Reload(ctx); err != nil {
			return nil, &control.RPCError{Code: "reload_failed", Message: "重载配置失败"}
		}
		return struct{}{}, nil
	default:
		return nil, &control.RPCError{Code: "method_not_found", Message: "控制方法不存在"}
	}
}

func (service *Service) setEnabledFromRequest(ctx context.Context, request control.Request, enabled bool) (interface{}, *control.RPCError) {
	var params AccountRequest
	if rpcError := decodeParams(request, &params); rpcError != nil {
		return nil, rpcError
	}
	if params.ID == "" {
		return nil, &control.RPCError{Code: "invalid_params", Message: "账号 ID 不能为空"}
	}
	if !service.hasAccount(params.ID) {
		return nil, &control.RPCError{Code: "account_not_found", Message: "账号不存在"}
	}
	if err := service.SetEnabled(ctx, params.ID, enabled); err != nil {
		return nil, &control.RPCError{Code: "account_update_failed", Message: "更新账号状态失败"}
	}
	return struct{}{}, nil
}

func (service *Service) configuration() ConfigResponse {
	service.mu.RLock()
	defer service.mu.RUnlock()
	result := ConfigResponse{
		Accounts: make([]AccountConfigView, 0, len(service.config.Accounts)),
	}
	for _, accountConfig := range service.config.Accounts {
		result.Accounts = append(result.Accounts, AccountConfigView{
			ID:               accountConfig.ID,
			DisplayName:      accountConfig.DisplayName,
			Username:         accountConfig.Username,
			Enabled:          accountConfig.Enabled,
			Priority:         accountConfig.Priority,
			NetworkInterface: accountConfig.NetworkInterface,
			HasCredential:    strings.TrimSpace(accountConfig.CredentialRef) != "",
		})
	}
	return result
}

func (service *Service) hasAccount(id string) bool {
	service.mu.RLock()
	defer service.mu.RUnlock()
	_, exists := service.runners[id]
	return exists
}

func decodeParams(request control.Request, target interface{}) *control.RPCError {
	if len(request.Params) == 0 {
		return &control.RPCError{Code: "invalid_params", Message: "请求参数不能为空"}
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &control.RPCError{Code: "invalid_params", Message: "请求参数格式无效"}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return &control.RPCError{Code: "invalid_params", Message: "请求参数包含多段 JSON 数据"}
		}
		return &control.RPCError{Code: "invalid_params", Message: "请求参数格式无效"}
	}
	return nil
}

var _ control.Handler = (*Service)(nil)
