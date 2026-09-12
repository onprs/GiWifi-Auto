package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// CurrentVersion 是当前配置格式版本。
	CurrentVersion = 1

	maxConfigFileSize     = 1 << 20
	maxAccountCount       = 256
	maxConcurrentRequests = 128
	maxEventBufferSize    = 100000
	maxDurationSeconds    = int64((1<<63 - 1) / int64(time.Second))
)

// Config 描述 GiWifi-Auto 的运行配置。
type Config struct {
	Version  int             `json:"version"`
	Runtime  RuntimeConfig   `json:"runtime"`
	Accounts []AccountConfig `json:"accounts"`
}

// RuntimeConfig 描述与账号无关的运行参数。
type RuntimeConfig struct {
	ConnectivityURL       string `json:"connectivity_url"`
	PortalLoginURL        string `json:"portal_login_url"`
	CheckIntervalSeconds  int    `json:"check_interval_seconds"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	RetryInitialSeconds   int    `json:"retry_initial_seconds"`
	RetryMaxSeconds       int    `json:"retry_max_seconds"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	LogLevel              string `json:"log_level"`
	EventBufferSize       int    `json:"event_buffer_size"`
	ControlSocket         string `json:"control_socket"`
}

// AccountConfig 描述一个账号及其网络上下文。
type AccountConfig struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	Username         string `json:"username"`
	CredentialRef    string `json:"credential_ref"`
	Enabled          bool   `json:"enabled"`
	Priority         int    `json:"priority"`
	NetworkInterface string `json:"network_interface"`
}

// ValidationIssue 描述一个配置校验问题。
type ValidationIssue struct {
	Field   string
	Message string
}

// ValidationErrors 汇总配置中的所有校验问题。
type ValidationErrors []ValidationIssue

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("配置无效")
	for _, issue := range e {
		builder.WriteString("\n- ")
		builder.WriteString(issue.Field)
		builder.WriteString(": ")
		builder.WriteString(issue.Message)
	}
	return builder.String()
}

// Default 返回开发阶段使用的最小默认配置。
func Default() Config {
	return Config{
		Version: CurrentVersion,
		Runtime: RuntimeConfig{
			ConnectivityURL:       "http://captive.apple.com/",
			CheckIntervalSeconds:  30,
			RequestTimeoutSeconds: 10,
			RetryInitialSeconds:   10,
			RetryMaxSeconds:       300,
			MaxConcurrentRequests: 2,
			LogLevel:              "info",
			EventBufferSize:       256,
			ControlSocket:         "/var/run/giwifi-auto.sock",
		},
	}
}

// LoadFile 读取并完整校验一个 JSON 配置文件。
func LoadFile(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("配置路径不能为空")
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("打开配置文件 %q 失败: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxConfigFileSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("读取配置文件 %q 失败: %w", path, err)
	}
	if len(data) > maxConfigFileSize {
		return Config{}, fmt.Errorf("配置文件 %q 超过 1 MiB 限制", path)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("配置文件 %q 格式错误: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("配置文件 %q 包含多段 JSON 数据", path)
		}
		return Config{}, fmt.Errorf("配置文件 %q 包含无法解析的尾随内容: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置文件 %q 校验失败: %w", path, err)
	}

	return cfg, nil
}

