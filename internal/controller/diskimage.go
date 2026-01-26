package controller

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
)

const downloadTimeout = 30 * time.Minute

// reconcileDiskImages reconciles all DiskImage resources
func (c *Controller) reconcileDiskImages(ctx context.Context) {
	diskImages, err := c.k8sClient.ListDiskImages(ctx)
	if err != nil {
		log.Printf("Controller: failed to list diskimages: %v", err)
		return
	}

	for _, di := range diskImages {
		c.reconcileDiskImage(ctx, di)
	}
}

// reconcileDiskImage reconciles a single DiskImage
func (c *Controller) reconcileDiskImage(ctx context.Context, di *k8s.DiskImage) {
	// Initialize status if empty
	if di.Status.Phase == "" {
		log.Printf("Controller: initializing DiskImage %s status to Pending", di.Name)
		status := &k8s.DiskImageStatus{
			Phase:   "Pending",
			Message: "Waiting for download",
			ISO: &k8s.DiskImageVerification{
				FileSizeMatch: "pending",
				DigestSha512:  "pending",
				DigestSha256:  "pending",
			},
		}
		if di.Firmware != "" {
			status.Firmware = &k8s.DiskImageVerification{
				FileSizeMatch: "pending",
				DigestSha512:  "pending",
				DigestSha256:  "pending",
			}
		}
		if err := c.k8sClient.UpdateDiskImageStatus(ctx, di.Name, status); err != nil {
			log.Printf("Controller: failed to initialize DiskImage %s: %v", di.Name, err)
		}
		return
	}

	// If already Complete or Failed, nothing to do
	if di.Status.Phase == "Complete" || di.Status.Phase == "Failed" {
		return
	}

	// If Pending or Downloading, ensure download is running
	// (Downloading phase may be stale if controller restarted mid-download)
	if di.Status.Phase == "Pending" || di.Status.Phase == "Downloading" {
		// Check if download is already in progress (prevent concurrent downloads)
		if _, alreadyRunning := c.activeDownloads.LoadOrStore(di.Name, true); alreadyRunning {
			return
		}
		go c.downloadDiskImage(ctx, di)
	}
}

// downloadDiskImage downloads and verifies a DiskImage
func (c *Controller) downloadDiskImage(parentCtx context.Context, di *k8s.DiskImage) {
	// Clean up activeDownloads tracking when done
	defer c.activeDownloads.Delete(di.Name)

	// Create a context with timeout for download operations (HTTP requests)
	downloadCtx, cancel := context.WithTimeout(parentCtx, downloadTimeout)
	defer cancel()

	// Use background context for status updates so they succeed even if download times out
	statusCtx := context.Background()

	// Update status to Downloading
	status := &k8s.DiskImageStatus{
		Phase:    "Downloading",
		Progress: 0,
		Message:  "Starting download",
		ISO: &k8s.DiskImageVerification{
			FileSizeMatch: "processing",
			DigestSha512:  "pending",
			DigestSha256:  "pending",
		},
	}
	if di.Firmware != "" {
		status.Firmware = &k8s.DiskImageVerification{
			FileSizeMatch: "pending",
			DigestSha512:  "pending",
			DigestSha256:  "pending",
		}
	}
	if err := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); err != nil {
		log.Printf("Controller: failed to update DiskImage %s to Downloading: %v", di.Name, err)
		return
	}

	// Download ISO
	isoPath := filepath.Join(c.isoBasePath, di.Name, filepath.Base(di.ISO))
	isoResult, err := c.downloadAndVerify(downloadCtx, di.ISO, isoPath)
	if err != nil {
		status.Phase = "Failed"
		status.Message = fmt.Sprintf("ISO download failed: %v", err)
		status.ISO = isoResult
		if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
			log.Printf("Controller: failed to update DiskImage %s to Failed: %v", di.Name, updateErr)
		}
		return
	}
	status.ISO = isoResult
	if di.Firmware != "" {
		status.Progress = 50
	} else {
		status.Progress = 100
	}

	// Download firmware if present
	if di.Firmware != "" {
		status.Message = "Downloading firmware"
		status.Firmware = &k8s.DiskImageVerification{
			FileSizeMatch: "processing",
			DigestSha512:  "pending",
			DigestSha256:  "pending",
		}
		if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
			log.Printf("Controller: failed to update DiskImage %s firmware progress: %v", di.Name, updateErr)
		}

		fwPath := filepath.Join(c.isoBasePath, di.Name, "firmware", filepath.Base(di.Firmware))
		fwResult, err := c.downloadAndVerify(downloadCtx, di.Firmware, fwPath)
		if err != nil {
			status.Phase = "Failed"
			status.Message = fmt.Sprintf("Firmware download failed: %v", err)
			status.Firmware = fwResult
			if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
				log.Printf("Controller: failed to update DiskImage %s to Failed: %v", di.Name, updateErr)
			}
			return
		}
		status.Firmware = fwResult
	}

	// Success
	status.Phase = "Complete"
	status.Progress = 100
	status.Message = "Download and verification complete"
	if err := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); err != nil {
		log.Printf("Controller: failed to update DiskImage %s to Complete: %v", di.Name, err)
	}
	log.Printf("Controller: DiskImage %s download complete", di.Name)
}

