package portal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLoginPageHandlesHTMLAttributesAndEscaping(t *testing.T) {
	page, err := ParseLoginPage(strings.NewReader(sampleLoginPage()), 4096)
	if err != nil {
		t.Fatalf("ParseLoginPage() 失败: %v", err)
	}
	if got := page.Fields["sign"]; got != "sig&+value" {
		t.Fatalf("sign = %q, want %q", got, "sig&+value")
	}
	if got := page.Fields["iv"]; got != "1234567890abcdef" {
		t.Fatalf("iv = %q", got)
	}
	if _, exists := page.Fields["outside"]; exists {
		t.Fatal("form 外的 hidden 字段不应被提取")
	}
}

func TestParseLoginPageSelectsLoginForm(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`<html><body><form id="other"><input type="hidden" name="sign" value="other-sign"></form><form id="loginForm">`)
	for _, field := range requiredPageFields {
		value := field + "-value"
		if field == "iv" {
			value = "1234567890abcdef"
		}
		builder.WriteString(fmt.Sprintf(`<input name="%s" value="%s" type="hidden">`, field, value))
	}
	builder.WriteString(`</form></body></html>`)

	page, err := ParseLoginPage(strings.NewReader(builder.String()), 4096)
	if err != nil {
		t.Fatalf("ParseLoginPage() 失败: %v", err)
	}
	if got := page.Fields["sign"]; got != "sign-value" {
		t.Fatalf("sign = %q, want loginForm 字段", got)
	}
}

func TestParseLoginPageRejectsOversizedBody(t *testing.T) {
	_, err := ParseLoginPage(strings.NewReader(strings.Repeat("x", 17)), 16)
	var portalErr *Error
	if err == nil || !errors.As(err, &portalErr) || portalErr.Category != CategoryPageParse {
		t.Fatalf("错误 = %T %v", err, err)
	}
}

func TestBuildLegacyFormKeepsProtocolOrderAndEscaping(t *testing.T) {
	page, err := ParseLoginPage(strings.NewReader(sampleLoginPage()), 4096)
	if err != nil {
		t.Fatalf("ParseLoginPage() 失败: %v", err)
	}

	form, iv, err := BuildLegacyForm(page, "user@example.test", "p+word value")
	if err != nil {
		t.Fatalf("BuildLegacyForm() 失败: %v", err)
	}
	if iv != "1234567890abcdef" {
		t.Fatalf("iv = %q", iv)
	}
	want := "sign=sig%26%2Bvalue&sta_vlan=sta_vlan-value&sta_port=sta_port-value&sta_ip=sta_ip-value&nas_ip=nas_ip-value&nas_name=nas_name-value&last_url=last_url-value&request_ip=request_ip-value&device_mode=device_mode-value&device_type=device_type-value&device_os_type=device_os_type-value&is_mobile=is_mobile-value&iv=1234567890abcdef&login_type=login_type-value&account_type=2&user_account=user%40example.test&user_password=p%2Bword%20value"
	if form != want {
		t.Fatalf("表单 = %q, want %q", form, want)
	}
}

func TestBuildLegacyFormPreservesPageAccountType(t *testing.T) {
	page, err := ParseLoginPage(strings.NewReader(sampleLoginPage()), 4096)
	if err != nil {
		t.Fatalf("ParseLoginPage() 失败: %v", err)
	}
	page.Fields["account_type"] = "1"
	form, _, err := BuildLegacyForm(page, "user@example.test", "password-placeholder")
	if err != nil {
		t.Fatalf("BuildLegacyForm() 失败: %v", err)
	}
	if !strings.Contains(form, "account_type=1&") {
		t.Fatalf("表单未保留页面下发的 account_type: %q", form)
	}
}

func TestEncodeFieldsPreservesOrder(t *testing.T) {
	got, err := EncodeFields([]Field{
		{Name: "second", Value: "a+b c"},
		{Name: "first", Value: "中文"},
	})
	if err != nil {
		t.Fatalf("EncodeFields() 失败: %v", err)
	}
	want := "second=a%2Bb+c&first=%E4%B8%AD%E6%96%87"
	if got != want {
		t.Fatalf("编码结果 = %q, want %q", got, want)
	}
}

