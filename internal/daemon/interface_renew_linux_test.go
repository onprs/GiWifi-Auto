//go:build linux

package daemon

import "testing"

func TestMatchNetifdInterfacePrefersL3Device(t *testing.T) {
	dump := netifdDump{Interfaces: []netifdInterface{
		{Name: "wan", Device: "eth1", L3Device: "eth1.101"},
		{Name: "wan6", Device: "@wan", L3Device: "eth1.104"},
	}}
	if name, found := matchNetifdInterface(dump, "eth1.104"); !found || name != "wan6" {
		t.Fatalf("三层设备匹配 = %q %v, want wan6", name, found)
	}
	if name, found := matchNetifdInterface(dump, "eth1.101"); !found || name != "wan" {
		t.Fatalf("默认设备匹配 = %q %v, want wan", name, found)
	}
}

func TestMatchNetifdInterfaceFallsBackToConfiguredDevice(t *testing.T) {
	dump := netifdDump{Interfaces: []netifdInterface{
		{Name: "wan101", Device: "eth1.101"},
		{Name: "wan102", Device: "eth1.102", L3Device: ""},
	}}
	if name, found := matchNetifdInterface(dump, "eth1.102"); !found || name != "wan102" {
		t.Fatalf("配置设备回退匹配 = %q %v, want wan102", name, found)
	}
}

func TestMatchNetifdInterfaceRejectsMissingDevice(t *testing.T) {
	dump := netifdDump{Interfaces: []netifdInterface{{Name: "wan101", Device: "eth1.101", L3Device: "eth1.101"}}}
	if name, found := matchNetifdInterface(dump, "eth1.999"); found || name != "" {
		t.Fatalf("未知设备匹配 = %q %v", name, found)
	}
	if name, found := matchNetifdInterface(dump, "  "); found || name != "" {
		t.Fatalf("空设备匹配 = %q %v", name, found)
	}
}
