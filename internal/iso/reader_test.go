package iso

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestReadLargeDirectory tests reading an ISO with more than 50 entries
// This verifies we don't have the 50-entry limit bug
func TestReadLargeDirectory(t *testing.T) {
	// Create temp directory for test files
	tmpDir, err := os.MkdirTemp("", "isoboot-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create 100 files with repeating alphabet content
	// 001 = repeating 'a', 026 = repeating 'z', 027 = repeating 'a', etc.
	filesDir := filepath.Join(tmpDir, "files")
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		t.Fatalf("Failed to create files dir: %v", err)
	}

	expectedContent := make(map[string][]byte)
	for i := 1; i <= 100; i++ {
		filename := filepath.Join(filesDir, fmt.Sprintf("%03d", i))
		// Determine character: (i-1) % 26 gives 0-25, add 'a' to get a-z
		char := byte('a' + ((i - 1) % 26))
		content := bytes.Repeat([]byte{char}, 512)

		if err := os.WriteFile(filename, content, 0644); err != nil {
			t.Fatalf("Failed to write file %s: %v", filename, err)
		}
		expectedContent[fmt.Sprintf("%03d", i)] = content
	}

	// Create ISO using genisoimage or mkisofs
	isoPath := filepath.Join(tmpDir, "test.iso")
	cmd := exec.Command("genisoimage", "-o", isoPath, "-r", "-J", filesDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Try mkisofs as fallback
		cmd = exec.Command("mkisofs", "-o", isoPath, "-r", "-J", filesDir)
		output, err = cmd.CombinedOutput()
		if err != nil {
			t.Skipf("Neither genisoimage nor mkisofs available: %v\n%s", err, output)
		}
	}

	// Now test reading all 100 files using our ISO9660 implementation
	iso, err := OpenISO9660(isoPath)
	if err != nil {
		t.Fatalf("Failed to open ISO: %v", err)
	}
	defer iso.Close()

	// List directory
	files, err := iso.ListDirectory("")
	if err != nil {
		t.Fatalf("Failed to list root directory: %v", err)
	}

	t.Logf("Found %d files in ISO", len(files))

	if len(files) != 100 {
		t.Errorf("Expected 100 files, got %d", len(files))
		for _, f := range files {
			t.Logf("  %s (size=%d, isDir=%v)", f.Name, f.Size, f.IsDir)
		}
	}

	// Verify each file's content
	errors := 0
	for i := 1; i <= 100; i++ {
		filename := fmt.Sprintf("%03d", i)
		content, err := iso.ReadFile(filename)
		if err != nil {
			t.Errorf("Failed to read file %s: %v", filename, err)
			errors++
			continue
		}

		expected := expectedContent[filename]
		if !bytes.Equal(content, expected) {
			t.Errorf("File %s content mismatch: expected %d bytes of '%c', got %d bytes",
				filename, len(expected), expected[0], len(content))
			if len(content) > 0 {
				t.Errorf("  First byte: expected '%c', got '%c'", expected[0], content[0])
			}
			errors++
		}
	}

	if errors > 0 {
		t.Errorf("Total errors: %d", errors)
	} else {
		t.Logf("Successfully read and verified all 100 files")
	}
}

// TestReadNestedDirectory tests reading files in nested directories
func TestReadNestedDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "isoboot-nested-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create nested structure: /boot/grub/file.txt
	filesDir := filepath.Join(tmpDir, "files")
	nestedDir := filepath.Join(filesDir, "boot", "grub")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("Failed to create nested dir: %v", err)
	}

	testContent := []byte("Hello from nested directory!")
	if err := os.WriteFile(filepath.Join(nestedDir, "file.txt"), testContent, 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Create ISO
	isoPath := filepath.Join(tmpDir, "nested.iso")
	cmd := exec.Command("genisoimage", "-o", isoPath, "-r", "-J", filesDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.Command("mkisofs", "-o", isoPath, "-r", "-J", filesDir)
		output, err = cmd.CombinedOutput()
		if err != nil {
			t.Skipf("Neither genisoimage nor mkisofs available: %v\n%s", err, output)
		}
	}

	iso, err := OpenISO9660(isoPath)
	if err != nil {
		t.Fatalf("Failed to open ISO: %v", err)
	}
	defer iso.Close()

	// List root
	rootEntries, err := iso.ListDirectory("")
	if err != nil {
		t.Fatalf("Failed to list root: %v", err)
	}
	t.Logf("Root entries: %d", len(rootEntries))
	for _, e := range rootEntries {
		t.Logf("  %s (dir=%v)", e.Name, e.IsDir)
	}

	// List /boot
	bootEntries, err := iso.ListDirectory("boot")
	if err != nil {
		t.Fatalf("Failed to list /boot: %v", err)
	}
	t.Logf("/boot entries: %d", len(bootEntries))
	for _, e := range bootEntries {
		t.Logf("  %s (dir=%v)", e.Name, e.IsDir)
	}

	// Read the nested file
	content, err := iso.ReadFile("boot/grub/file.txt")
	if err != nil {
		t.Fatalf("Failed to read boot/grub/file.txt: %v", err)
	}

	if !bytes.Equal(content, testContent) {
		t.Errorf("Content mismatch: expected %q, got %q", testContent, content)
	} else {
		t.Logf("Successfully read nested file with content: %s", content)
	}
}
