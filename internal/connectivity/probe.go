package connectivity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Status 是连通性探测的结果状态。
type Status string

const (
	StatusAuthenticated Status = "authenticated"
	StatusPortal        Status = "portal"
	StatusOffline       Status = "offline"
)

// ErrorCategory 是探测错误的分类。
type ErrorCategory string

const (
	CategoryConfiguration ErrorCategory = "configuration"
	CategoryNetwork       ErrorCategory = "network"
	CategoryProtocol      ErrorCategory = "protocol"
)

const (
	DefaultTimeout          = 5 * time.Second
	DefaultMaxResponseBytes = 1 << 20
	DefaultUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0"
	DefaultSuccessMarker    = "Success"
)

// ProbeError 保留底层错误原因，同时避免在 Error 文本中回显可能含敏感查询参数的 URL。
type ProbeError struct {
	Category ErrorCategory
	message  string
	cause    error
}

func (e *ProbeError) Error() string {
	if e == nil {
		return ""
	}
	if e.message == "" {
		return string(e.Category)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.message)
}

func (e *ProbeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *ProbeError) ErrorCategory() string {
	if e == nil {
		return ""
	}
	return string(e.Category)
}

// Result 是一次连通性探测结果。RedirectURL 只供后续 Portal 流程使用，不应直接写入日志或状态输出。
type Result struct {
	Status      Status
	HTTPStatus  int
	RedirectURL *url.URL
}

// Probe 执行一次不跟随重定向的连通性探测。
type Probe struct {
	Transport        http.RoundTripper
	CookieJar        http.CookieJar
	Timeout          time.Duration
	MaxResponseBytes int64
	UserAgent        string
	SuccessMarker    string
}

// New 返回使用兼容默认值的探测器。
func New() Probe {
	return Probe{
		Timeout:          DefaultTimeout,
		MaxResponseBytes: DefaultMaxResponseBytes,
		UserAgent:        DefaultUserAgent,
		SuccessMarker:    DefaultSuccessMarker,
	}
}

// Check 检查目标是否已联网、被 Portal 拦截或暂时不可达。
func (p Probe) Check(ctx context.Context, rawURL string) (Result, error) {
	if ctx == nil {
		return Result{}, newProbeError(CategoryConfiguration, "请求上下文不能为空", nil)
	}
	if p.Timeout <= 0 {
		return Result{}, newProbeError(CategoryConfiguration, "探测超时必须大于 0", nil)
	}
	if p.MaxResponseBytes <= 0 {
		return Result{}, newProbeError(CategoryConfiguration, "响应体上限必须大于 0", nil)
	}
	if p.SuccessMarker == "" {
		return Result{}, newProbeError(CategoryConfiguration, "联网成功标记不能为空", nil)
	}

	target, err := parseHTTPURL(rawURL)
	if err != nil {
		return Result{}, newProbeError(CategoryConfiguration, err.Error(), err)
	}

	requestContext, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target.String(), nil)
	if err != nil {
		return Result{}, newProbeError(CategoryConfiguration, "创建连通性请求失败", err)
	}
	if p.UserAgent != "" {
		request.Header.Set("User-Agent", p.UserAgent)
	}

	client := &http.Client{
		Transport: p.Transport,
		Jar:       p.CookieJar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{Status: StatusOffline}, newProbeError(CategoryNetwork, "连通性请求失败", err)
	}
	defer response.Body.Close()

	result := Result{HTTPStatus: response.StatusCode}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		result.Status = StatusPortal
		location := response.Header.Get("Location")
		if location == "" {
			return result, newProbeError(CategoryProtocol, "重定向响应缺少 Location", nil)
		}
		redirectURL, err := target.Parse(location)
		if err != nil {
			return result, newProbeError(CategoryProtocol, "重定向地址无法解析", err)
		}
		redirectURL = target.ResolveReference(redirectURL)
		if _, err := parseHTTPURL(redirectURL.String()); err != nil {
			return result, newProbeError(CategoryProtocol, "重定向地址不是允许的 HTTP URL", err)
		}
		result.RedirectURL = redirectURL
		return result, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, p.MaxResponseBytes+1))
	if err != nil {
		result.Status = StatusOffline
		return result, newProbeError(CategoryNetwork, "读取连通性响应失败", err)
	}
	if int64(len(body)) > p.MaxResponseBytes {
		result.Status = StatusPortal
		return result, newProbeError(CategoryProtocol, "连通性响应超过大小限制", nil)
	}

	result.Status = StatusPortal
	if bytes.Contains(body, []byte(p.SuccessMarker)) {
		result.Status = StatusAuthenticated
	}
	return result, nil
}

func newProbeError(category ErrorCategory, message string, cause error) *ProbeError {
	return &ProbeError{Category: category, message: message, cause: cause}
}

func parseHTTPURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("目标 URL 不能为空")
	}
	if raw != strings.TrimSpace(raw) {
		return nil, errors.New("目标 URL 不能包含首尾空白")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("目标 URL 不是合法 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("目标 URL 只允许使用 http 或 https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("目标 URL 必须包含主机")
	}
	if parsed.User != nil {
		return nil, errors.New("目标 URL 不能包含用户信息")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("目标 URL 不能包含 fragment")
	}
	return parsed, nil
}
