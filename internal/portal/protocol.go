package portal

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ErrorCategory 是 Portal 流程错误的分类。
type ErrorCategory string

const (
	CategoryConfiguration  ErrorCategory = "configuration"
	CategoryNetwork        ErrorCategory = "network"
	CategoryPageParse      ErrorCategory = "portal_parse"
	CategoryCrypto         ErrorCategory = "crypto"
	CategoryProtocol       ErrorCategory = "protocol"
	CategoryAuthentication ErrorCategory = "authentication"
	// CategoryRateLimited 表示 Portal 暂时限制登录频率，稍后重试即可。
	CategoryRateLimited ErrorCategory = "rate_limit"
	// CategoryPending 表示 Portal 已受理登录但仍在等待计费开始，需要稍后复检。
	CategoryPending ErrorCategory = "pending"
	// CategoryAccountRestricted 表示账号套餐或设备策略拒绝登录，需要人工处理。
	CategoryAccountRestricted ErrorCategory = "account_restricted"
)

var ErrAuthenticationRejected = errors.New("authentication rejected")

// ErrAuthenticationRateLimited 表示服务端限制登录频率。
var ErrAuthenticationRateLimited = errors.New("authentication rate limited")

// ErrAuthenticationPending 表示服务端已受理登录，等待计费开始。
var ErrAuthenticationPending = errors.New("authentication pending billing")

// ErrAccountRestricted 表示账号套餐或设备策略不允许当前设备上网。
var ErrAccountRestricted = errors.New("account restricted by portal policy")

// ErrDeviceBindingConfirmationRejected 表示服务端拒绝设备绑定确认。
var ErrDeviceBindingConfirmationRejected = errors.New("device binding confirmation rejected")

const (
	DefaultTimeout          = 10 * time.Second
	DefaultMaxResponseBytes = 1 << 20
	DefaultMaxRedirects     = 3
	DefaultUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0"
	DefaultLoginPath        = "/gportal/Web/loginAction"
	LegacyKey               = "1234567887654321"
	// DeviceBindingResultCode 是现场 Portal 的 MAC 绑定确认结果码。
	DeviceBindingResultCode = 124
	// AccountRestrictedResultCode 是现场 Portal 的套餐设备类型限制结果码。
	AccountRestrictedResultCode = 123
	// RateLimitedDataCode 是现场 Portal 在 data 字段返回的登录频率限制码。
	RateLimitedDataCode = 102
)

var legacyFieldOrder = []string{
	"sign",
	"sta_vlan",
	"sta_port",
	"sta_ip",
	"nas_ip",
	"nas_name",
	"last_url",
	"request_ip",
	"device_mode",
	"device_type",
	"device_os_type",
	"is_mobile",
	"iv",
	"login_type",
	"account_type",
	"user_account",
	"user_password",
}

var requiredPageFields = []string{
	"sign",
	"iv",
	"sta_vlan",
	"sta_port",
	"sta_ip",
	"nas_ip",
	"nas_name",
	"last_url",
	"request_ip",
	"device_mode",
	"device_type",
	"device_os_type",
	"is_mobile",
	"login_type",
}

type loginResponse struct {
	Status int             `json:"status"`
	Info   string          `json:"info"`
	Data   json.RawMessage `json:"data"`
}

// loginResponseData 对应 data 为对象时的登录结果。
// resultCode 可能为数字、字符串或 null，resultData 可能是地址字符串或会话对象。
type loginResponseData struct {
	ResultCode    json.RawMessage
	ResultData    json.RawMessage
	HasResultCode bool
}