// Validate 检查配置是否满足当前版本的运行约束。
func (c Config) Validate() error {
	issues := make(ValidationErrors, 0)
	if c.Version != CurrentVersion {
		issues = append(issues, ValidationIssue{
			Field:   "version",
			Message: fmt.Sprintf("必须为 %d", CurrentVersion),
		})
	}

	issues = append(issues, validateRuntime(c.Runtime)...)

	if len(c.Accounts) > maxAccountCount {
		issues = append(issues, ValidationIssue{Field: "accounts", Message: fmt.Sprintf("账号数量不能超过 %d", maxAccountCount)})
	}
	seenIDs := make(map[string]int, len(c.Accounts))
	for index, account := range c.Accounts {
		prefix := fmt.Sprintf("accounts[%d]", index)

		if account.ID == "" {
			issues = append(issues, ValidationIssue{Field: prefix + ".id", Message: "不能为空"})
		} else {
			if !validAccountID(account.ID) {
				issues = append(issues, ValidationIssue{
					Field:   prefix + ".id",
					Message: "只能包含小写字母、数字、连字符和下划线，且首字符必须为小写字母或数字",
				})
			}
			if previous, exists := seenIDs[account.ID]; exists {
				issues = append(issues, ValidationIssue{
					Field:   prefix + ".id",
					Message: fmt.Sprintf("与 accounts[%d].id 重复", previous),
				})
			} else {
				seenIDs[account.ID] = index
			}
		}

		issues = append(issues, validateText(prefix+".display_name", account.DisplayName, 128, true)...)
		issues = append(issues, validateText(prefix+".username", account.Username, 256, false)...)
		issues = append(issues, validateCredentialRef(prefix+".credential_ref", account.CredentialRef)...)
		issues = append(issues, validateText(prefix+".network_interface", account.NetworkInterface, 64, false)...)

		if account.Enabled {
			if strings.TrimSpace(account.Username) == "" {
				issues = append(issues, ValidationIssue{
					Field:   prefix + ".username",
					Message: "启用账号不能为空",
				})
			}
			if strings.TrimSpace(account.CredentialRef) == "" {
				issues = append(issues, ValidationIssue{
					Field:   prefix + ".credential_ref",
					Message: "启用账号必须配置凭据引用，不能填写明文密码",
				})
			}
		}
		if account.Priority < 0 {
			issues = append(issues, ValidationIssue{
				Field:   prefix + ".priority",
				Message: "不能小于 0",
			})
		}
	}

	if len(issues) > 0 {
		return issues
	}
	return nil
}

func validateRuntime(runtime RuntimeConfig) ValidationErrors {
	issues := make(ValidationErrors, 0)
	if issue := validateEndpoint(runtime.ConnectivityURL, true); issue != "" {
		issues = append(issues, ValidationIssue{Field: "runtime.connectivity_url", Message: issue})
	}
	if runtime.PortalLoginURL != "" {
		if issue := validateEndpoint(runtime.PortalLoginURL, true); issue != "" {
			issues = append(issues, ValidationIssue{Field: "runtime.portal_login_url", Message: issue})
		}
	}

	positiveFields := []struct {
		name  string
		value int
	}{
		{name: "runtime.check_interval_seconds", value: runtime.CheckIntervalSeconds},
		{name: "runtime.request_timeout_seconds", value: runtime.RequestTimeoutSeconds},
		{name: "runtime.retry_initial_seconds", value: runtime.RetryInitialSeconds},
		{name: "runtime.retry_max_seconds", value: runtime.RetryMaxSeconds},
		{name: "runtime.max_concurrent_requests", value: runtime.MaxConcurrentRequests},
		{name: "runtime.event_buffer_size", value: runtime.EventBufferSize},
	}
	for _, field := range positiveFields {
		if field.value <= 0 {
			issues = append(issues, ValidationIssue{Field: field.name, Message: "必须大于 0"})
		}
	}
	if runtime.RetryMaxSeconds > 0 && runtime.RetryInitialSeconds > runtime.RetryMaxSeconds {
		issues = append(issues, ValidationIssue{
			Field:   "runtime.retry_max_seconds",
			Message: "不能小于 retry_initial_seconds",
		})
	}

	for _, field := range []struct {
		name  string
		value int
	}{
		{name: "runtime.check_interval_seconds", value: runtime.CheckIntervalSeconds},
		{name: "runtime.request_timeout_seconds", value: runtime.RequestTimeoutSeconds},
		{name: "runtime.retry_initial_seconds", value: runtime.RetryInitialSeconds},
		{name: "runtime.retry_max_seconds", value: runtime.RetryMaxSeconds},
	} {
		if int64(field.value) > maxDurationSeconds {
			issues = append(issues, ValidationIssue{Field: field.name, Message: "数值过大，无法转换为时间间隔"})
		}
	}
	if runtime.MaxConcurrentRequests > maxConcurrentRequests {
		issues = append(issues, ValidationIssue{Field: "runtime.max_concurrent_requests", Message: fmt.Sprintf("不能超过 %d", maxConcurrentRequests)})
	}
	if runtime.EventBufferSize > maxEventBufferSize {
		issues = append(issues, ValidationIssue{Field: "runtime.event_buffer_size", Message: fmt.Sprintf("不能超过 %d", maxEventBufferSize)})
	}

	switch runtime.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		issues = append(issues, ValidationIssue{
			Field:   "runtime.log_level",
			Message: "只能是 debug、info、warn 或 error",
		})
	}

	if strings.TrimSpace(runtime.ControlSocket) == "" {
		issues = append(issues, ValidationIssue{Field: "runtime.control_socket", Message: "不能为空"})
	} else if issue := validateControlAddress(runtime.ControlSocket); issue != "" {
		issues = append(issues, ValidationIssue{Field: "runtime.control_socket", Message: issue})
	}

	return issues
}

