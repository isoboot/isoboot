package controllerclient

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrNotFound_IsComparable(t *testing.T) {
	// Verify ErrNotFound can be used with errors.Is
	wrappedErr := fmt.Errorf("boottarget foo: %w", ErrNotFound)

	if !errors.Is(wrappedErr, ErrNotFound) {
		t.Error("expected errors.Is(wrappedErr, ErrNotFound) to be true")
	}
}

func TestErrNotFound_NotMatchOther(t *testing.T) {
	otherErr := errors.New("some other error")

	if errors.Is(otherErr, ErrNotFound) {
		t.Error("expected errors.Is(otherErr, ErrNotFound) to be false")
	}
}

func TestBootInfo_Fields(t *testing.T) {
	info := BootInfo{
		MachineName: "test-machine",
		DeployName:  "test-deploy",
		Target:      "debian-13",
	}

	if info.MachineName != "test-machine" {
		t.Errorf("expected MachineName 'test-machine', got %q", info.MachineName)
	}
	if info.DeployName != "test-deploy" {
		t.Errorf("expected DeployName 'test-deploy', got %q", info.DeployName)
	}
	if info.Target != "debian-13" {
		t.Errorf("expected Target 'debian-13', got %q", info.Target)
	}
}

func TestBootTargetInfo_Fields(t *testing.T) {
	info := BootTargetInfo{
		DiskImageRef:        "debian-13",
		IncludeFirmwarePath: "/initrd.gz",
		Template:            "#!ipxe\nboot",
	}

	if info.DiskImageRef != "debian-13" {
		t.Errorf("expected DiskImageRef 'debian-13', got %q", info.DiskImageRef)
	}
	if info.IncludeFirmwarePath != "/initrd.gz" {
		t.Errorf("expected IncludeFirmwarePath '/initrd.gz', got %q", info.IncludeFirmwarePath)
	}
	if info.Template != "#!ipxe\nboot" {
		t.Errorf("expected Template '#!ipxe\\nboot', got %q", info.Template)
	}
}