// Error 保留底层原因，但 Error 文本不回显 URL、凭据或认证载荷。
type Error struct {
	Category ErrorCategory
	message  string
	cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.message == "" {
		return string(e.Category)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) ErrorCategory() string {
	if e == nil {
		return ""
	}
	return string(e.Category)
}

// UserMessage 返回可直接展示给用户的原因文本。
func (e *Error) UserMessage() string {
	if e == nil {
		return ""
	}
	return e.message
}

// Page 是从 Portal 登录页提取的隐藏表单字段。
type Page struct {
	Fields map[string]string
}

// Client 执行 Portal 页面获取和登录请求。每个账号应使用独立的 CookieJar。
type Client struct {
	Transport        http.RoundTripper
	CookieJar        http.CookieJar
	LoginEndpoint    *url.URL
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRedirects     int
	UserAgent        string
	PageReferer      *url.URL
}

// NewClient 返回使用兼容默认值的 Portal 客户端。
func NewClient(loginEndpoint *url.URL) Client {
	return Client{
		LoginEndpoint:    loginEndpoint,
		Timeout:          DefaultTimeout,
		MaxResponseBytes: DefaultMaxResponseBytes,
		MaxRedirects:     DefaultMaxRedirects,
		UserAgent:        DefaultUserAgent,
	}
}

// ParseLoginPage 使用 HTML 解析器提取实际登录表单的 hidden input。
func ParseLoginPage(reader io.Reader, maxBytes int64) (Page, error) {
	if reader == nil {
		return Page{}, newError(CategoryPageParse, "登录页读取器不能为空", nil)
	}
	if maxBytes <= 0 {
		return Page{}, newError(CategoryConfiguration, "响应体上限必须大于 0", nil)
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return Page{}, newError(CategoryNetwork, "读取登录页失败", err)
	}
	if int64(len(body)) > maxBytes {
		return Page{}, newError(CategoryPageParse, "登录页超过大小限制", nil)
	}

	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return Page{}, newError(CategoryPageParse, "解析登录页失败", err)
	}

	type parsedForm struct {
		id     string
		fields map[string]string
	}
	forms := make([]parsedForm, 0, 1)
	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		if node.Type == html.ElementNode && node.Data == "form" {
			formID := ""
			for _, attribute := range node.Attr {
				if attribute.Key == "id" {
					formID = attribute.Val
					break
				}
			}
			form := parsedForm{id: formID, fields: make(map[string]string)}
			var collect func(*html.Node) error
			collect = func(current *html.Node) error {
				if current.Type == html.ElementNode && current.Data == "form" {
					return nil
				}
				if current.Type == html.ElementNode && current.Data == "input" && isHiddenInput(current) {
					name, value, hasName := inputAttributes(current)
					if hasName && name != "" {
						if _, exists := form.fields[name]; exists {
							return newError(CategoryPageParse, "登录表单包含重复 hidden 字段", nil)
						}
						form.fields[name] = value
					}
				}
				for child := current.FirstChild; child != nil; child = child.NextSibling {
					if err := collect(child); err != nil {
						return err
					}
				}
				return nil
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if err := collect(child); err != nil {
					return err
				}
			}
			forms = append(forms, form)
			return nil
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document); err != nil {
		return Page{}, err
	}

	selected := -1
	for index := range forms {
		if forms[index].id == "loginForm" {
			selected = index
			break
		}
	}
	if selected < 0 {
		for index := range forms {
			if hasRequiredPageFields(forms[index].fields) {
				selected = index
				break
			}
		}
	}
	if selected < 0 {
		return Page{Fields: make(map[string]string)}, nil
	}
	return Page{Fields: forms[selected].fields}, nil
}

func hasRequiredPageFields(fields map[string]string) bool {
	for _, field := range requiredPageFields {
		if _, exists := fields[field]; !exists {
			return false
		}
	}
	return true
}

// BuildLegacyForm 按 Portal 登录页实际 DOM 顺序生成加密前的表单字符串。
func BuildLegacyForm(page Page, username, password string) (string, string, error) {
	for _, field := range requiredPageFields {
		if _, exists := page.Fields[field]; !exists {
			return "", "", newError(CategoryPageParse, "登录页缺少必需字段", nil)
		}
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return "", "", newError(CategoryConfiguration, "认证凭据不能为空", nil)
	}

	iv := page.Fields["iv"]
	if len([]byte(iv)) != aes.BlockSize {
		return "", "", newError(CategoryCrypto, "IV 必须为 16 字节", nil)
	}

	fields := make([]Field, 0, len(legacyFieldOrder))
	for _, name := range legacyFieldOrder {
		value := page.Fields[name]
		switch name {
		case "account_type":
			if value == "" {
				value = "2"
			}
		case "user_account":
			value = username
		case "user_password":
			value = password
		}
		fields = append(fields, Field{Name: name, Value: value})
	}
	encoded, err := EncodeBrowserFields(fields)
	if err != nil {
		return "", "", newError(CategoryProtocol, "生成登录表单失败", err)
	}
	return encoded, iv, nil
}

