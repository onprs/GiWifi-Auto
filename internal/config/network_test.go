package config

import (
	"reflect"
	"testing"
)

func TestParseDefaultRouteDevicesSortsAndDeduplicates(t *testing.T) {
	data := []byte("" +
		"10.20.0.0/16 dev uplink-a scope link\n" +
		"default via 10.20.0.1 dev uplink-b table 20 metric 20\n" +
		"default via 10.20.0.1 dev uplink-a table 10 metric 10\n" +
		"default via 10.20.0.1 dev uplink-a metric 30\n" +
		"default dev ppp0 metric 5\n" +
		"default via 192.0.2.1 metric 1\n")

	got := parseDefaultRouteDevices(data)
	want := []string{"ppp0", "uplink-a", "uplink-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("默认路由设备 = %#v, want %#v", got, want)
	}
}

func TestParseDefaultRouteDevicesSortsEqualMetricsByName(t *testing.T) {
	data := []byte("default via 192.0.2.1 dev uplink-z metric 10\n" +
		"default via 192.0.2.1 dev uplink-a metric 10\n")

	got := parseDefaultRouteDevices(data)
	want := []string{"uplink-a", "uplink-z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("相同 metric 的默认路由设备 = %#v, want %#v", got, want)
	}
}
