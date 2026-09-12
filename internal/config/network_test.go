package config

import (
	"reflect"
	"testing"
)

func TestParseWANDevices(t *testing.T) {
	data := []byte("" +
		"network.wan=interface\n" +
		"network.wan.proto='none'\n" +
		"network.wan.device='eth1'\n" +
		"network.wan6=interface\n" +
		"network.wan6.proto='dhcpv6'\n" +
		"network.wan101=interface\n" +
		"network.wan101.proto='dhcp'\n" +
		"network.wan101.device='eth1.101'\n" +
		"network.wan102=interface\n" +
		"network.wan102.proto='dhcp'\n" +
		"network.wan102.device='eth1.102'\n" +
		"network.wan103=interface\n" +
		"network.wan103.proto='dhcp'\n" +
		"network.wan103.device='eth1.103'\n" +
		"network.wan104=interface\n" +
		"network.wan104.proto='dhcp'\n" +
		"network.wan104.device='eth1.104'\n")

	got, err := parseWANDevices(data)
	if err != nil {
		t.Fatalf("parseWANDevices() 失败: %v", err)
	}
	want := []string{"eth1.101", "eth1.102", "eth1.103", "eth1.104"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WAN 设备 = %#v, want %#v", got, want)
	}
}
