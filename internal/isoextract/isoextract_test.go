package isoextract

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rot13 applies ROT-13 to each byte in s.
func rot13(s string) string {
	out := make([]byte, len(s))
	for i, b := range []byte(s) {
		switch {
		case b >= 'a' && b <= 'z':
			out[i] = 'a' + (b-'a'+13)%26
		default:
			out[i] = b
		}
	}
	return string(out)
}

// randChars returns n random lowercase ASCII characters.
func randChars(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(rand.IntN(26))
	}
	return string(b)
}

// buildISO creates an ISO from srcDir using genisoimage with Rock Ridge
// extensions and returns the path to the ISO file.
func buildISO(srcDir, isoPath string) {
	cmd := exec.Command("genisoimage", "-o", isoPath, "-R", srcDir)
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "genisoimage failed: %s", string(output))
}

var _ = Describe("Extract", func() {
	var (
		tmpDir  string
		isoPath string
		destDir string
	)

	BeforeEach(func() {
		if _, err := exec.LookPath("genisoimage"); err != nil {
			Skip("genisoimage not in PATH")
		}
		var err error
		tmpDir, err = os.MkdirTemp("", "isoextract-test-*")
		Expect(err).NotTo(HaveOccurred())
		isoPath = filepath.Join(tmpDir, "test.iso")
		destDir = filepath.Join(tmpDir, "out")
		Expect(os.MkdirAll(destDir, 0o755)).To(Succeed())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	Context("single file extraction", func() {
		It("extracts one file with correct content", func() {
			srcDir := filepath.Join(tmpDir, "src")
			Expect(os.MkdirAll(srcDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("world\n"), 0o644)).To(Succeed())

			buildISO(srcDir, isoPath)

			err := Extract(isoPath, []string{"hello.txt"}, destDir)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(filepath.Join(destDir, "hello.txt"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("world\n"))
		})
	})

	Context("file not found", func() {
		It("returns an error listing the missing path", func() {
			srcDir := filepath.Join(tmpDir, "src")
			Expect(os.MkdirAll(srcDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(srcDir, "exists.txt"), []byte("ok"), 0o644)).To(Succeed())

			buildISO(srcDir, isoPath)

			err := Extract(isoPath, []string{"does-not-exist.txt"}, destDir)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does-not-exist.txt"))
		})
	})

	Context("subset extraction", func() {
		It("extracts only the requested files from a larger set", func() {
			srcDir := filepath.Join(tmpDir, "src")
			Expect(os.MkdirAll(srcDir, 0o755)).To(Succeed())

			// Create 100 files but request only 3.
			names := make([]string, 100)
			for i := range 100 {
				chars := randChars(4)
				name := fmt.Sprintf("%03d-%s.txt", i+1, chars)
				names[i] = name
				content := rot13(chars) + "\n"
				Expect(os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0o644)).To(Succeed())
			}

			buildISO(srcDir, isoPath)

			requested := []string{names[0], names[49], names[99]}
			err := Extract(isoPath, requested, destDir)
			Expect(err).NotTo(HaveOccurred())

			// Verify only the 3 requested files exist.
			entries, err := os.ReadDir(destDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))

			for _, name := range requested {
				data, err := os.ReadFile(filepath.Join(destDir, name))
				Expect(err).NotTo(HaveOccurred())
				// Verify content: ROT-13 of the 4 chars + newline.
				chars := name[4:8] // e.g. "001-XXXX.txt" -> "XXXX"
				Expect(string(data)).To(Equal(rot13(chars) + "\n"))
			}
		})
	})

	Context("100-file round-trip", func() {
		It("extracts all 100 files with correct content", func() {
			srcDir := filepath.Join(tmpDir, "src")
			Expect(os.MkdirAll(srcDir, 0o755)).To(Succeed())

			type fileInfo struct {
				name    string
				content string
			}
			files := make([]fileInfo, 100)
			requestPaths := make([]string, 100)

			for i := range 100 {
				chars := randChars(4)
				name := fmt.Sprintf("%03d-%s.txt", i+1, chars)
				content := strings.Repeat(rot13(chars), 25) // 100 bytes
				files[i] = fileInfo{name: name, content: content}
				requestPaths[i] = name
				Expect(os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0o644)).To(Succeed())
			}

			buildISO(srcDir, isoPath)

			err := Extract(isoPath, requestPaths, destDir)
			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(destDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(100))

			for _, fi := range files {
				data, err := os.ReadFile(filepath.Join(destDir, fi.name))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(Equal(fi.content))
			}
		})
	})
})