// Field 是一个有序表单字段。
type Field struct {
	Name  string
	Value string
}

// EncodeFields 使用 QueryEscape 编码有序字段，保留调用方提供的字段顺序。
func EncodeFields(fields []Field) (string, error) {
	return encodeFields(fields, url.QueryEscape)
}

// EncodeBrowserFields 复刻 jQuery 表单序列化使用的 encodeURIComponent 规则。
func EncodeBrowserFields(fields []Field) (string, error) {
	return encodeFields(fields, encodeURIComponent)
}

func encodeFields(fields []Field, escape func(string) string) (string, error) {
	if len(fields) == 0 {
		return "", newError(CategoryProtocol, "登录表单不能为空", nil)
	}
	var builder strings.Builder
	for index, field := range fields {
		if field.Name == "" {
			return "", newError(CategoryProtocol, "登录表单字段名不能为空", nil)
		}
		if index > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(escape(field.Name))
		builder.WriteByte('=')
		builder.WriteString(escape(field.Value))
	}
	return builder.String(), nil
}

func encodeURIComponent(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || strings.ContainsRune("-_.!~*'()", rune(character)) {
			builder.WriteByte(character)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hex[character>>4])
		builder.WriteByte(hex[character&15])
	}
	return builder.String()
}

// EncryptLegacy 使用旧协议的 AES-128-CBC 和零填充规则生成标准 Base64 密文。
func EncryptLegacy(data, iv string) (string, error) {
	return encryptCBC([]byte(data), []byte(LegacyKey), []byte(iv))
}

