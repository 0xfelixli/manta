package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	proxyTransportMu sync.Mutex
	proxyTransports  = make(map[int]*http.Transport)
)

func getProxyTransport(mixedPort int) (*http.Transport, error) {
	proxyTransportMu.Lock()
	defer proxyTransportMu.Unlock()
	if tr, ok := proxyTransports[mixedPort]; ok {
		return tr, nil
	}
	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", mixedPort))
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
	}
	proxyTransports[mixedPort] = tr
	return tr, nil
}

// Client talks to mihomo's External Controller REST API.
type Client struct {
	BaseURL string
	Secret  string
	HTTP    *http.Client
}

// VersionResp is returned by GET /version.
type VersionResp struct {
	Version string `json:"version"`
	Meta    bool   `json:"meta"`
}

// ConfigsResp is returned by GET /configs.
type ConfigsResp struct {
	MixedPort int    `json:"mixed-port"`
	AllowLan  bool   `json:"allow-lan"`
	Mode      string `json:"mode"`
	LogLevel  string `json:"log-level"`
	Tun       struct {
		Enable bool   `json:"enable"`
		Stack  string `json:"stack"`
	} `json:"tun"`
}

// ConnectionItem represents a single active connection from GET /connections.
type ConnectionItem struct {
	ID       string   `json:"id"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Chains   []string `json:"chains"`
	Rule     string   `json:"rule"`
	Metadata struct {
		Network         string `json:"network"`
		Host            string `json:"host"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
	} `json:"metadata"`
}

// Target returns host:port (falling back to destinationIP:port).
func (c ConnectionItem) Target() string {
	host := c.Metadata.Host
	if host == "" {
		host = c.Metadata.DestinationIP
	}
	if c.Metadata.DestinationPort != "" {
		return host + ":" + c.Metadata.DestinationPort
	}
	return host
}

// Outbound returns the final exit proxy/direct name for this connection.
func (c ConnectionItem) Outbound() string {
	if len(c.Chains) == 0 {
		return "-"
	}
	return c.Chains[0]
}

// ConnectionsSummary summarizes GET /connections.
type ConnectionsSummary struct {
	UploadTotal   int64            `json:"uploadTotal"`
	DownloadTotal int64            `json:"downloadTotal"`
	Connections   []ConnectionItem `json:"connections"`
}

// ExitIPInfo describes the current outbound IP and geo location through the proxy.
type ExitIPInfo struct {
	IP          string
	Country     string
	CountryCode string
	Region      string
	City        string
	ISP         string
}

// ProxyInfo describes a single proxy or proxy group from GET /proxies.
type ProxyInfo struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Now     string   `json:"now,omitempty"`
	All     []string `json:"all,omitempty"`
	History []struct {
		Delay int `json:"delay"`
	} `json:"history,omitempty"`
}

type proxiesResp struct {
	Proxies map[string]ProxyInfo `json:"proxies"`
}

type delayResp struct {
	Delay int `json:"delay"`
}

// TrafficSample represents a single 1-second bandwidth snapshot from GET /traffic.
type TrafficSample struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// NewClient constructs a controller API client from host:port and optional secret.
func NewClient(controller, secret string) *Client {
	base := strings.TrimSpace(controller)
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	base = strings.TrimRight(base, "/")
	tr := &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		BaseURL: base,
		Secret:  secret,
		HTTP: &http.Client{
			Timeout:   8 * time.Second,
			Transport: tr,
		},
	}
}

