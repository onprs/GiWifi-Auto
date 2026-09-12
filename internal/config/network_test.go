package config

import (
	"reflect"
	"testing"
)

func TestParseWANDevices(t *testing.T) {
	data := []byte("" +
		"network.loopback=interface\n" +
		"network.loopback.proto='static'\n" +
		"network.lan=interface\n" +
		"network.lan.device='br-lan'\n" +
		"network.wan=interface\n" +
		"network.wan.proto='dhcp'\n" +
		"network.wan.device='eth1'\n" +
		"network.wan6=interface\n" +
		"network.wan6.proto='dhcpv6'\n" +
		"network.wan6.device='eth1'\n" +
		"network.wan101=interface\n" +
		"network.wan101.proto='dhcp'\n" +
		"network.wan101.device='eth1.101'\n" +
		"network.wan2=interface\n" +
		"network.wan2.proto='dhcp'\n" +
		"network.wan2.device='lan2'\n" +
		"network.wan3=interface\n" +
		"network.wan3.proto='dhcp'\n" +
		"network.wan3.device='lan2'\n")

	got, err := parseWANDevices(data)
	if err != nil {
		t.Fatalf("parseWANDevices() 失败: %v", err)
	}
	want := []string{"eth1", "eth1.101", "lan2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WAN 设备 = %#v, want %#v", got, want)
	}
}