// Authenticate 获取登录页、生成认证载荷并提交到配置的登录端点。
func (c Client) Authenticate(ctx context.Context, pageURL *url.URL, username, password string) error {
	if err := c.validate(); err != nil {
		return err
	}
	if ctx == nil {
		return newError(CategoryConfiguration, "请求上下文不能为空", nil)
	}
	if pageURL == nil {
		return newError(CategoryConfiguration, "Portal 页面地址不能为空", nil)
	}
	if _, err := parseAllowedURL(pageURL.String()); err != nil {
		return newError(CategoryConfiguration, "Portal 页面地址无效", err)
	}

	page, err := c.fetchLoginPage(ctx, pageURL)
	if err != nil {
		return err
	}
	formData, iv, err := BuildLegacyForm(page, username, password)
	if err != nil {
		return err
	}
	loginEndpoint := c.LoginEndpoint
	if loginEndpoint == nil {
		loginEndpoint, err = defaultLoginEndpoint(pageURL)
		if err != nil {
			return newError(CategoryConfiguration, "无法确定登录端点", err)
		}
	}
	encrypted, err := EncryptLegacy(formData, iv)
	if err != nil {
		return newError(CategoryCrypto, "生成认证载荷失败", err)
	}
	payload, err := EncodeBrowserFields([]Field{
		{Name: "data", Value: encrypted},
		{Name: "iv", Value: iv},
	})
	if err != nil {
		return newError(CategoryProtocol, "生成登录请求失败", err)
	}

	requestContext, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, loginEndpoint.String(), strings.NewReader(payload))
	if err != nil {
		return newError(CategoryConfiguration, "创建登录请求失败", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	request.Header.Set("Referer", pageURL.String())
	if c.UserAgent != "" {
		request.Header.Set("User-Agent", c.UserAgent)
	}

	response, err := c.newHTTPClient(false).Do(request)
	if err != nil {
		return newError(CategoryNetwork, "登录请求失败", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newError(CategoryProtocol, "登录接口返回异常状态", nil)
	}

	body, err := readLimited(response.Body, c.MaxResponseBytes)
	if err != nil {
		return newError(CategoryNetwork, "读取登录响应失败", err)
	}
	result, err := decodeLoginResponse(body)
	if err != nil {
		return newError(CategoryProtocol, "登录响应不是合法 JSON", err)
	}
	if result.Status == 1 {
		return nil
	}
	return c.handleLoginFailure(ctx, pageURL, result)
}

// handleLoginFailure 将服务端 status=0 的响应按现场返回结构归类。
// 现场 Portal 的 data 可能是对象、数字或 null，直接按结构判断会丢掉真实原因。
func (c Client) handleLoginFailure(ctx context.Context, pageURL *url.URL, result loginResponse) error {
	info := strings.TrimSpace(result.Info)

	if data, ok := decodeLoginResponseData(result.Data); ok {
		if resultCode, valid := parseResultCode(data.ResultCode); valid {
			switch resultCode {
			case DeviceBindingResultCode:
				bindingURL, valid := decodeJSONText(data.ResultData)
				if !valid || strings.TrimSpace(bindingURL) == "" {
					return newError(CategoryProtocol, "设备绑定确认地址无效", nil)
				}
				if err := c.confirmDeviceBinding(ctx, pageURL, bindingURL); err != nil {
					return err
				}
				return nil
			case AccountRestrictedResultCode:
				return newError(CategoryAccountRestricted, withServerInfo("认证被服务端拒绝", info), ErrAccountRestricted)
			default:
				return newError(CategoryAuthentication, withServerInfo("认证被服务端拒绝", info), ErrAuthenticationRejected)
			}
		}
		// resultCode 为 null：现场 Portal 在等待计费开始时返回这种结构。
		if data.HasResultCode {
			return newError(CategoryPending, withServerInfo("认证已受理，等待计费开始", info), ErrAuthenticationPending)
		}
		return newError(CategoryAuthentication, withServerInfo("认证被服务端拒绝", info), ErrAuthenticationRejected)
	}

	if code, valid := parseDataCode(result.Data); valid && code == RateLimitedDataCode {
		return newError(CategoryRateLimited, withServerInfo("认证请求过于频繁", info), ErrAuthenticationRateLimited)
	}
	return newError(CategoryAuthentication, withServerInfo("认证被服务端拒绝", info), ErrAuthenticationRejected)
}

// withServerInfo 拼接服务端返回的原因文本，缺失时只保留本地说明。
func withServerInfo(message, info string) string {
	if info == "" {
		return message
	}
	return message + "：" + info
}

// decodeLoginResponseData 在 data 是 JSON 对象时解码登录结果。
func decodeLoginResponseData(raw json.RawMessage) (loginResponseData, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' {
		return loginResponseData{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return loginResponseData{}, false
	}
	data := loginResponseData{}
	if code, exists := fields["resultCode"]; exists {
		data.ResultCode = code
		data.HasResultCode = true
	}
	if resultData, exists := fields["resultData"]; exists {
		data.ResultData = resultData
	}
	return data, true
}

// parseDataCode 解析 data 为数字时的结果码。
func parseDataCode(raw json.RawMessage) (int, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return 0, false
	}
	var number int
	if err := json.Unmarshal(value, &number); err != nil {
		return 0, false
	}
	return number, true
}

// decodeJSONText 解析 resultData 为地址字符串，对象或 null 视为缺失。
func decodeJSONText(raw json.RawMessage) (string, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '"' {
		return "", false
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", false
	}
	return text, true
}

func defaultLoginEndpoint(pageURL *url.URL) (*url.URL, error) {
	return sameOriginURL(pageURL, DefaultLoginPath)
}

func decodeLoginResponse(body []byte) (loginResponse, error) {
	var result loginResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&result); err != nil {
		return loginResponse{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return loginResponse{}, errors.New("登录响应包含多段 JSON 数据")
	}
	return result, nil
}

func parseResultCode(raw json.RawMessage) (int, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return 0, false
	}
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	number, err := strconv.Atoi(text)
	return number, err == nil
}

