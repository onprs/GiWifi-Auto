package daemon

import "testing"

func TestParseInterfaceGatewayURL(t *testing.T) {
	output := []byte("default via 10.20.1.1 table 2 proto static src 10.20.2.126 metric 20\n")
	got, err := parseInterfaceGatewayURL(output)
	if err != nil {
		t.Fatalf("解析接口网关失败: %v", err)
	}
	if got != "http://10.20.1.1/" {
		t.Fatalf("接口网关 URL = %q", got)
	}
}

func TestParseInterfaceGatewayURLRejectsMissingGateway(t *testing.T) {
	if _, err := parseInterfaceGatewayURL([]byte("10.20.0.0/16 dev eth1.102 scope link\n")); err == nil {
		t.Fatal("缺少默认网关时应返回错误")
	}
}