func (c *Client) doJSON(method, endpoint string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.BaseURL+endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("controller %s %s failed (%d): %s", method, endpoint, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Version checks controller health and returns the mihomo version.
func (c *Client) Version() (*VersionResp, error) {
	var v VersionResp
	if err := c.doJSON(http.MethodGet, "/version", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// GetConfigs fetches the active runtime configuration from mihomo.
func (c *Client) GetConfigs() (*ConfigsResp, error) {
	var cfg ConfigsResp
	if err := c.doJSON(http.MethodGet, "/configs", nil, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetConnections fetches total traffic counters and active connection count.
func (c *Client) GetConnections() (*ConnectionsSummary, error) {
	var res ConnectionsSummary
	if err := c.doJSON(http.MethodGet, "/connections", nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// FetchExitIP queries the current exit IP & geolocation via manta's local HTTP proxy
// using concurrent Happy Eyeballs racing across multiple GeoIP endpoints and a pooled Transport.
func FetchExitIP(mixedPort int) (*ExitIPInfo, error) {
	tr, err := getProxyTransport(mixedPort)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: tr,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type res struct {
		info *ExitIPInfo
		err  error
	}
	ch := make(chan res, 2)

	// Endpoint 1: ip-api.com (fast JSON)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ip-api.com/json/?fields=status,country,countryCode,regionName,city,isp,query", nil)
		if err != nil {
			ch <- res{err: err}
			return
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			ch <- res{err: err}
			return
		}
		defer resp.Body.Close()
		var r struct {
			Status      string `json:"status"`
			Country     string `json:"country"`
			CountryCode string `json:"countryCode"`
			RegionName  string `json:"regionName"`
			City        string `json:"city"`
			ISP         string `json:"isp"`
			Query       string `json:"query"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Status != "success" || r.Query == "" {
			ch <- res{err: fmt.Errorf("ip-api invalid response")}
			return
		}
		ch <- res{info: &ExitIPInfo{
			IP:          r.Query,
			Country:     r.Country,
			CountryCode: r.CountryCode,
			Region:      r.RegionName,
			City:        r.City,
			ISP:         r.ISP,
		}}
	}()

	// Endpoint 2: ipwho.is (HTTPS fallback, 150ms staggered Happy Eyeballs start)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/", nil)
		if err != nil {
			ch <- res{err: err}
			return
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			ch <- res{err: err}
			return
		}
		defer resp.Body.Close()
		var fb struct {
			Success     bool   `json:"success"`
			IP          string `json:"ip"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
			Region      string `json:"region"`
			City        string `json:"city"`
			Connection  struct {
				ISP string `json:"isp"`
			} `json:"connection"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&fb); err != nil || !fb.Success || fb.IP == "" {
			ch <- res{err: fmt.Errorf("ipwho.is invalid response")}
			return
		}
		ch <- res{info: &ExitIPInfo{
			IP:          fb.IP,
			Country:     fb.Country,
			CountryCode: fb.CountryCode,
			Region:      fb.Region,
			City:        fb.City,
			ISP:         fb.Connection.ISP,
		}}
	}()

	var lastErr error
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.info != nil {
			cancel()
			return r.info, nil
		}
		if r.err != nil {
			lastErr = r.err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("exit IP lookup failed")
	}
	return nil, lastErr
}

// SetMode switches between rule, global, and direct modes without restarting.
func (c *Client) SetMode(mode string) error {
	return c.doJSON(http.MethodPatch, "/configs", map[string]string{"mode": mode}, nil)
}

// SetTun hot-toggles mihomo's TUN virtual interface (requires root-started mihomo).
func (c *Client) SetTun(enable bool) error {
	if enable {
		return c.doJSON(http.MethodPatch, "/configs", map[string]any{
			"tun": map[string]any{
				"enable":                true,
				"stack":                 "mixed",
				"auto-route":            true,
				"auto-detect-interface": true,
				"dns-hijack":            []string{"any:53", "tcp://any:53"},
				"route-exclude-address": []string{
					"100.64.0.0/10",
					"192.168.1.0/24",
					"fd7a:115c:a1e0::/48",
				},
			},
		}, nil)
	}
	return c.doJSON(http.MethodPatch, "/configs", map[string]any{
		"tun": map[string]any{"enable": false},
	}, nil)
}

// SetAllowLan hot-toggles LAN proxy sharing.
func (c *Client) SetAllowLan(allow bool) error {
	return c.doJSON(http.MethodPatch, "/configs", map[string]any{
		"allow-lan": allow,
	}, nil)
}

// ReloadConfig tells mihomo to hot-reload the runtime YAML file without restarting.
func (c *Client) ReloadConfig(path string) error {
	return c.doJSON(http.MethodPut, "/configs?force=true", map[string]string{
		"path": path,
	}, nil)
}

// CloseAllConnections terminates all currently active connections in mihomo.
func (c *Client) CloseAllConnections() error {
	return c.doJSON(http.MethodDelete, "/connections", nil, nil)
}

// FlushFakeIP clears mihomo's Fake-IP DNS cache.
func (c *Client) FlushFakeIP() error {
	return c.doJSON(http.MethodPost, "/cache/fakeip/flush", nil, nil)
}

// GetProxies returns all proxies and proxy groups.
func (c *Client) GetProxies() (map[string]ProxyInfo, error) {
	var res proxiesResp
	if err := c.doJSON(http.MethodGet, "/proxies", nil, &res); err != nil {
		return nil, err
	}
	return res.Proxies, nil
}

// SelectProxy sets the active node for a Selector proxy group.
func (c *Client) SelectProxy(group, node string) error {
	endpoint := "/proxies/" + url.PathEscape(group)
	return c.doJSON(http.MethodPut, endpoint, map[string]string{"name": node}, nil)
}

// TestDelay measures round-trip latency (ms) for a proxy via a test URL.
func (c *Client) TestDelay(name, testURL string, timeoutMs int) (int, error) {
	endpoint := fmt.Sprintf(
		"/proxies/%s/delay?url=%s&timeout=%d",
		url.PathEscape(name),
		url.QueryEscape(testURL),
		timeoutMs,
	)
	var res delayResp
	if err := c.doJSON(http.MethodGet, endpoint, nil, &res); err != nil {
		return 0, err
	}
	return res.Delay, nil
}

// StreamTraffic subscribes to mihomo's newline-delimited JSON /traffic stream.
// It closes the returned channel when ctx is cancelled or the stream ends.
func (c *Client) StreamTraffic(ctx context.Context) (<-chan TrafficSample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/traffic", nil)
	if err != nil {
		return nil, err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}

	// Streaming requests must not use the short request timeout.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("controller GET /traffic failed (%d)", resp.StatusCode)
	}

	ch := make(chan TrafficSample, 4)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var sample TrafficSample
			if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
				continue
			}
			select {
			case ch <- sample:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