func (c Client) confirmDeviceBinding(ctx context.Context, pageURL *url.URL, rawURL string) error {
	confirmationURL, err := sameOriginURL(pageURL, rawURL)
	if err != nil {
		return newError(CategoryProtocol, "设备绑定确认地址无效", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, confirmationURL.String(), strings.NewReader(""))
	if err != nil {
		return newError(CategoryConfiguration, "创建设备绑定确认请求失败", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Referer", pageURL.String())
	if c.UserAgent != "" {
		request.Header.Set("User-Agent", c.UserAgent)
	}
	response, err := c.newHTTPClient(false).Do(request)
	if err != nil {
		return newError(CategoryNetwork, "设备绑定确认请求失败", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newError(CategoryAuthentication, "设备绑定确认被服务端拒绝", ErrDeviceBindingConfirmationRejected)
	}
	if _, err := readLimited(response.Body, c.MaxResponseBytes); err != nil {
		return newError(CategoryNetwork, "读取设备绑定确认响应失败", err)
	}
	return nil
}

func sameOriginURL(base *url.URL, raw string) (*url.URL, error) {
	if base == nil || strings.TrimSpace(raw) == "" || raw != strings.TrimSpace(raw) {
		return nil, errors.New("确认地址不能为空且不能包含首尾空白")
	}
	parsed, err := base.Parse(raw)
	if err != nil {
		return nil, errors.New("确认地址不是合法 URL")
	}
	if _, err := parseAllowedURL(parsed.String()); err != nil {
		return nil, err
	}
	if !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
		return nil, errors.New("确认地址必须与登录页同源")
	}
	return parsed, nil
}

func (c Client) fetchLoginPage(ctx context.Context, pageURL *url.URL) (Page, error) {
	requestContext, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return Page{}, newError(CategoryConfiguration, "创建 Portal 页面请求失败", err)
	}
	if c.UserAgent != "" {
		request.Header.Set("User-Agent", c.UserAgent)
	}
	if c.PageReferer != nil {
		request.Header.Set("Referer", c.PageReferer.String())
	}
	response, err := c.newHTTPClient(true).Do(request)
	if err != nil {
		return Page{}, newError(CategoryNetwork, "获取 Portal 页面失败", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Page{}, newError(CategoryProtocol, "Portal 页面返回异常状态", nil)
	}
	return ParseLoginPage(response.Body, c.MaxResponseBytes)
}

func (c Client) validate() error {
	if c.LoginEndpoint != nil {
		if _, err := parseAllowedURL(c.LoginEndpoint.String()); err != nil {
			return newError(CategoryConfiguration, "登录端点无效", err)
		}
	}
	if c.Timeout <= 0 {
		return newError(CategoryConfiguration, "请求超时必须大于 0", nil)
	}
	if c.MaxResponseBytes <= 0 {
		return newError(CategoryConfiguration, "响应体上限必须大于 0", nil)
	}
	if c.MaxRedirects <= 0 {
		return newError(CategoryConfiguration, "重定向上限必须大于 0", nil)
	}
	return nil
}

func (c Client) newHTTPClient(followPageRedirects bool) *http.Client {
	client := &http.Client{Transport: c.Transport, Jar: c.CookieJar}
	if !followPageRedirects {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
		return client
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= c.MaxRedirects {
			return errors.New("重定向次数超过限制")
		}
		if _, err := parseAllowedURL(request.URL.String()); err != nil {
			return err
		}
		return nil
	}
	return client
}

func readLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("响应体上限必须大于 0")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("响应体超过大小限制")
	}
	return body, nil
}

func encryptCBC(data, key, iv []byte) (string, error) {
	if len(key) != 16 {
		return "", newError(CategoryCrypto, "AES-128 密钥必须为 16 字节", nil)
	}
	if len(iv) != aes.BlockSize {
		return "", newError(CategoryCrypto, "IV 必须为 16 字节", nil)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", newError(CategoryCrypto, "创建 AES 密码器失败", err)
	}
	padded := zeroPad(data, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func zeroPad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	if padding == blockSize {
		return append([]byte(nil), data...)
	}
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	return padded
}

func isHiddenInput(node *html.Node) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == "type" {
			return strings.EqualFold(attribute.Val, "hidden")
		}
	}
	return false
}

func inputAttributes(node *html.Node) (name, value string, hasName bool) {
	for _, attribute := range node.Attr {
		switch attribute.Key {
		case "name":
			name = attribute.Val
			hasName = true
		case "value":
			value = attribute.Val
		}
	}
	return name, value, hasName
}

func parseAllowedURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" || raw != strings.TrimSpace(raw) {
		return nil, errors.New("URL 不能为空且不能包含首尾空白")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("URL 不是合法 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("URL 只允许使用 http 或 https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("URL 必须包含主机")
	}
	if parsed.User != nil {
		return nil, errors.New("URL 不能包含用户信息")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("URL 不能包含 fragment")
	}
	return parsed, nil
}

func newError(category ErrorCategory, message string, cause error) *Error {
	return &Error{Category: category, message: message, cause: cause}
}
