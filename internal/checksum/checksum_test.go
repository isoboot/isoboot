package checksum

import (
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testShasumURL = "https://example.com/SHA256SUMS"
	testFileURL   = "https://example.com/myfile.iso"
)

var _ = Describe("DetectAlgorithm", func() {
	It("should detect SHA-256 from a 64-character hex string", func() {
		hash := "1e8e7112c8ecf8e5f569e2e47c97db027ecd21bbe48897a7e4be0aee4cfb1bce"
		algo, err := DetectAlgorithm(hash)
		Expect(err).NotTo(HaveOccurred())
		Expect(algo).To(Equal(crypto.SHA256))
	})

	It("should detect SHA-512 from a 128-character hex string", func() {
		hash := "36cf12b0f68090e14977a08e077e10528e0a785d4f53dad60d5b3b1eed6865381098dc06e40e0f4e63a5a9ed35b7f1dfad4e18e18563cdca67b7c2b96dc3cb6a"
		algo, err := DetectAlgorithm(hash)
		Expect(err).NotTo(HaveOccurred())
		Expect(algo).To(Equal(crypto.SHA512))
	})

	It("should reject a hash with invalid length", func() {
		_, err := DetectAlgorithm("abcdef0123456789")
		Expect(err).To(HaveOccurred())
	})

	It("should reject non-hex characters", func() {
		_, err := DetectAlgorithm(strings.Repeat("z", 64))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ParseShasumFile", func() {
	It("should resolve relative paths (Debian SHA256SUMS style)", func() {
		content := "1e8e7112c8ecf8e5f569e2e47c97db027ecd21bbe48897a7e4be0aee4cfb1bce  ./netboot/debian-installer/amd64/initrd.gz\n" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./netboot/debian-installer/amd64/linux"
		shasumURL := "https://ftp.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/SHA256SUMS"
		fileURL := "https://ftp.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/netboot/debian-installer/amd64/initrd.gz"

		hash, err := ParseShasumFile(content, fileURL, shasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("1e8e7112c8ecf8e5f569e2e47c97db027ecd21bbe48897a7e4be0aee4cfb1bce"))
	})

	It("should match bare filenames in the same directory (cdimage SHA512SUMS style)", func() {
		content := "36cf12b0f68090e14977a08e077e10528e0a785d4f53dad60d5b3b1eed6865381098dc06e40e0f4e63a5a9ed35b7f1dfad4e18e18563cdca67b7c2b96dc3cb6a  firmware.cpio.gz\n" +
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  other-file.tar.gz"
		shasumURL := "https://cdimage.debian.org/images/unofficial/non-free/firmware/bookworm/13.3.0/SHA512SUMS"
		fileURL := "https://cdimage.debian.org/images/unofficial/non-free/firmware/bookworm/13.3.0/firmware.cpio.gz"

		hash, err := ParseShasumFile(content, fileURL, shasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("36cf12b0f68090e14977a08e077e10528e0a785d4f53dad60d5b3b1eed6865381098dc06e40e0f4e63a5a9ed35b7f1dfad4e18e18563cdca67b7c2b96dc3cb6a"))
	})

	It("should fall back to longest suffix match", func() {
		content := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  some/other/path/initrd.gz"
		shasumURL := "https://example.com/base/SHA256SUMS"
		fileURL := "https://example.com/base/deeply/nested/path/initrd.gz"

		hash, err := ParseShasumFile(content, fileURL, shasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"))
	})

	It("should reject ambiguous suffix matches", func() {
		content := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  dir1/initrd.gz\n" +
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  dir2/initrd.gz"
		fileURL := "https://example.com/some/other/initrd.gz"

		_, err := ParseShasumFile(content, fileURL, testShasumURL)
		Expect(err).To(HaveOccurred())
	})

	It("should parse hash-first format", func() {
		content := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  myfile.iso"

		hash, err := ParseShasumFile(content, testFileURL, testShasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"))
	})

	It("should parse filename-first format", func() {
		content := "myfile.iso  eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

		hash, err := ParseShasumFile(content, testFileURL, testShasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"))
	})

	It("should strip ./ prefix from paths", func() {
		content := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff  ./subdir/file.bin"
		shasumURL := "https://example.com/images/SHA256SUMS"
		fileURL := "https://example.com/images/subdir/file.bin"

		hash, err := ParseShasumFile(content, fileURL, shasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"))
	})

	It("should normalize uppercase hashes to lowercase", func() {
		content := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA  myfile.iso"

		hash, err := ParseShasumFile(content, testFileURL, testShasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	})

	It("should skip comment lines", func() {
		content := "# This is a comment\n" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  myfile.iso"

		hash, err := ParseShasumFile(content, testFileURL, testShasumURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(Equal("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	})

	It("should return error when no entry matches", func() {
		content := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  other.iso"
		fileURL := "https://example.com/missing.iso"

		_, err := ParseShasumFile(content, fileURL, testShasumURL)
		Expect(err).To(HaveOccurred())
	})

	Context("URL validation", func() {
		const validContent = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  myfile.iso"

		It("should reject http scheme in file URL", func() {
			_, err := ParseShasumFile(validContent, "http://example.com/myfile.iso", testShasumURL)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only https is allowed"))
		})

		It("should reject http scheme in shasum URL", func() {
			_, err := ParseShasumFile(validContent, testFileURL, "http://example.com/SHA256SUMS")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only https is allowed"))
		})

		It("should reject ftp scheme", func() {
			_, err := ParseShasumFile(validContent, "ftp://example.com/myfile.iso", testShasumURL)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only https is allowed"))
		})

		It("should reject file scheme", func() {
			_, err := ParseShasumFile(validContent, "file:///tmp/myfile.iso", "file:///tmp/SHA256SUMS")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only https is allowed"))
		})

		It("should reject empty scheme", func() {
			_, err := ParseShasumFile(validContent, "//example.com/myfile.iso", testShasumURL)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only https is allowed"))
		})

		It("should reject mismatched hosts", func() {
			_, err := ParseShasumFile(validContent, "https://evil.com/myfile.iso", testShasumURL)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("host mismatch"))
		})

		It("should reject mismatched hosts with subdomain difference", func() {
			_, err := ParseShasumFile(validContent,
				"https://cdn.example.com/myfile.iso",
				"https://mirror.example.com/SHA256SUMS")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("host mismatch"))
		})

		It("should reject mismatched ports on same hostname", func() {
			_, err := ParseShasumFile(validContent,
				"https://example.com:8443/myfile.iso",
				"https://example.com:9443/SHA256SUMS")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("host mismatch"))
		})
	})
})

var _ = Describe("VerifyFile", func() {
	It("should verify a SHA-256 hash", func() {
		dir := GinkgoT().TempDir()
		filePath := filepath.Join(dir, "testfile")
		data := []byte("hello world\n")
		Expect(os.WriteFile(filePath, data, 0o644)).To(Succeed())

		h := sha256.Sum256(data)
		expectedHash := hex.EncodeToString(h[:])

		Expect(VerifyFile(filePath, expectedHash)).To(Succeed())
	})

	It("should accept an uppercase hash", func() {
		dir := GinkgoT().TempDir()
		filePath := filepath.Join(dir, "testfile")
		data := []byte("hello world\n")
		Expect(os.WriteFile(filePath, data, 0o644)).To(Succeed())

		h := sha256.Sum256(data)
		expectedHash := strings.ToUpper(hex.EncodeToString(h[:]))

		Expect(VerifyFile(filePath, expectedHash)).To(Succeed())
	})

	It("should verify a SHA-512 hash", func() {
		dir := GinkgoT().TempDir()
		filePath := filepath.Join(dir, "testfile")
		data := []byte("hello world\n")
		Expect(os.WriteFile(filePath, data, 0o644)).To(Succeed())

		h := sha512.Sum512(data)
		expectedHash := hex.EncodeToString(h[:])

		Expect(VerifyFile(filePath, expectedHash)).To(Succeed())
	})

	It("should return error on hash mismatch", func() {
		dir := GinkgoT().TempDir()
		filePath := filepath.Join(dir, "testfile")
		data := []byte("hello world\n")
		Expect(os.WriteFile(filePath, data, 0o644)).To(Succeed())

		wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
		err := VerifyFile(filePath, wrongHash)
		Expect(err).To(HaveOccurred())
	})

	It("should return error for a missing file", func() {
		err := VerifyFile("/nonexistent/path/file", "0000000000000000000000000000000000000000000000000000000000000000")
		Expect(err).To(HaveOccurred())
	})
})
