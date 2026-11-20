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

// ProfileMeta stores metadata for a saved subscription profile.
type ProfileMeta struct {
	URL       string    `yaml:"url,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// AppConfig represents ~/.config/manta/config.yaml.
type AppConfig struct {
	MixedPort      int                    `yaml:"mixed_port"`
	AllowLan       bool                   `yaml:"allow_lan"`
	Mode           string                 `yaml:"mode"`
	TunEnabled     bool                   `yaml:"tun_enabled,omitempty"`
	SortBySpeed    bool                   `yaml:"sort_by_speed,omitempty"`
	AutoTestOnOpen bool                   `yaml:"auto_test_on_open,omitempty"`
	StartupTab     string                 `yaml:"startup_tab,omitempty"` // "dashboard", "proxies", or "last"
	LastTab        string                 `yaml:"last_tab,omitempty"`
	LastGroup      string                 `yaml:"last_group,omitempty"`
	Controller     string                 `yaml:"controller"`
	Secret         string                 `yaml:"secret,omitempty"`
	CoreBin        string                 `yaml:"core_bin,omitempty"`
	ActiveProfile  string                 `yaml:"active_profile,omitempty"`
	Profiles       map[string]ProfileMeta `yaml:"profiles,omitempty"`
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

// FetchProfile downloads a Clash YAML subscription and validates it before saving.
func FetchProfile(p Paths, name, subURL string) error {
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, subURL, nil)
	if err != nil {
		return fmt.Errorf("invalid subscription URL: %w", err)
	}
	req.Header.Set("User-Agent", "clash-verge/v1.7.7 mihomo")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("subscription server returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // 20 MB cap
	if err != nil {
		return fmt.Errorf("read subscription body: %w", err)
	}

	var check map[string]any
	if err := yaml.Unmarshal(raw, &check); err != nil || len(check) == 0 {
		return fmt.Errorf("downloaded content is not a valid Clash YAML profile")
	}

	if err := os.WriteFile(p.ProfilePath(name), raw, 0o600); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	fixOwner(p.ProfilePath(name))
	return nil
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

	if cfg.TunEnabled {
		doc["tun"] = map[string]any{
			"enable":                true,
			"stack":                 "mixed",
			"auto-route":            true,
			"auto-detect-interface": true,
			"dns-hijack":            []string{"any:53", "tcp://any:53"},
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
		if _, exists := dnsMap["nameserver"]; !exists {
			dnsMap["nameserver"] = []string{"223.5.5.5", "119.29.29.29"}
		}
		if _, exists := dnsMap["fallback"]; !exists {
			dnsMap["fallback"] = []string{"https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query"}
		}
		doc["dns"] = dnsMap
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
