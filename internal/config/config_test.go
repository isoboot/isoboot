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

	// Test fallback to "default" directory for path traversal attempt in diskImageName
	path = ISOPathWithFilename("/opt/isoboot/iso", "..", "mini.iso")
	expected = "/opt/isoboot/iso/default/mini.iso"
	if path != expected {
		t.Errorf("Expected %s for invalid diskImageName, got %s", expected, path)
	}

	// Test path sanitization for filename with path traversal attempt
	path = ISOPathWithFilename("/opt/isoboot/iso", "debian-13", "../../etc/passwd")
	expected = "/opt/isoboot/iso/debian-13/passwd"
	if path != expected {
		t.Errorf("Expected %s for path traversal filename, got %s", expected, path)
	}

	// Test fallback to "file" for lone ".." filename
	path = ISOPathWithFilename("/opt/isoboot/iso", "debian-13", "..")
	expected = "/opt/isoboot/iso/debian-13/file"
	if path != expected {
		t.Errorf("Expected %s for '..' filename, got %s", expected, path)
	}
}

func TestFirmwarePath(t *testing.T) {
	path := FirmwarePath("/opt/isoboot/iso", "debian-13")
	expected := "/opt/isoboot/iso/debian-13/firmware/firmware.cpio.gz"
	if path != expected {
		t.Errorf("Expected %s, got %s", expected, path)
	}

	// Test fallback to "default" for invalid diskImageName
	path = FirmwarePath("/opt/isoboot/iso", "..")
	expected = "/opt/isoboot/iso/default/firmware/firmware.cpio.gz"
	if path != expected {
		t.Errorf("Expected %s for invalid diskImageName, got %s", expected, path)
	}
}

func TestInitrdOrigPath(t *testing.T) {
	path := InitrdOrigPath("/opt/isoboot/iso", "debian-13")
	expected := "/opt/isoboot/iso/debian-13/initrd.gz.orig"
	if path != expected {
		t.Errorf("Expected %s, got %s", expected, path)
	}

	// Test fallback to "default" for invalid diskImageName
	path = InitrdOrigPath("/opt/isoboot/iso", "..")
	expected = "/opt/isoboot/iso/default/initrd.gz.orig"
	if path != expected {
		t.Errorf("Expected %s for invalid diskImageName, got %s", expected, path)
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
		{
			name:       "extracts base name from path with parent directory traversal",
			config:     TargetConfig{DiskImageRef: "../../../etc/passwd"},
			targetName: "debian-13",
			expected:   "passwd",
		},
		{
			name:       "extracts base name from absolute path",
			config:     TargetConfig{DiskImageRef: "/etc/passwd"},
			targetName: "debian-13",
			expected:   "passwd",
		},
		{
			name:       "extracts base name from target name with parent directory traversal",
			config:     TargetConfig{},
			targetName: "../../../etc/shadow",
			expected:   "shadow",
		},
		{
			name:       "falls back to target name for lone ..",
			config:     TargetConfig{DiskImageRef: ".."},
			targetName: "debian-13",
			expected:   "debian-13",
		},
		{
			name:       "falls back to target name for lone .",
			config:     TargetConfig{DiskImageRef: "."},
			targetName: "debian-13",
			expected:   "debian-13",
		},
		{
			name:       "falls back to default when both are invalid",
			config:     TargetConfig{DiskImageRef: ".."},
			targetName: "..",
			expected:   "default",
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

func TestDiskImageRef_YAMLUnmarshal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `targets:
  debian-13:
    iso: "https://example.com/debian.iso"
    diskImageRef: "shared-debian-image"
  ubuntu-24:
    iso: "https://example.com/ubuntu.iso"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	cw, err := NewConfigWatcher(configPath)
	if err != nil {
		t.Fatalf("Failed to create config watcher: %v", err)
	}

	// Test that diskImageRef is properly unmarshaled
	debianTarget, ok := cw.GetTarget("debian-13")
	if !ok {
		t.Fatal("Expected debian-13 target")
	}
	if debianTarget.DiskImageRef != "shared-debian-image" {
		t.Errorf("DiskImageRef = %q, want %q", debianTarget.DiskImageRef, "shared-debian-image")
	}
	if debianTarget.DiskImageName("debian-13") != "shared-debian-image" {
		t.Errorf("DiskImageName() = %q, want %q", debianTarget.DiskImageName("debian-13"), "shared-debian-image")
	}

	// Test that missing diskImageRef falls back to target name
	ubuntuTarget, ok := cw.GetTarget("ubuntu-24")
	if !ok {
		t.Fatal("Expected ubuntu-24 target")
	}
	if ubuntuTarget.DiskImageRef != "" {
		t.Errorf("DiskImageRef = %q, want empty", ubuntuTarget.DiskImageRef)
	}
	if ubuntuTarget.DiskImageName("ubuntu-24") != "ubuntu-24" {
		t.Errorf("DiskImageName() = %q, want %q", ubuntuTarget.DiskImageName("ubuntu-24"), "ubuntu-24")
	}
}

func TestPathFunctions_WithMaliciousDiskImageRef(t *testing.T) {
	// Integration test: verify end-to-end path sanitization when TargetConfig
	// has a malicious DiskImageRef and path functions use the sanitized result
	basePath := "/opt/isoboot/iso"

	tests := []struct {
		name         string
		diskImageRef string
		targetName   string
		wantDir      string
	}{
		{
			name:         "path traversal in DiskImageRef",
			diskImageRef: "../../../etc/passwd",
			targetName:   "debian-13",
			wantDir:      "passwd",
		},
		{
			name:         "null byte injection in DiskImageRef",
			diskImageRef: "valid\x00malicious",
			targetName:   "debian-13",
			wantDir:      "debian-13", // falls back to target name
		},
		{
			name:         "lone .. in DiskImageRef",
			diskImageRef: "..",
			targetName:   "ubuntu-24",
			wantDir:      "ubuntu-24", // falls back to target name
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TargetConfig{DiskImageRef: tt.diskImageRef}
			diskImageName := cfg.DiskImageName(tt.targetName)

			// Verify DiskImageName returns sanitized value
			if diskImageName != tt.wantDir {
				t.Errorf("DiskImageName() = %q, want %q", diskImageName, tt.wantDir)
			}

			// Verify path functions use the sanitized name correctly
			isoPath := ISOPathWithFilename(basePath, diskImageName, "mini.iso")
			expectedISO := basePath + "/" + tt.wantDir + "/mini.iso"
			if isoPath != expectedISO {
				t.Errorf("ISOPathWithFilename() = %q, want %q", isoPath, expectedISO)
			}

			fwPath := FirmwarePath(basePath, diskImageName)
			expectedFW := basePath + "/" + tt.wantDir + "/firmware/firmware.cpio.gz"
			if fwPath != expectedFW {
				t.Errorf("FirmwarePath() = %q, want %q", fwPath, expectedFW)
			}

			initrdPath := InitrdOrigPath(basePath, diskImageName)
			expectedInitrd := basePath + "/" + tt.wantDir + "/initrd.gz.orig"
			if initrdPath != expectedInitrd {
				t.Errorf("InitrdOrigPath() = %q, want %q", initrdPath, expectedInitrd)
			}
		})
	}
}
