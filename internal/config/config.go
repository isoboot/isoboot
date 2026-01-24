package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"sigs.k8s.io/yaml"
)

type TargetConfig struct {
	ISO      string `json:"iso" yaml:"iso"`
	Firmware string `json:"firmware,omitempty" yaml:"firmware,omitempty"`
}

type Config struct {
	Targets map[string]TargetConfig `json:"targets" yaml:"targets"`
}

var DefaultConfig = Config{
	Targets: map[string]TargetConfig{
		"debian-13": {
			ISO:      "https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/netboot/mini.iso",
			Firmware: "https://cdimage.debian.org/cdimage/firmware/trixie/current/firmware.cpio.gz",
		},
	},
}

type ConfigWatcher struct {
	mu         sync.RWMutex
	config     Config
	configPath string
	stopCh     chan struct{}
}

func NewConfigWatcher(configPath string) (*ConfigWatcher, error) {
	cw := &ConfigWatcher{
		configPath: configPath,
		stopCh:     make(chan struct{}),
		config:     DefaultConfig,
	}

	if configPath != "" {
		if err := cw.reload(); err != nil {
			// Use default if file doesn't exist
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	return cw, nil
}

func (cw *ConfigWatcher) reload() error {
	data, err := os.ReadFile(cw.configPath)
	if err != nil {
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	cw.mu.Lock()
	cw.config = cfg
	cw.mu.Unlock()

	return nil
}

func (cw *ConfigWatcher) Start() {
	if cw.configPath == "" {
		return
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		var lastMod time.Time

		for {
			select {
			case <-cw.stopCh:
				return
			case <-ticker.C:
				info, err := os.Stat(cw.configPath)
				if err != nil {
					continue
				}
				if info.ModTime().After(lastMod) {
					lastMod = info.ModTime()
					if err := cw.reload(); err == nil {
						println("Config reloaded")
					}
				}
			}
		}
	}()
}

func (cw *ConfigWatcher) Stop() {
	close(cw.stopCh)
}

func (cw *ConfigWatcher) Get() Config {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return cw.config
}

func (cw *ConfigWatcher) GetTarget(name string) (TargetConfig, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	t, ok := cw.config.Targets[name]
	return t, ok
}

// ISOPath returns the path to the ISO file for a target
func ISOPath(basePath, target string) string {
	return filepath.Join(basePath, target, "mini.iso")
}

// FirmwarePath returns the path to the firmware file for a target
func FirmwarePath(basePath, target string) string {
	return filepath.Join(basePath, target, "firmware", "firmware.cpio.gz")
}

// InitrdOrigPath returns the path to the original initrd extracted from ISO
func InitrdOrigPath(basePath, target string) string {
	return filepath.Join(basePath, target, "initrd.gz.orig")
}