// downloadAndVerify downloads a file and verifies checksums
func (c *Controller) downloadAndVerify(ctx context.Context, fileURL, destPath string) (*k8s.DiskImageVerification, error) {
	result := &k8s.DiskImageVerification{
		FileSizeMatch: "processing",
		DigestSha512:  "pending",
		DigestSha256:  "pending",
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("create directory: %w", err)
	}

	// Get expected file size from HEAD request
	// Use http.DefaultClient to reuse connections across downloads
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, fileURL, nil)
	if err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("create HEAD request: %w", err)
	}
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("HEAD request: %w", err)
	}
	if headResp.Body != nil {
		headResp.Body.Close()
	}
	expectedSize := headResp.ContentLength

	// Try to find checksums
	checksums := c.discoverChecksums(fileURL)

	// Check if file already exists and is valid
	if existingResult := c.verifyExistingFile(destPath, expectedSize, checksums, filepath.Base(fileURL)); existingResult != nil {
		log.Printf("Controller: existing file %s verified, skipping download", filepath.Base(destPath))
		return existingResult, nil
	}

	// Download file
	log.Printf("Controller: downloading %s", fileURL)
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("create GET request: %w", err)
	}
	resp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("GET request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Create temp file
	tmpPath := destPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	// Create hashers
	sha256Hash := sha256.New()
	sha512Hash := sha512.New()
	multiWriter := io.MultiWriter(tmpFile, sha256Hash, sha512Hash)

	// Download with progress
	written, err := io.Copy(multiWriter, resp.Body)
	tmpFile.Close()
	if err != nil {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("download: %w", err)
	}

	// Verify file size
	if expectedSize > 0 && written != expectedSize {
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("size mismatch: expected %d, got %d", expectedSize, written)
	}
	result.FileSizeMatch = "verified"

	// Verify checksums
	result.DigestSha512 = verifyChecksum(checksums, "sha512", sha512Hash, filepath.Base(fileURL))
	result.DigestSha256 = verifyChecksum(checksums, "sha256", sha256Hash, filepath.Base(fileURL))

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return result, fmt.Errorf("rename: %w", err)
	}

	log.Printf("Controller: downloaded %s (%d bytes)", filepath.Base(destPath), written)
	return result, nil
}