func TestEncodeBrowserFieldsMatchesJQueryEncoding(t *testing.T) {
	got, err := EncodeBrowserFields([]Field{
		{Name: "second", Value: "a+b c!"},
		{Name: "first", Value: "中文"},
	})
	if err != nil {
		t.Fatalf("EncodeBrowserFields() 失败: %v", err)
	}
	want := "second=a%2Bb%20c!&first=%E4%B8%AD%E6%96%87"
	if got != want {
		t.Fatalf("浏览器编码结果 = %q, want %q", got, want)
	}
}

func TestEncryptLegacyMatchesGoldenVector(t *testing.T) {
	got, err := EncryptLegacy("0123456789abcdef", "1234567890abcdef")
	if err != nil {
		t.Fatalf("EncryptLegacy() 失败: %v", err)
	}
	const want = "sT89Pv0VTmFgB+bLfEGrnQ=="
	if got != want {
		t.Fatalf("密文 = %q, want %q", got, want)
	}
}

func TestBuildLegacyFormRejectsMissingFieldAndInvalidIV(t *testing.T) {
	page := Page{Fields: map[string]string{
		"sign": "synthetic-sign",
		"iv":   "short",
	}}
	_, _, err := BuildLegacyForm(page, "user@example.test", "password-placeholder")
	var portalErr *Error
	if err == nil || !errors.As(err, &portalErr) || portalErr.Category != CategoryPageParse {
		t.Fatalf("缺失字段错误 = %T %v", err, err)
	}

	page, err = ParseLoginPage(strings.NewReader(sampleLoginPage()), 4096)
	if err != nil {
		t.Fatalf("ParseLoginPage() 失败: %v", err)
	}
	page.Fields["iv"] = "short"
	_, _, err = BuildLegacyForm(page, "user@example.test", "password-placeholder")
	if err == nil || !errors.As(err, &portalErr) || portalErr.Category != CategoryCrypto {
		t.Fatalf("非法 IV 错误 = %T %v", err, err)
	}
}