func validateControlAddress(address string) string {
	if strings.IndexByte(address, 0) >= 0 {
		return "不能包含 NUL 字符"
	}
	if strings.ContainsAny(address, "\r\n") {
		return "不能包含换行符"
	}
	if strings.HasPrefix(address, "tcp://") {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme != "tcp" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "回环 TCP 地址无效"
		}
		host := parsed.Hostname()
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return "TCP 地址只允许回环主机"
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return "TCP 地址端口无效"
		}
		return ""
	}
	if !strings.HasPrefix(address, "/") {
		return "必须是绝对 Unix 路径或回环 TCP 地址"
	}
	return ""
}

func validateEndpoint(raw string, required bool) string {
	if strings.TrimSpace(raw) == "" {
		if required {
			return "不能为空"
		}
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "不是合法 URL"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "只允许使用 http 或 https"
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "必须包含主机"
	}
	if parsed.User != nil {
		return "不能包含用户信息"
	}
	if parsed.Fragment != "" {
		return "不能包含 fragment"
	}
	return ""
}

func validateText(field, value string, maxBytes int, required bool) ValidationErrors {
	issues := make(ValidationErrors, 0)
	if required && strings.TrimSpace(value) == "" {
		issues = append(issues, ValidationIssue{Field: field, Message: "不能为空"})
	}
	if !utf8.ValidString(value) {
		issues = append(issues, ValidationIssue{Field: field, Message: "必须是有效 UTF-8 文本"})
	}
	if strings.IndexByte(value, 0) >= 0 {
		issues = append(issues, ValidationIssue{Field: field, Message: "不能包含 NUL 字符"})
	}
	if strings.ContainsAny(value, "\r\n") {
		issues = append(issues, ValidationIssue{Field: field, Message: "不能包含换行符"})
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\r' && character != '\n' {
			issues = append(issues, ValidationIssue{Field: field, Message: "不能包含其他控制字符"})
			break
		}
	}
	if len(value) > maxBytes {
		issues = append(issues, ValidationIssue{
			Field:   field,
			Message: fmt.Sprintf("长度不能超过 %d 字节", maxBytes),
		})
	}
	return issues
}

func validateCredentialRef(field, value string) ValidationErrors {
	issues := validateText(field, value, 512, false)
	if strings.TrimSpace(value) == "" {
		return issues
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		issues = append(issues, ValidationIssue{Field: field, Message: "不能包含空白字符"})
		return issues
	}
	prefix, target, found := strings.Cut(value, ":")
	if !found || target == "" {
		return append(issues, ValidationIssue{Field: field, Message: "必须使用 env:、file: 或 uci: 引用"})
	}
	switch prefix {
	case "env":
		if !validCredentialEnvironmentName(target) {
			issues = append(issues, ValidationIssue{Field: field, Message: "环境变量名称无效"})
		}
	case "file":
		if strings.IndexByte(target, 0) >= 0 {
			issues = append(issues, ValidationIssue{Field: field, Message: "凭据文件路径不能包含 NUL 字符"})
		}
	case "uci":
		if !validCredentialUCIReference(target) {
			issues = append(issues, ValidationIssue{Field: field, Message: "UCI 凭据引用包含非法字符"})
		}
	default:
		issues = append(issues, ValidationIssue{Field: field, Message: "必须使用 env:、file: 或 uci: 引用"})
	}
	return issues
}

func validCredentialEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

func validCredentialUCIReference(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validAccountID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index, character := range value {
		valid := character >= 'a' && character <= 'z'
		valid = valid || character >= '0' && character <= '9'
		if index == 0 && !valid {
			return false
		}
		if index > 0 {
			valid = valid || character == '-' || character == '_'
			if !valid {
				return false
			}
		}
	}
	return true
}
