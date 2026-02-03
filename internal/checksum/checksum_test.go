package checksum

import (
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testShasumURL = "https://example.com/SHA256SUMS"
	testFileURL   = "https://example.com/myfile.iso"
)

func TestDetectAlgorithm_SHA256(t *testing.T) {
	hash := "1e8e7112c8ecf8e5f569e2e47c97db027ecd21bbe48897a7e4be0aee4cfb1bce"
	algo, err := DetectAlgorithm(hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if algo != crypto.SHA256 {
		t.Errorf("expected SHA256, got %v", algo)
	}
}

func TestDetectAlgorithm_SHA512(t *testing.T) {
	hash := "36cf12b0f68090e14977a08e077e10528e0a785d4f53dad60d5b3b1eed6865381098dc06e40e0f4e63a5a9ed35b7f1dfad4e18e18563cdca67b7c2b96dc3cb6a"
	algo, err := DetectAlgorithm(hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if algo != crypto.SHA512 {
		t.Errorf("expected SHA512, got %v", algo)
	}
}

func TestDetectAlgorithm_InvalidLength(t *testing.T) {
	_, err := DetectAlgorithm("abcdef0123456789")
	if err == nil {
		t.Fatal("expected error for invalid hash length")
	}
}

func TestDetectAlgorithm_InvalidHexChars(t *testing.T) {
	// 64 'z' characters — correct length but not valid hex.
	_, err := DetectAlgorithm(strings.Repeat("z", 64))
	if err == nil {
		t.Fatal("expected error for non-hex characters")
	}
}

func TestParseShasumFile_RelativePathResolution(t *testing.T) {
	// Debian SHA256SUMS style: hash-first with ./ prefix, nested path.
	content := `1e8e7112c8ecf8e5f569e2e47c97db027ecd21bbe48897a7e4be0aee4cfb1bce  ./netboot/debian-installer/amd64/initrd.gz
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./netboot/debian-installer/amd64/linux`
	shasumURL := "https://ftp.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/SHA256SUMS"
	fileURL := "https://ftp.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/netboot/debian-installer/amd64/initrd.gz"

	hash, err := ParseShasumFile(content, fileURL, shasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "1e8e7112c8ecf8e5f569e2e47c97db027ecd21bbe48897a7e4be0aee4cfb1bce"
	if hash != expected {
		t.Errorf("expected %s, got %s", expected, hash)
	}
}

func TestParseShasumFile_SameDirectoryBareFilename(t *testing.T) {
	// cdimage.debian.org SHA512SUMS style: same directory, bare filename, sha512.
	content := `36cf12b0f68090e14977a08e077e10528e0a785d4f53dad60d5b3b1eed6865381098dc06e40e0f4e63a5a9ed35b7f1dfad4e18e18563cdca67b7c2b96dc3cb6a  firmware.cpio.gz
bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  other-file.tar.gz`
	shasumURL := "https://cdimage.debian.org/images/unofficial/non-free/firmware/bookworm/13.3.0/SHA512SUMS"
	fileURL := "https://cdimage.debian.org/images/unofficial/non-free/firmware/bookworm/13.3.0/firmware.cpio.gz"

	hash, err := ParseShasumFile(content, fileURL, shasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "36cf12b0f68090e14977a08e077e10528e0a785d4f53dad60d5b3b1eed6865381098dc06e40e0f4e63a5a9ed35b7f1dfad4e18e18563cdca67b7c2b96dc3cb6a"
	if hash != expected {
		t.Errorf("expected %s, got %s", expected, hash)
	}
}

func TestParseShasumFile_LongestSuffixFallback(t *testing.T) {
	// The shasum file uses a different base path but the suffix matches.
	content := `cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  some/other/path/initrd.gz`
	shasumURL := "https://example.com/base/SHA256SUMS"
	fileURL := "https://example.com/base/deeply/nested/path/initrd.gz"

	hash, err := ParseShasumFile(content, fileURL, shasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if hash != expected {
		t.Errorf("expected %s, got %s", expected, hash)
	}
}

func TestParseShasumFile_AmbiguousSuffix(t *testing.T) {
	content := `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  dir1/initrd.gz
bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  dir2/initrd.gz`
	fileURL := "https://example.com/some/other/initrd.gz"

	_, err := ParseShasumFile(content, fileURL, testShasumURL)
	if err == nil {
		t.Fatal("expected error for ambiguous match")
	}
}

func TestParseShasumFile_HashFirstFormat(t *testing.T) {
	content := `dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  myfile.iso`
	fileURL := testFileURL

	hash, err := ParseShasumFile(content, fileURL, testShasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" {
		t.Errorf("unexpected hash: %s", hash)
	}
}

func TestParseShasumFile_FilenameFirstFormat(t *testing.T) {
	content := `myfile.iso  eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee`
	fileURL := testFileURL

	hash, err := ParseShasumFile(content, fileURL, testShasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" {
		t.Errorf("unexpected hash: %s", hash)
	}
}

func TestParseShasumFile_DotSlashPrefixStripping(t *testing.T) {
	// Both the relative path and the shasum entry have ./ prefixes.
	content := `ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff  ./subdir/file.bin`
	shasumURL := "https://example.com/images/SHA256SUMS"
	fileURL := "https://example.com/images/subdir/file.bin"

	hash, err := ParseShasumFile(content, fileURL, shasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" {
		t.Errorf("unexpected hash: %s", hash)
	}
}

func TestParseShasumFile_CommentLinesSkipped(t *testing.T) {
	content := `# This is a comment
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  myfile.iso`
	fileURL := testFileURL

	hash, err := ParseShasumFile(content, fileURL, testShasumURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("unexpected hash: %s", hash)
	}
}

func TestParseShasumFile_NoMatchingEntry(t *testing.T) {
	content := `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  other.iso`
	fileURL := "https://example.com/missing.iso"

	_, err := ParseShasumFile(content, fileURL, testShasumURL)
	if err == nil {
		t.Fatal("expected error for no matching entry")
	}
}

func TestVerifyFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "testfile")
	data := []byte("hello world\n")
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	h := sha256.Sum256(data)
	expectedHash := hex.EncodeToString(h[:])

	if err := VerifyFile(filePath, expectedHash); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyFile_UppercaseHash(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "testfile")
	data := []byte("hello world\n")
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	h := sha256.Sum256(data)
	expectedHash := strings.ToUpper(hex.EncodeToString(h[:]))

	if err := VerifyFile(filePath, expectedHash); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyFile_SHA512HappyPath(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "testfile")
	data := []byte("hello world\n")
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	h := sha512.Sum512(data)
	expectedHash := hex.EncodeToString(h[:])

	if err := VerifyFile(filePath, expectedHash); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyFile_Mismatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "testfile")
	data := []byte("hello world\n")
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
	err := VerifyFile(filePath, wrongHash)
	if err == nil {
		t.Fatal("expected error for hash mismatch")
	}
}

func TestVerifyFile_FileNotFound(t *testing.T) {
	err := VerifyFile("/nonexistent/path/file", "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
