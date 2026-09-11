package config

import (
	"strings"
	"testing"
)

func TestParseUCIConfig(t *testing.T) {
	data := []byte(`package giwifi-auto

config runtime 'main'
	option version '1'
	option connectivity_url 'http://connectivity.example.test/check'
	option portal_login_url 'http://portal.example.test/login'
	option check_interval_seconds '30'
	option request_timeout_seconds '10'
	option retry_initial_seconds '5'
	option retry_max_seconds '120'
	option max_concurrent_requests '2'
	option log_level 'info'
	option event_buffer_size '128'
	option control_socket '/var/run/giwifi-auto.sock'

config account 'primary'
	option display_name '主账号 #1'
	option username 'user@example.test'
	option credential_ref 'uci:giwifi-auto.primary.password'
	option enabled '1'
	option priority '10'
	option network_interface 'br-lan'
`)

	cfg, err := ParseUCI(data)
	if err != nil {
		t.Fatalf("ParseUCI() 失败: %v", err)
	}
	if cfg.Version != CurrentVersion || len(cfg.Accounts) != 1 {
		t.Fatalf("配置 = %+v", cfg)
	}
	account := cfg.Accounts[0]
	if account.ID != "primary" || !account.Enabled || account.DisplayName != "主账号 #1" || account.NetworkInterface != "br-lan" {
		t.Fatalf("账号 = %+v", account)
	}
}

func TestParseUCIAcceptsRawConfigWithoutPackageLine(t *testing.T) {
	data := []byte(`config runtime 'main'
 option version '1'
 option connectivity_url 'http://example.test'
 option check_interval_seconds '30'
 option request_timeout_seconds '10'
 option retry_initial_seconds '5'
 option retry_max_seconds '120'
 option max_concurrent_requests '1'
 option log_level 'info'
 option event_buffer_size '32'
 option control_socket '/tmp/giwifi-auto.sock'
`)
	if _, err := ParseUCI(data); err != nil {
		t.Fatalf("无 package 行的原始 UCI 配置未被接受: %v", err)
	}
}

func TestParseUCIHandlesEscapesAndComments(t *testing.T) {
	data := []byte(`package giwifi-auto
config runtime 'main'
 option version '1'
 option connectivity_url 'http://example.test/check'
 option check_interval_seconds '30' # inline comment
 option request_timeout_seconds '10'
 option retry_initial_seconds '5'
 option retry_max_seconds '120'
 option max_concurrent_requests '1'
 option log_level 'info'
 option event_buffer_size '32'
 option control_socket '/tmp/giwifi-auto.sock'
config account 'sample'
 option display_name 'quoted \'name\''
`)

	cfg, err := ParseUCI(data)
	if err != nil {
		t.Fatalf("ParseUCI() 失败: %v", err)
	}
	if cfg.Accounts[0].DisplayName != "quoted 'name'" {
		t.Fatalf("显示名称 = %q", cfg.Accounts[0].DisplayName)
	}
}

func TestParseUCIRejectsUnknownOptionsAndSections(t *testing.T) {
	base := `package giwifi-auto
config runtime 'main'
 option version '1'
 option connectivity_url 'http://example.test'
 option check_interval_seconds '30'
 option request_timeout_seconds '10'
 option retry_initial_seconds '5'
 option retry_max_seconds '120'
 option max_concurrent_requests '1'
 option log_level 'info'
 option event_buffer_size '32'
 option control_socket '/tmp/giwifi-auto.sock'
`
	for _, data := range []string{
		base + " option unknown 'value'\n",
		base + "config unsupported 'name'\n",
	} {
		if _, err := ParseUCI([]byte(data)); err == nil {
			t.Errorf("未拒绝无效 UCI: %s", data)
		}
	}
}

func TestParseUCIRejectsMismatchedAccountID(t *testing.T) {
	data := `package giwifi-auto
config runtime 'main'
 option version '1'
 option connectivity_url 'http://example.test'
 option check_interval_seconds '30'
 option request_timeout_seconds '10'
 option retry_initial_seconds '5'
 option retry_max_seconds '120'
 option max_concurrent_requests '1'
 option log_level 'info'
 option event_buffer_size '32'
 option control_socket '/tmp/giwifi-auto.sock'
config account 'section-name'
 option id 'different-id'
 option display_name '示例'
`
	_, err := ParseUCI([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "section") {
		t.Fatalf("错误 = %v, 应拒绝 section/id 不一致", err)
	}
}
