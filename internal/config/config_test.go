package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewConfigWatcher_DefaultConfig(t *testing.T) {
	cw, err := NewConfigWatcher("")
	if err != nil {
		t.Fatalf("Failed to create config watcher: %v", err)
	}

	cfg := cw.Get()
	if len(cfg.Targets) == 0 {
		t.Error("Expected default targets")
	}

	target, ok := cfg.Targets["debian-13"]
	if !ok {
		t.Error("Expected debian-13 target in defaults")
	}

	if target.ISO == "" {
		t.Error("Expected ISO URL in debian-13 target")
	}
}

func TestNewConfigWatcher_LoadFromFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `targets:
  ubuntu-24:
    iso: "https://example.com/ubuntu.iso"
    firmware: "https://example.com/firmware.cpio.gz"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	cw, err := NewConfigWatcher(configPath)
	if err != nil {
		t.Fatalf("Failed to create config watcher: %v", err)
	}

	cfg := cw.Get()
	target, ok := cfg.Targets["ubuntu-24"]
	if !ok {
		t.Error("Expected ubuntu-24 target from config file")
	}

	if target.ISO != "https://example.com/ubuntu.iso" {
		t.Errorf("Expected ISO URL from config, got %s", target.ISO)
	}

	if target.Firmware != "https://example.com/firmware.cpio.gz" {
		t.Errorf("Expected firmware URL from config, got %s", target.Firmware)
	}
}

func TestConfigWatcher_HotReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	initialConfig := `targets:
  debian-13:
    iso: "https://example.com/initial.iso"
`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	cw, err := NewConfigWatcher(configPath)
	if err != nil {
		t.Fatalf("Failed to create config watcher: %v", err)
	}
	cw.Start()
	defer cw.Stop()

	// Verify initial config
	cfg := cw.Get()
	if cfg.Targets["debian-13"].ISO != "https://example.com/initial.iso" {
		t.Error("Initial config not loaded")
	}

	// Update config file
	updatedConfig := `targets:
  debian-13:
    iso: "https://example.com/updated.iso"
`
	// Wait a bit then update
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to update config: %v", err)
	}

	// Wait for reload (poll interval is 5 seconds, but we can force reload)
	time.Sleep(6 * time.Second)

	cfg = cw.Get()
	if cfg.Targets["debian-13"].ISO != "https://example.com/updated.iso" {
		t.Errorf("Config not reloaded, got %s", cfg.Targets["debian-13"].ISO)
	}
}

func TestGetTarget(t *testing.T) {
	cw, err := NewConfigWatcher("")
	if err != nil {
		t.Fatalf("Failed to create config watcher: %v", err)
	}

	target, ok := cw.GetTarget("debian-13")
	if !ok {
		t.Error("Expected to find debian-13 target")
	}
	if target.ISO == "" {
		t.Error("Expected ISO URL")
	}

	_, ok = cw.GetTarget("nonexistent")
	if ok {
		t.Error("Expected nonexistent target to not be found")
	}
}

func TestISOPathWithFilename(t *testing.T) {
	path := ISOPathWithFilename("/opt/isoboot/iso", "debian-13", "mini.iso")
	expected := "/opt/isoboot/iso/debian-13/mini.iso"
	if path != expected {
		t.Errorf("Expected %s, got %s", expected, path)
	}
}

func TestFirmwarePath(t *testing.T) {
	path := FirmwarePath("/opt/isoboot/iso", "debian-13")
	expected := "/opt/isoboot/iso/debian-13/firmware/firmware.cpio.gz"
	if path != expected {
		t.Errorf("Expected %s, got %s", expected, path)
	}
}

func TestDiskImageName(t *testing.T) {
	tests := []struct {
		name       string
		config     TargetConfig
		targetName string
		expected   string
	}{
		{
			name:       "uses DiskImageRef when set",
			config:     TargetConfig{DiskImageRef: "shared-debian"},
			targetName: "debian-13",
			expected:   "shared-debian",
		},
		{
			name:       "falls back to target name when DiskImageRef empty",
			config:     TargetConfig{},
			targetName: "debian-13",
			expected:   "debian-13",
		},
		{
			name:       "uses DiskImageRef with ISO and Firmware set",
			config:     TargetConfig{ISO: "https://example.com/iso", Firmware: "https://example.com/fw", DiskImageRef: "custom-image"},
			targetName: "default-target",
			expected:   "custom-image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.DiskImageName(tt.targetName)
			if result != tt.expected {
				t.Errorf("DiskImageName(%q) = %q, want %q", tt.targetName, result, tt.expected)
			}
		})
	}
}