// verifyExistingFile checks if an existing file is valid
// Returns nil if file doesn't exist or verification fails (should download)
// Returns verification result if file is valid (skip download)
func (c *Controller) verifyExistingFile(filePath string, expectedSize int64, checksums map[string]map[string]string, filename string) *k8s.DiskImageVerification {
	// Check if file exists
	info, err := os.Stat(filePath)
	if err != nil {
		return nil // File doesn't exist, need to download
	}

	// Check size
	if expectedSize > 0 && info.Size() != expectedSize {
		log.Printf("Controller: existing file %s size mismatch (expected %d, got %d), will re-download", filename, expectedSize, info.Size())
		return nil // Size mismatch, need to download
	}

	// Compute checksums
	file, err := os.Open(filePath)
	if err != nil {
		return nil // Can't read file, need to download
	}
	defer file.Close()

	sha256Hash := sha256.New()
	sha512Hash := sha512.New()
	multiWriter := io.MultiWriter(sha256Hash, sha512Hash)

	if _, err := io.Copy(multiWriter, file); err != nil {
		return nil // Error reading file, need to download
	}

	// Verify checksums
	result := &k8s.DiskImageVerification{
		FileSizeMatch: "verified",
		DigestSha256:  verifyChecksum(checksums, "sha256", sha256Hash, filename),
		DigestSha512:  verifyChecksum(checksums, "sha512", sha512Hash, filename),
	}

	// If any checksum verification failed, need to re-download
	if result.DigestSha256 == "failed" || result.DigestSha512 == "failed" {
		log.Printf("Controller: existing file %s checksum mismatch, will re-download", filename)
		return nil
	}

	return result
}

// discoverChecksums looks for checksum files in parent directories
func (c *Controller) discoverChecksums(fileURL string) map[string]map[string]string {
	checksums := make(map[string]map[string]string) // type -> filename -> hash

	u, err := url.Parse(fileURL)
	if err != nil {
		return checksums
	}

	// Try current directory, then 1 level up, then 2 levels up
	dirs := []string{
		path.Dir(u.Path),
		path.Dir(path.Dir(u.Path)),
		path.Dir(path.Dir(path.Dir(u.Path))),
	}

	checksumFiles := []struct {
		name     string
		hashType string
	}{
		{"SHA256SUMS", "sha256"},
		{"SHA512SUMS", "sha512"},
	}

	for _, dir := range dirs {
		for _, cf := range checksumFiles {
			checksumURL := fmt.Sprintf("%s://%s%s/%s", u.Scheme, u.Host, dir, cf.name)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
			if err != nil {
				cancel()
				continue
			}

			resp, err := http.DefaultClient.Do(req)
			cancel()
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil {
					resp.Body.Close()
				}
				continue
			}

			// Parse checksum file
			parsed := parseChecksumFile(resp.Body)
			resp.Body.Close()

			if len(parsed) > 0 {
				if checksums[cf.hashType] == nil {
					checksums[cf.hashType] = make(map[string]string)
				}
				for k, v := range parsed {
					checksums[cf.hashType][k] = v
				}
			}
		}
	}

	return checksums
}

// parseChecksumFile parses a checksum file (SHA256SUMS format)
func parseChecksumFile(r io.Reader) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Format: "hash  filename" (text mode) or "hash *filename" (binary mode)
		var hash, filename string

		if i := strings.Index(line, "  "); i != -1 {
			// Text mode: "hash  filename"
			hash = strings.TrimSpace(line[:i])
			filename = strings.TrimSpace(line[i+2:])
		} else if i := strings.Index(line, " *"); i != -1 {
			// Binary mode: "hash *filename"
			hash = strings.TrimSpace(line[:i])
			filename = strings.TrimSpace(line[i+2:])
		}

		if hash == "" || filename == "" {
			continue
		}

		filename = strings.TrimPrefix(filename, "./")
		result[filename] = hash
		// Also store with path variations
		result[filepath.Base(filename)] = hash
	}

	return result
}

// verifyChecksum verifies a hash against discovered checksums
func verifyChecksum(checksums map[string]map[string]string, hashType string, h hash.Hash, filename string) string {
	typeChecksums, ok := checksums[hashType]
	if !ok {
		return "not_found"
	}

	expected, ok := typeChecksums[filename]
	if !ok {
		// Try just the base filename
		expected, ok = typeChecksums[filepath.Base(filename)]
		if !ok {
			return "not_found"
		}
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	if strings.EqualFold(actual, expected) {
		return "verified"
	}

	log.Printf("Controller: %s checksum mismatch for %s: expected %s, got %s", hashType, filename, expected, actual)
	return "failed"
}
