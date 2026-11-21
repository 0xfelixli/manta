package sysproxy

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	servicesCacheMu   sync.Mutex
	cachedServices    []string
	servicesCacheTime time.Time
)

var defaultBypassDomains = []string{
	"127.0.0.1",
	"localhost",
	"*.local",
	"169.254/16",
	"192.168.0.0/16",
	"10.0.0.0/8",
	"172.16.0.0/12",
}

// ServiceStatus describes the current macOS system proxy state for one network service.
type ServiceStatus struct {
	Service string
	HTTP    string
	HTTPS   string
	SOCKS   string
}

func ensureDarwin() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("system proxy management is currently only supported on macOS (got %s)", runtime.GOOS)
	}
	return nil
}

func runNetworkSetup(args ...string) (string, error) {
	cmd := exec.Command("networksetup", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("networksetup %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// FastIsEnabled queries macOS `scutil --proxy` in a single lightweight Mach IPC call (~2ms)
// instead of spawning 8-12 `networksetup` subprocesses across all network interfaces.
func FastIsEnabled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	out, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "HTTPEnable :") ||
			strings.HasPrefix(line, "HTTPSEnable :") ||
			strings.HasPrefix(line, "SOCKSEnable :") {
			if strings.HasSuffix(line, " 1") {
				return true
			}
		}
	}
	return false
}

// ActiveServices returns enabled macOS network services that currently have an IP address.
// Falls back to "Wi-Fi" if present when no active IP is detected.
func ActiveServices() ([]string, error) {
	if err := ensureDarwin(); err != nil {
		return nil, err
	}
	servicesCacheMu.Lock()
	if len(cachedServices) > 0 && time.Since(servicesCacheTime) < 30*time.Second {
		res := make([]string, len(cachedServices))
		copy(res, cachedServices)
		servicesCacheMu.Unlock()
		return res, nil
	}
	servicesCacheMu.Unlock()

	out, err := runNetworkSetup("-listallnetworkservices")
	if err != nil {
		return nil, err
	}

	var all []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "An asterisk") || strings.HasPrefix(line, "*") {
			continue
		}
		all = append(all, line)
	}

	var active []string
	for _, svc := range all {
		info, err := runNetworkSetup("-getinfo", svc)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(info, "\n") {
			if strings.HasPrefix(line, "IP address:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "IP address:"))
				if val != "" && !strings.EqualFold(val, "none") {
					active = append(active, svc)
				}
				break
			}
		}
	}

	var chosen []string
	if len(active) > 0 {
		chosen = active
	} else {
		for _, svc := range all {
			if svc == "Wi-Fi" {
				chosen = []string{"Wi-Fi"}
				break
			}
		}
	}
	if len(chosen) == 0 {
		if len(all) == 0 {
			return nil, errors.New("no enabled macOS network services found")
		}
		chosen = all[:1]
	}

	servicesCacheMu.Lock()
	cachedServices = make([]string, len(chosen))
	copy(cachedServices, chosen)
	servicesCacheTime = time.Now()
	servicesCacheMu.Unlock()

	return chosen, nil
}

// ResolveServices returns the user-specified service or auto-detected active services.
func ResolveServices(service string) ([]string, error) {
	if err := ensureDarwin(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(service) != "" {
		return []string{strings.TrimSpace(service)}, nil
	}
	return ActiveServices()
}

// Enable configures HTTP, HTTPS, and SOCKS5 system proxies on the given macOS network services.
func Enable(host string, port int, services []string) error {
	if err := ensureDarwin(); err != nil {
		return err
	}
	portStr := strconv.Itoa(port)
	for _, svc := range services {
		cmds := [][]string{
			{"-setwebproxy", svc, host, portStr},
			{"-setsecurewebproxy", svc, host, portStr},
			{"-setsocksfirewallproxy", svc, host, portStr},
			{"-setwebproxystate", svc, "on"},
			{"-setsecurewebproxystate", svc, "on"},
			{"-setsocksfirewallproxystate", svc, "on"},
			append([]string{"-setproxybypassdomains", svc}, defaultBypassDomains...),
		}
		for _, args := range cmds {
			if _, err := runNetworkSetup(args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// Disable turns off HTTP, HTTPS, and SOCKS5 system proxies on the given macOS network services.
func Disable(services []string) error {
	if err := ensureDarwin(); err != nil {
		return err
	}
	for _, svc := range services {
		cmds := [][]string{
			{"-setwebproxystate", svc, "off"},
			{"-setsecurewebproxystate", svc, "off"},
			{"-setsocksfirewallproxystate", svc, "off"},
		}
		for _, args := range cmds {
			if _, err := runNetworkSetup(args...); err != nil {
				return err
			}
		}
	}
	return nil
}

// Status inspects HTTP, HTTPS, and SOCKS5 proxy settings for the given macOS network services.
func Status(services []string) ([]ServiceStatus, error) {
	if err := ensureDarwin(); err != nil {
		return nil, err
	}
	results := make([]ServiceStatus, 0, len(services))
	for _, svc := range services {
		st := ServiceStatus{
			Service: svc,
			HTTP:    parseProxySummary("-getwebproxy", svc),
			HTTPS:   parseProxySummary("-getsecurewebproxy", svc),
			SOCKS:   parseProxySummary("-getsocksfirewallproxy", svc),
		}
		results = append(results, st)
	}
	return results, nil
}

func parseProxySummary(flag, svc string) string {
	out, err := runNetworkSetup(flag, svc)
	if err != nil {
		return "unknown"
	}
	var enabled, server, port string
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		switch k {
		case "Enabled":
			enabled = v
		case "Server":
			server = v
		case "Port":
			port = v
		}
	}
	if !strings.EqualFold(enabled, "Yes") {
		return "off"
	}
	return fmt.Sprintf("on (%s:%s)", server, port)
}
