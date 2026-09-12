package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/config"
)

// InterfaceStatus 描述账号绑定接口的当前链路和地址状态。
type InterfaceStatus struct {
	Name          string   `json:"name"`
	Present       bool     `json:"present"`
	AdminUp       bool     `json:"admin_up"`
	Carrier       bool     `json:"carrier"`
	OperState     string   `json:"oper_state"`
	IPv4Addresses []string `json:"ipv4_addresses,omitempty"`
}

func (service *Service) statusResponse(ctx context.Context) StatusResponse {
	service.mu.RLock()
	accounts := make([]account.Snapshot, 0, len(service.order))
	interfaces := make([]string, 0, len(service.config.Accounts))
	seenInterfaces := make(map[string]struct{}, len(service.config.Accounts))
	for _, id := range service.order {
		if runner, exists := service.runners[id]; exists {
			accounts = append(accounts, runner.Snapshot())
		}
	}
	for _, accountConfig := range service.config.Accounts {
		if accountConfig.NetworkInterface == "" {
			continue
		}
		if _, exists := seenInterfaces[accountConfig.NetworkInterface]; exists {
			continue
		}
		seenInterfaces[accountConfig.NetworkInterface] = struct{}{}
		interfaces = append(interfaces, accountConfig.NetworkInterface)
	}
	service.mu.RUnlock()
	if ctx != nil {
		if wanDevices, err := config.DiscoverWANDevices(ctx); err == nil {
			for _, device := range wanDevices {
				if _, exists := seenInterfaces[device]; exists {
					continue
				}
				seenInterfaces[device] = struct{}{}
				interfaces = append(interfaces, device)
			}
		}
	}
	return StatusResponse{
		Accounts:   accounts,
		Interfaces: inspectInterfaceStatuses(interfaces),
	}
}

func inspectInterfaceStatuses(names []string) []InterfaceStatus {
	allInterfaces, err := net.Interfaces()
	if err != nil {
		allInterfaces = nil
	}
	byName := make(map[string]net.Interface, len(allInterfaces))
	for _, networkInterface := range allInterfaces {
		byName[networkInterface.Name] = networkInterface
	}

	statuses := make([]InterfaceStatus, 0, len(names))
	for _, name := range names {
		status := InterfaceStatus{Name: name, OperState: "not_found"}
		networkInterface, exists := byName[name]
		if !exists {
			statuses = append(statuses, status)
			continue
		}
		status.Present = true
		status.AdminUp = networkInterface.Flags&net.FlagUp != 0
		status.Carrier = networkInterface.Flags&net.FlagRunning != 0
		status.OperState = readInterfaceValue(name, "operstate")
		if carrier, err := strconv.Atoi(readInterfaceValue(name, "carrier")); err == nil {
			status.Carrier = carrier == 1
		}
		if status.OperState == "" {
			switch {
			case !status.AdminUp:
				status.OperState = "down"
			case status.Carrier:
				status.OperState = "up"
			default:
				status.OperState = "no_carrier"
			}
		}
		for _, address := range interfaceAddresses(networkInterface) {
			status.IPv4Addresses = append(status.IPv4Addresses, address)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func interfaceAddresses(networkInterface net.Interface) []string {
	addresses, err := networkInterface.Addrs()
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		value := address.String()
		if ip, _, err := net.ParseCIDR(value); err == nil && ip.To4() != nil {
			result = append(result, ip.To4().String())
		}
	}
	return result
}

func readInterfaceValue(name, value string) string {
	if strings.ContainsAny(name, `/\\`) || strings.ContainsAny(value, `/\\`) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join("/sys/class/net", name, value))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
