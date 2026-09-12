package connectivity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeClassifiesAuthenticatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("User-Agent"); got != DefaultUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, DefaultUserAgent)
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("<html><body>Success</body></html>"))
	}))
	defer server.Close()

	result, err := New().Check(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Check() 失败: %v", err)
	}
	if result.Status != StatusAuthenticated || result.HTTPStatus != http.StatusOK {
		t.Fatalf("结果 = %+v", result)
	}
}

func TestProbeClassifiesPortalWithoutFollowingRedirect(t *testing.T) {
	loginRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/login" {
			loginRequests++
			writer.WriteHeader(http.StatusOK)
			return
		}
		writer.Header().Set("Location", "/login")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	result, err := New().Check(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Check() 失败: %v", err)
	}
	if result.Status != StatusPortal || result.HTTPStatus != http.StatusFound {
		t.Fatalf("结果 = %+v", result)
	}
	if result.RedirectURL == nil || result.RedirectURL.String() != server.URL+"/login" {
		t.Fatalf("RedirectURL = %v", result.RedirectURL)
	}
	if loginRequests != 0 {
		t.Fatalf("探测器跟随了重定向，请求次数 = %d", loginRequests)
	}
}

func TestProbeClassifiesResponseWithoutMarkerAsPortal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("login required"))
	}))
	defer server.Close()

	result, err := New().Check(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Check() 失败: %v", err)
	}
	if result.Status != StatusPortal {
		t.Fatalf("状态 = %q, want %q", result.Status, StatusPortal)
	}
}

func TestProbeRejectsUnsafeEndpoint(t *testing.T) {
	for _, rawURL := range []string{
		"ftp://example.test",
		"http://user:pass@example.test",
		"https://example.test/check#fragment",
		"http://",
	} {
		result, err := New().Check(context.Background(), rawURL)
		if err == nil {
			t.Errorf("URL %q 未被拒绝", rawURL)
			continue
		}
		var probeErr *ProbeError
		if !errors.As(err, &probeErr) || probeErr.Category != CategoryConfiguration {
			t.Errorf("URL %q 错误 = %T %v", rawURL, err, err)
		}
		if result.Status != "" {
			t.Errorf("URL %q 失败结果 = %+v", rawURL, result)
		}
	}
}

func TestProbeRejectsInvalidRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Location", "javascript:alert(1)")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	result, err := New().Check(context.Background(), server.URL)
	if err == nil {
		t.Fatal("非法重定向未返回错误")
	}
	var probeErr *ProbeError
	if !errors.As(err, &probeErr) || probeErr.Category != CategoryProtocol {
		t.Fatalf("错误 = %T %v", err, err)
	}
	if result.Status != StatusPortal || result.RedirectURL != nil {
		t.Fatalf("结果 = %+v", result)
	}
}

func TestProbeRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("12345"))
	}))
	defer server.Close()

	probe := New()
	probe.MaxResponseBytes = 4
	result, err := probe.Check(context.Background(), server.URL)
	if err == nil {
		t.Fatal("超大响应未返回错误")
	}
	var probeErr *ProbeError
	if !errors.As(err, &probeErr) || probeErr.Category != CategoryProtocol {
		t.Fatalf("错误 = %T %v", err, err)
	}
	if result.Status != StatusPortal {
		t.Fatalf("状态 = %q, want %q", result.Status, StatusPortal)
	}
}

func TestProbeReturnsOfflineForTransportError(t *testing.T) {
	probe := New()
	probe.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic transport failure")
	})

	result, err := probe.Check(context.Background(), "http://example.test")
	if err == nil {
		t.Fatal("传输错误未返回错误")
	}
	var probeErr *ProbeError
	if !errors.As(err, &probeErr) || probeErr.Category != CategoryNetwork {
		t.Fatalf("错误 = %T %v", err, err)
	}
	if result.Status != StatusOffline {
		t.Fatalf("状态 = %q, want %q", result.Status, StatusOffline)
	}
	if strings.Contains(err.Error(), "example.test") {
		t.Fatalf("错误文本泄露目标 URL: %v", err)
	}
}

func TestProbeFallsBackToURLAfterNetworkError(t *testing.T) {
	calls := make([]string, 0, 2)
	probe := New()
	probe.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls = append(calls, request.URL.String())
		if request.URL.Host == "primary.example.test" {
			return nil, errors.New("primary endpoint unavailable")
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"/login"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	probe.FallbackURL = func(context.Context) (string, error) {
		return "http://gateway.example.test/", nil
	}

	result, err := probe.Check(context.Background(), "http://primary.example.test/check")
	if err != nil {
		t.Fatalf("Check() 失败: %v", err)
	}
	if result.Status != StatusPortal || result.RedirectURL == nil {
		t.Fatalf("备用地址结果 = %+v", result)
	}
	if result.RedirectURL.String() != "http://gateway.example.test/login" {
		t.Fatalf("备用地址重定向 = %q", result.RedirectURL.String())
	}
	if len(calls) != 2 || calls[0] != "http://primary.example.test/check" || calls[1] != "http://gateway.example.test/" {
		t.Fatalf("探测请求顺序 = %v", calls)
	}
}
func TestProbeHonorsTimeout(t *testing.T) {
	probe := New()
	probe.Timeout = 10 * time.Millisecond
	probe.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	result, err := probe.Check(context.Background(), "http://example.test")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("超时错误 = %v", err)
	}
	var probeErr *ProbeError
	if !errors.As(err, &probeErr) || probeErr.Category != CategoryNetwork {
		t.Fatalf("错误 = %T %v", err, err)
	}
	if result.Status != StatusOffline {
		t.Fatalf("状态 = %q, want %q", result.Status, StatusOffline)
	}
}

func TestProbeValidatesRuntimeOptions(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Probe)
	}{
		{name: "timeout", setup: func(probe *Probe) { probe.Timeout = 0 }},
		{name: "response size", setup: func(probe *Probe) { probe.MaxResponseBytes = 0 }},
		{name: "marker", setup: func(probe *Probe) { probe.SuccessMarker = "" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			probe := New()
			testCase.setup(&probe)
			_, err := probe.Check(context.Background(), "http://example.test")
			var probeErr *ProbeError
			if err == nil || !errors.As(err, &probeErr) || probeErr.Category != CategoryConfiguration {
				t.Fatalf("错误 = %T %v", err, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