func TestAuthenticateUsesCookieAndEncryptedPayload(t *testing.T) {
	var loginCalled atomic.Bool
	var cookieReceived atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/portal":
			cookieReceived.Store(false)
			writer.Header().Set("Set-Cookie", "session=synthetic-session; Path=/")
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write([]byte(sampleLoginPage()))
		case "/gportal/Web/loginAction":
			loginCalled.Store(true)
			if cookie, err := request.Cookie("session"); err == nil && cookie.Value == "synthetic-session" {
				cookieReceived.Store(true)
			}
			if err := request.ParseForm(); err != nil {
				http.Error(writer, "bad request", http.StatusBadRequest)
				return
			}
			data := request.Form.Get("data")
			iv := request.Form.Get("iv")
			if data == "" || iv != "1234567890abcdef" {
				http.Error(writer, "bad payload", http.StatusBadRequest)
				return
			}
			if request.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				http.Error(writer, "missing ajax header", http.StatusBadRequest)
				return
			}
			if strings.Contains(data, "password-placeholder") {
				http.Error(writer, "plaintext credential", http.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":1,"data":"pending"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	pageURL, err := url.Parse(server.URL + "/portal")
	if err != nil {
		t.Fatalf("解析 Portal 页面地址失败: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("创建 Cookie Jar 失败: %v", err)
	}
	client := NewClient(nil)
	client.CookieJar = jar
	client.Timeout = time.Second

	err = client.Authenticate(context.Background(), pageURL, "user@example.test", "password-placeholder")
	if err != nil {
		t.Fatalf("Authenticate() 失败: %v", err)
	}
	if !loginCalled.Load() || !cookieReceived.Load() {
		t.Fatalf("登录请求状态: called=%v cookie=%v", loginCalled.Load(), cookieReceived.Load())
	}
}

func TestAuthenticateConfirmsDeviceBinding(t *testing.T) {
	var confirmCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/portal":
			_, _ = writer.Write([]byte(sampleLoginPage()))
		case "/gportal/Web/loginAction":
			if err := request.ParseForm(); err != nil || request.Form.Get("data") == "" || request.Form.Get("iv") == "" {
				http.Error(writer, "bad login request", http.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":0,"data":{"resultCode":124,"resultData":"/gportal/Web/confirmDevice"}}`))
		case "/gportal/Web/confirmDevice":
			if request.Method != http.MethodPost || request.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				http.Error(writer, "bad confirmation request", http.StatusBadRequest)
				return
			}
			if err := request.ParseForm(); err != nil || len(request.Form) != 0 {
				http.Error(writer, "confirmation must be empty", http.StatusBadRequest)
				return
			}
			confirmCalled.Store(true)
			_, _ = writer.Write([]byte(`{"status":1}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	loginEndpoint, _ := url.Parse(server.URL + "/gportal/Web/loginAction")
	pageURL, _ := url.Parse(server.URL + "/portal")
	client := NewClient(loginEndpoint)
	client.Timeout = time.Second
	if err := client.Authenticate(context.Background(), pageURL, "user@example.test", "password-placeholder"); err != nil {
		t.Fatalf("Authenticate() 失败: %v", err)
	}
	if !confirmCalled.Load() {
		t.Fatal("设备绑定确认请求未发送")
	}
}

func TestAuthenticateRejectsCrossOriginDeviceBinding(t *testing.T) {
	var otherCalled atomic.Bool
	otherServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		otherCalled.Store(true)
		_, _ = writer.Write([]byte(`{"status":1}`))
	}))
	defer otherServer.Close()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/portal":
			_, _ = writer.Write([]byte(sampleLoginPage()))
		case "/gportal/Web/loginAction":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"status":0,"data":{"resultCode":124,"resultData":%q}}`, otherServer.URL+"/confirm")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	loginEndpoint, _ := url.Parse(server.URL + "/gportal/Web/loginAction")
	pageURL, _ := url.Parse(server.URL + "/portal")
	client := NewClient(loginEndpoint)
	client.Timeout = time.Second
	err := client.Authenticate(context.Background(), pageURL, "user@example.test", "password-placeholder")
	var portalErr *Error
	if err == nil || !errors.As(err, &portalErr) || portalErr.Category != CategoryProtocol {
		t.Fatalf("跨源确认地址错误 = %T %v", err, err)
	}
	if otherCalled.Load() {
		t.Fatal("不应请求跨源设备绑定确认地址")
	}
}

func TestAuthenticateReportsRejectedLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/portal" {
			_, _ = writer.Write([]byte(sampleLoginPage()))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":0}`))
	}))
	defer server.Close()

	loginEndpoint, _ := url.Parse(server.URL + "/login")
	pageURL, _ := url.Parse(server.URL + "/portal")
	client := NewClient(loginEndpoint)
	client.Timeout = time.Second

	err := client.Authenticate(context.Background(), pageURL, "user@example.test", "password-placeholder")
	if !errors.Is(err, ErrAuthenticationRejected) {
		t.Fatalf("错误 = %T %v, want ErrAuthenticationRejected", err, err)
	}
	var portalErr *Error
	if !errors.As(err, &portalErr) || portalErr.Category != CategoryAuthentication {
		t.Fatalf("错误分类 = %T %v", err, err)
	}
}

func TestAuthenticateRejectsTrailingLoginJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/portal" {
			_, _ = writer.Write([]byte(sampleLoginPage()))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":1}{"status":1}`))
	}))
	defer server.Close()

	loginEndpoint, _ := url.Parse(server.URL + "/login")
	pageURL, _ := url.Parse(server.URL + "/portal")
	client := NewClient(loginEndpoint)
	client.Timeout = time.Second
	if err := client.Authenticate(context.Background(), pageURL, "user@example.test", "password-placeholder"); err == nil {
		t.Fatal("登录响应包含尾随 JSON 时未被拒绝")
	} else {
		var portalErr *Error
		if !errors.As(err, &portalErr) || portalErr.Category != CategoryProtocol {
			t.Fatalf("错误分类 = %T %v", err, err)
		}
	}
}

func sampleLoginPage() string {
	var builder strings.Builder
	builder.WriteString(`<html><body><input type="hidden" name="outside" value="ignored"><form method='post'>`)
	builder.WriteString(`<input value="sig&amp;+value" data-extra="ignored" TYPE="hidden" name="sign">`)
	for _, field := range requiredPageFields[1:] {
		value := field + "-value"
		if field == "iv" {
			value = "1234567890abcdef"
		}
		builder.WriteString(fmt.Sprintf(`<input name='%s' value='%s' type='hidden'>`, field, value))
	}
	builder.WriteString(`</form></body></html>`)
	return builder.String()
}
