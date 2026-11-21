package config

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Paths holds all filesystem locations managed by manta.
type Paths struct {
	RootDir        string
	ConfigFile     string
	ProfilesDir    string
	RuntimeFile    string
	PIDFile        string
	MenubarPIDFile string
	ShutdownFile   string
	LogFile        string
}

// ProfileMeta stores metadata and subscription traffic quota for a saved profile.
type ProfileMeta struct {
	URL       string    `yaml:"url,omitempty" json:"url,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
	Upload    int64     `yaml:"upload,omitempty" json:"upload,omitempty"`
	Download  int64     `yaml:"download,omitempty" json:"download,omitempty"`
	Total     int64     `yaml:"total,omitempty" json:"total,omitempty"`
	Expire    int64     `yaml:"expire,omitempty" json:"expire,omitempty"`
}

// QuotaSummary returns a human-readable traffic & expiration summary if available.
func (m ProfileMeta) QuotaSummary() string {
	if m.Total <= 0 && m.Expire <= 0 {
		return ""
	}
	var parts []string
	if m.Total > 0 {
		used := m.Upload + m.Download
		pct := int(used * 100 / m.Total)
		parts = append(parts, fmt.Sprintf("%s / %s (%d%%)", formatGib(used), formatGib(m.Total), pct))
	}
	if m.Expire > 0 {
		exp := time.Unix(m.Expire, 0).Format("2006-01-02")
		parts = append(parts, "exp "+exp)
	}
	return strings.Join(parts, " • ")
}

func formatGib(b int64) string {
	const gb = 1 << 30
	const mb = 1 << 20
	if b >= gb {
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	}
	return fmt.Sprintf("%.0f MB", float64(b)/float64(mb))
}

// AppConfig represents ~/.config/manta/config.yaml.
type AppConfig struct {
	MixedPort      int                    `yaml:"mixed_port" json:"mixed_port"`
	AllowLan       bool                   `yaml:"allow_lan" json:"allow_lan"`
	Mode           string                 `yaml:"mode" json:"mode"`
	TunEnabled     bool                   `yaml:"tun_enabled,omitempty" json:"tun_enabled"`
	SortBySpeed    bool                   `yaml:"sort_by_speed,omitempty" json:"sort_by_speed"`
	AutoTestOnOpen bool                   `yaml:"auto_test_on_open,omitempty" json:"auto_test_on_open"`
	StartupTab     string                 `yaml:"startup_tab,omitempty" json:"startup_tab,omitempty"` // "dashboard", "proxies", or "last"
	LastTab        string                 `yaml:"last_tab,omitempty" json:"last_tab,omitempty"`
	LastGroup      string                 `yaml:"last_group,omitempty" json:"last_group,omitempty"`
	Controller     string                 `yaml:"controller" json:"controller"`
	Secret         string                 `yaml:"secret,omitempty" json:"-"`
	CoreBin        string                 `yaml:"core_bin,omitempty" json:"core_bin,omitempty"`
	ActiveProfile  string                 `yaml:"active_profile,omitempty" json:"active_profile,omitempty"`
	CustomRules    []string               `yaml:"custom_rules,omitempty" json:"custom_rules,omitempty"`
	Profiles       map[string]ProfileMeta `yaml:"profiles,omitempty" json:"profiles,omitempty"`
}

// DefaultPaths returns the default ~/.config/manta directory layout.
// When invoked via sudo, it resolves the invoking SUDO_USER's home directory
// so root TUN runs share the same config and profiles as the regular user.
func DefaultPaths() (Paths, error) {
	home, err := resolveUserHome()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	root := filepath.Join(home, ".config", "manta")
	return Paths{
		RootDir:        root,
		ConfigFile:     filepath.Join(root, "config.yaml"),
		ProfilesDir:    filepath.Join(root, "profiles"),
		RuntimeFile:    filepath.Join(root, "runtime.yaml"),
		PIDFile:        filepath.Join(root, "manta.pid"),
		MenubarPIDFile: filepath.Join(root, "menubar.pid"),
		ShutdownFile:   filepath.Join(root, "manta.shutdown"),
		LogFile:        filepath.Join(root, "mihomo.log"),
	}, nil
}

func resolveUserHome() (string, error) {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil && u.HomeDir != "" {
			return u.HomeDir, nil
		}
	}
	if uid := os.Getuid(); uid != 0 {
		if u, err := user.LookupId(strconv.Itoa(uid)); err == nil && u.HomeDir != "" {
			return u.HomeDir, nil
		}
	}
	return os.UserHomeDir()
}

func fixOwner(path string) {
	if os.Geteuid() != 0 {
		return
	}
	if uid := os.Getuid(); uid != 0 {
		_ = os.Chown(path, uid, os.Getgid())
		return
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if u, err := user.Lookup(sudoUser); err == nil {
			uid, err1 := strconv.Atoi(u.Uid)
			gid, err2 := strconv.Atoi(u.Gid)
			if err1 == nil && err2 == nil {
				_ = os.Chown(path, uid, gid)
			}
		}
	}
}

// DefaultConfig returns sensible defaults for a local HTTP/SOCKS5 proxy setup.
func DefaultConfig() *AppConfig {
	return &AppConfig{
		MixedPort:  7890,
		AllowLan:   false,
		Mode:       "rule",
		Controller: "127.0.0.1:9090",
		Profiles:   make(map[string]ProfileMeta),
	}
}

// EnsureDirs creates all required directories if they do not exist.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.RootDir, p.ProfilesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
		fixOwner(dir)
	}
	return nil
}

// ProfilePath returns the file path for a named profile.
func (p Paths) ProfilePath(name string) string {
	return filepath.Join(p.ProfilesDir, name+".yaml")
}

// Load reads config.yaml, returning defaults if it does not exist yet.
func Load(p Paths) (*AppConfig, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.MixedPort == 0 {
		cfg.MixedPort = 7890
	}
	if cfg.Controller == "" {
		cfg.Controller = "127.0.0.1:9090"
	}
	if cfg.Mode == "" {
		cfg.Mode = "rule"
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]ProfileMeta)
	}
	return cfg, nil
}

// Save writes config.yaml to disk.
func Save(p Paths, cfg *AppConfig) error {
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(p.ConfigFile, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fixOwner(p.ConfigFile)
	return nil
}

// FetchProfile downloads a Clash YAML subscription, validates it, and extracts Subscription-Userinfo quota headers.
func FetchProfile(p Paths, name, subURL string) (ProfileMeta, error) {
	meta := ProfileMeta{
		URL:       subURL,
		UpdatedAt: time.Now(),
	}
	if err := p.EnsureDirs(); err != nil {
		return meta, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, subURL, nil)
	if err != nil {
		return meta, fmt.Errorf("invalid subscription URL: %w", err)
	}
	req.Header.Set("User-Agent", "clash-verge/v1.7.7 mihomo")

	resp, err := client.Do(req)
	if err != nil {
		return meta, fmt.Errorf("download subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return meta, fmt.Errorf("subscription server returned HTTP %d", resp.StatusCode)
	}

	parseSubscriptionUserinfo(resp.Header.Get("Subscription-Userinfo"), &meta)

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // 20 MB cap
	if err != nil {
		return meta, fmt.Errorf("read subscription body: %w", err)
	}

	var check map[string]any
	if err := yaml.Unmarshal(raw, &check); err != nil || len(check) == 0 {
		return meta, fmt.Errorf("downloaded content is not a valid Clash YAML profile")
	}

	if err := os.WriteFile(p.ProfilePath(name), raw, 0o600); err != nil {
		return meta, fmt.Errorf("write profile: %w", err)
	}
	fixOwner(p.ProfilePath(name))
	return meta, nil
}

func parseSubscriptionUserinfo(header string, meta *ProfileMeta) {
	if header == "" || meta == nil {
		return
	}
	for _, part := range strings.Split(header, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		val, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "upload":
			meta.Upload = val
		case "download":
			meta.Download = val
		case "total":
			meta.Total = val
		case "expire":
			meta.Expire = val
		}
	}
}

// BuildRuntimeConfig merges manta's local inbound/controller settings into the active profile.
func BuildRuntimeConfig(p Paths, cfg *AppConfig) error {
	if err := p.EnsureDirs(); err != nil {
		return err
	}

	doc := make(map[string]any)
	if cfg.ActiveProfile != "" {
		raw, err := os.ReadFile(p.ProfilePath(cfg.ActiveProfile))
		if err != nil {
			return fmt.Errorf("read active profile %q: %w", cfg.ActiveProfile, err)
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parse active profile %q: %w", cfg.ActiveProfile, err)
		}
	} else {
		// Minimal fallback config so `manta up` works even before adding a subscription.
		doc["proxies"] = []any{}
		doc["proxy-groups"] = []any{}
		doc["rules"] = []string{"MATCH,DIRECT"}
	}

	// Enforce local HTTP + SOCKS5 mixed inbound and controller settings.
	delete(doc, "port")
	delete(doc, "socks-port")
	doc["mixed-port"] = cfg.MixedPort
	doc["allow-lan"] = cfg.AllowLan
	doc["mode"] = cfg.Mode
	doc["external-controller"] = cfg.Controller
	if cfg.Secret != "" {
		doc["secret"] = cfg.Secret
	} else {
		delete(doc, "secret")
	}

	// High-performance mihomo data-plane defaults:
	// - log-level: warning (eliminates per-connection disk I/O & string formatting overhead)
	// - tcp-concurrent: dial all resolved IPs concurrently and pick the lowest-latency handshake
	// - unified-delay: normalize latency measurement across protocols
	// - profile.store-fake-ip & store-selected: persist Fake-IP mapping and node selection across restarts
	doc["log-level"] = "warning"
	if _, exists := doc["tcp-concurrent"]; !exists {
		doc["tcp-concurrent"] = true
	}
	if _, exists := doc["unified-delay"]; !exists {
		doc["unified-delay"] = true
	}
	profileMap, ok := doc["profile"].(map[string]any)
	if !ok || profileMap == nil {
		profileMap = make(map[string]any)
	}
	if _, exists := profileMap["store-selected"]; !exists {
		profileMap["store-selected"] = true
	}
	if _, exists := profileMap["store-fake-ip"]; !exists {
		profileMap["store-fake-ip"] = true
	}
	doc["profile"] = profileMap

	// Prepend user custom override rules ahead of subscription rules.
	if len(cfg.CustomRules) > 0 {
		mergedRules := make([]any, 0, len(cfg.CustomRules))
		for _, r := range cfg.CustomRules {
			mergedRules = append(mergedRules, r)
		}
		if existing, ok := doc["rules"].([]any); ok {
			mergedRules = append(mergedRules, existing...)
		} else if existingStr, ok := doc["rules"].([]string); ok {
			for _, r := range existingStr {
				mergedRules = append(mergedRules, r)
			}
		}
		doc["rules"] = mergedRules
	}

	// Disable per-connection macOS proc_pidinfo syscall lookup when no PROCESS-NAME/PROCESS-PATH rules exist.
	if _, exists := doc["find-process-mode"]; !exists {
		if !hasProcessRules(doc["rules"]) {
			doc["find-process-mode"] = "off"
		}
	}

	if cfg.TunEnabled {
		doc["tun"] = map[string]any{
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
		}
		dnsMap, ok := doc["dns"].(map[string]any)
		if !ok || dnsMap == nil {
			dnsMap = make(map[string]any)
		}
		dnsMap["enable"] = true
		if _, exists := dnsMap["listen"]; !exists {
			dnsMap["listen"] = "0.0.0.0:1053"
		}
		dnsMap["enhanced-mode"] = "fake-ip"
		if _, exists := dnsMap["fake-ip-range"]; !exists {
			dnsMap["fake-ip-range"] = "198.18.0.1/16"
		}
		if _, exists := dnsMap["fake-ip-filter"]; !exists {
			dnsMap["fake-ip-filter"] = []string{
				"*.lan",
				"*.local",
				"localhost.ptlogin2.qq.com",
				"+.srv.nintendo.net",
				"+.stun.playstation.net",
				"xbox.*.microsoft.com",
				"+.xboxlive.com",
				"*.msftconnecttest.com",
				"*.msftncsi.com",
				"time.*.com",
				"time.*.gov",
				"time.*.apple.com",
				"ntp.*.com",
				"stun.*.*",
				"stun.*.*.*",
				"+.tailscale.com",
				"+.tailscale.io",
				"+.ts.net",
			}
		} else {
			dnsMap["fake-ip-filter"] = mergeFakeIPFilter(dnsMap["fake-ip-filter"],
				"+.tailscale.com",
				"+.tailscale.io",
				"+.ts.net",
			)
		}
		if _, exists := dnsMap["default-nameserver"]; !exists {
			dnsMap["default-nameserver"] = []string{"223.5.5.5", "119.29.29.29"}
		}
		if _, exists := dnsMap["nameserver"]; !exists {
			dnsMap["nameserver"] = []string{"https://223.5.5.5/dns-query", "https://1.12.12.12/dns-query", "223.5.5.5"}
		}
		doc["dns"] = dnsMap

		if _, exists := doc["sniffer"]; !exists {
			doc["sniffer"] = map[string]any{
				"enable":               true,
				"force-dns-mapping":    true,
				"parse-pure-ip":        true,
				"override-destination": true,
				"sniff": map[string]any{
					"HTTP": map[string]any{
						"ports":                []string{"80", "8080-8880"},
						"override-destination": true,
					},
					"TLS": map[string]any{
						"ports": []string{"443", "8443"},
					},
					"QUIC": map[string]any{
						"ports": []string{"443", "8443"},
					},
				},
			}
		}
	} else {
		delete(doc, "tun")
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal runtime config: %w", err)
	}
	if err := os.WriteFile(p.RuntimeFile, out, 0o644); err != nil {
		return fmt.Errorf("write runtime config: %w", err)
	}
	fixOwner(p.RuntimeFile)
	return nil
}

func hasProcessRules(rulesAny any) bool {
	checkRule := func(r string) bool {
		u := strings.ToUpper(strings.TrimSpace(r))
		return strings.HasPrefix(u, "PROCESS-NAME,") || strings.HasPrefix(u, "PROCESS-PATH,")
	}
	switch rules := rulesAny.(type) {
	case []any:
		for _, item := range rules {
			if s, ok := item.(string); ok && checkRule(s) {
				return true
			}
		}
	case []string:
		for _, s := range rules {
			if checkRule(s) {
				return true
			}
		}
	}
	return false
}

func mergeFakeIPFilter(existing any, additions ...string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	switch v := existing.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range v {
			add(s)
		}
	}
	for _, s := range additions {
		add(s)
	}
	return out
}
