package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"sigs.k8s.io/yaml"
)

type TargetConfig struct {
	ISO          string `json:"iso" yaml:"iso"`
	Firmware     string `json:"firmware,omitempty" yaml:"firmware,omitempty"`
	DiskImageRef string `json:"diskImageRef,omitempty" yaml:"diskImageRef,omitempty"` // DiskImage name for file paths (defaults to target name)
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

// safePathSegment normalizes a value so it can be safely used as a single
// directory component in a path. It strips any leading path elements and
// rejects special values that could lead to directory traversal.
func safePathSegment(name string) string {
	base := filepath.Base(name)
	if base == "." || base == ".." {
		return ""
	}
	return base
}

// safeDiskImageDir returns a sanitized disk image directory name, falling back
// to "default" if the name is empty or invalid. This prevents path collapsing
// when used with filepath.Join.
func safeDiskImageDir(name string) string {
	if sanitized := safePathSegment(name); sanitized != "" {
		return sanitized
	}
	return "default"
}

// DiskImageName returns the DiskImage name to use for file paths.
// If DiskImageRef is set, use it; otherwise default to target name.
// The result is sanitized to prevent path traversal attacks and will never be empty.
func (t TargetConfig) DiskImageName(targetName string) string {
	if t.DiskImageRef != "" {
		if name := safePathSegment(t.DiskImageRef); name != "" {
			return name
		}
	}

	if name := safePathSegment(targetName); name != "" {
		return name
	}

	// Final fallback to avoid collapsing directory structure when both
	// DiskImageRef and targetName are invalid (e.g., "." or "..").
	return "default"
}

// ISOPathWithFilename returns the path to the ISO file with explicit filename.
// The diskImageName is sanitized to prevent path traversal and will never be empty.
func ISOPathWithFilename(basePath, diskImageName, filename string) string {
	return filepath.Join(basePath, safeDiskImageDir(diskImageName), safePathSegment(filename))
}

// FirmwarePath returns the path to the firmware file for a DiskImage.
// The diskImageName is sanitized to prevent path traversal and will never be empty.
func FirmwarePath(basePath, diskImageName string) string {
	return filepath.Join(basePath, safeDiskImageDir(diskImageName), "firmware", "firmware.cpio.gz")
}

// InitrdOrigPath returns the path to the original initrd extracted from ISO.
// The diskImageName is sanitized to prevent path traversal and will never be empty.
func InitrdOrigPath(basePath, diskImageName string) string {
	return filepath.Join(basePath, safeDiskImageDir(diskImageName), "initrd.gz.orig")
}
