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

// downloadRequestTimeout is the timeout for the entire download operation.
const downloadRequestTimeout = 15 * time.Minute

// checksumDiscoveryTimeout is the timeout for fetching checksum files.
const checksumDiscoveryTimeout = 30 * time.Second

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
				DigestSha256:  "pending",
				DigestSha512:  "pending",
			},
		}
		if di.Firmware != "" {
			status.Firmware = &k8s.DiskImageVerification{
				FileSizeMatch: "pending",
				DigestSha256:  "pending",
				DigestSha512:  "pending",
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
		if _, alreadyRunning := c.activeDiskImageDownloads.LoadOrStore(di.Name, true); alreadyRunning {
			return
		}
		// Pass parent context so downloads can be cancelled during shutdown
		go c.downloadDiskImage(ctx, di)
	}
}

// downloadDiskImage downloads and verifies a DiskImage
func (c *Controller) downloadDiskImage(parentCtx context.Context, di *k8s.DiskImage) {
	// Clean up activeDiskImageDownloads tracking when done
	defer c.activeDiskImageDownloads.Delete(di.Name)

	// Create a context with timeout for download operations (HTTP requests)
	downloadCtx, cancel := context.WithTimeout(parentCtx, downloadRequestTimeout)
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
			DigestSha256:  "pending",
			DigestSha512:  "pending",
		},
	}
	if di.Firmware != "" {
		status.Firmware = &k8s.DiskImageVerification{
			FileSizeMatch: "pending",
			DigestSha256:  "pending",
			DigestSha512:  "pending",
		}
	}
	if err := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); err != nil {
		log.Printf("Controller: failed to update DiskImage %s to Downloading: %v", di.Name, err)
		return
	}

	// Validate isoBasePath is set
	if c.isoBasePath == "" {
		status.Phase = "Failed"
		status.Message = "Controller isoBasePath not configured"
		if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
			log.Printf("Controller: failed to update DiskImage %s to Failed: %v", di.Name, updateErr)
		}
		return
	}

	// Download ISO
	isoFilename, err := filenameFromURL(di.ISO)
	if err != nil {
		status.Phase = "Failed"
		status.Message = fmt.Sprintf("Invalid ISO URL: %v", err)
		if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
			log.Printf("Controller: failed to update DiskImage %s to Failed: %v", di.Name, updateErr)
		}
		return
	}
	isoPath := filepath.Join(c.isoBasePath, di.Name, isoFilename)
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
			DigestSha256:  "pending",
			DigestSha512:  "pending",
		}
		if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
			log.Printf("Controller: failed to update DiskImage %s firmware progress: %v", di.Name, updateErr)
		}

		fwFilename, err := filenameFromURL(di.Firmware)
		if err != nil {
			status.Phase = "Failed"
			status.Message = fmt.Sprintf("Invalid firmware URL: %v", err)
			if updateErr := c.k8sClient.UpdateDiskImageStatus(statusCtx, di.Name, status); updateErr != nil {
				log.Printf("Controller: failed to update DiskImage %s to Failed: %v", di.Name, updateErr)
			}
			return
		}
		fwPath := filepath.Join(c.isoBasePath, di.Name, "firmware", fwFilename)
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
		DigestSha256:  "pending",
		DigestSha512:  "pending",
	}

	// Create parent directory with restricted permissions
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
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
	defer headResp.Body.Close()

	// Check HEAD response status
	var expectedSize int64
	if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
		expectedSize = headResp.ContentLength
	} else if headResp.StatusCode >= 400 {
		// Fail immediately on client/server errors
		result.FileSizeMatch = "failed"
		return result, fmt.Errorf("HEAD request returned %d for %s", headResp.StatusCode, fileURL)
	} else {
		// 3xx redirects - proceed without Content-Length
		log.Printf("Controller: HEAD request for %s returned %d, will not use Content-Length", fileURL, headResp.StatusCode)
	}

	// Try to find checksums (uses its own per-request timeouts internally)
	checksums := c.discoverChecksums(ctx, fileURL)

	// Check if the file already exists and is valid.
	// verifyExistingFile returns nil to trigger re-download when verification is not possible.
	if existingResult := c.verifyExistingFile(destPath, expectedSize, checksums, fileURL); existingResult != nil {
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

	// Create temp file with restricted permissions
	tmpPath := destPath + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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

	// Verify checksums using full URL for progressive path matching
	result.DigestSha256 = verifyChecksum(checksums, "sha256", sha256Hash, fileURL)
	result.DigestSha512 = verifyChecksum(checksums, "sha512", sha512Hash, fileURL)

	// If any checksum verification explicitly failed or had multiple matches, don't persist the file
	var failedDigests []string
	var multipleMatches []string
	if result.DigestSha256 == "failed" {
		failedDigests = append(failedDigests, "SHA256")
	} else if result.DigestSha256 == "multiple_matches" {
		multipleMatches = append(multipleMatches, "SHA256")
	}
	if result.DigestSha512 == "failed" {
		failedDigests = append(failedDigests, "SHA512")
	} else if result.DigestSha512 == "multiple_matches" {
		multipleMatches = append(multipleMatches, "SHA512")
	}
	if len(failedDigests) > 0 {
		if removeErr := os.Remove(tmpPath); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("Controller: failed to remove temporary file %s after checksum failure: %v", tmpPath, removeErr)
		}
		return result, fmt.Errorf("%s checksum verification failed", strings.Join(failedDigests, " and "))
	}
	if len(multipleMatches) > 0 {
		if removeErr := os.Remove(tmpPath); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("Controller: failed to remove temporary file %s after multiple checksum matches: %v", tmpPath, removeErr)
		}
		return result, fmt.Errorf("%s checksum has multiple matches in checksum file, cannot verify", strings.Join(multipleMatches, " and "))
	}

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
func (c *Controller) verifyExistingFile(filePath string, expectedSize int64, checksums map[string]map[string]string, fileURL string) *k8s.DiskImageVerification {
	// Check if file exists
	info, err := os.Stat(filePath)
	if err != nil {
		return nil // File doesn't exist, need to download
	}

	// Check size
	if expectedSize > 0 && info.Size() != expectedSize {
		log.Printf("Controller: existing file %s size mismatch (expected %d, got %d), will re-download", fileURL, expectedSize, info.Size())
		return nil // Size mismatch, need to download
	}

	// If no checksums are available, we cannot securely verify file contents.
	// Return nil to trigger re-download rather than relying on size-only verification.
	if len(checksums) == 0 {
		log.Printf("Controller: existing file %s cannot be securely verified (no checksums available), will re-download", fileURL)
		return nil
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

	// Verify checksums using full URL for progressive path matching
	result := &k8s.DiskImageVerification{
		FileSizeMatch: "verified",
		DigestSha256:  verifyChecksum(checksums, "sha256", sha256Hash, fileURL),
		DigestSha512:  verifyChecksum(checksums, "sha512", sha512Hash, fileURL),
	}

	// If any checksum verification failed or had multiple matches, need to re-download
	if result.DigestSha256 == "failed" || result.DigestSha512 == "failed" {
		log.Printf("Controller: existing file %s checksum mismatch, will re-download", fileURL)
		return nil
	}
	if result.DigestSha256 == "multiple_matches" || result.DigestSha512 == "multiple_matches" {
		log.Printf("Controller: existing file %s has multiple checksum matches, will re-download", fileURL)
		return nil
	}

	return result
}

// discoverChecksums looks for checksum files in parent directories
func (c *Controller) discoverChecksums(ctx context.Context, fileURL string) map[string]map[string]string {
	checksums := make(map[string]map[string]string) // type -> filename -> hash

	u, err := url.Parse(fileURL)
	if err != nil {
		return checksums
	}

	// Try current directory, then 1 level up, then 2 levels up
	// Deduplicate directories (path.Dir("/") stays "/")
	dirs := []string{}
	seen := make(map[string]bool)
	dir := path.Dir(u.Path)
	for i := 0; i < 3; i++ {
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
		if dir == "/" {
			break // Stop at root
		}
		dir = path.Dir(dir)
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

			// Use an inner function so defer cancel() runs per iteration; without this, defers in the loop
			// body would only execute when discoverChecksums() returns.
			parsed := func() map[string]string {
				reqCtx, cancel := context.WithTimeout(ctx, checksumDiscoveryTimeout)
				defer cancel()

				req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, checksumURL, nil)
				if err != nil {
					return nil
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil || resp.StatusCode != http.StatusOK {
					if resp != nil {
						resp.Body.Close()
					}
					return nil
				}
				defer resp.Body.Close()

				return parseChecksumFile(resp.Body)
			}()

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

// parseChecksumFile parses a checksum file (SHA256SUMS format).
// Returns a map of path to hash. Only stores full paths as keys to allow
// progressive matching when multiple files share the same base filename.
// On scanner error, returns partial results parsed so far (may be empty).
func parseChecksumFile(r io.Reader) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Format: "hash  filename" (text mode) or "hash *filename" (binary mode).
		// We use strings.Index instead of strings.Fields, because strings.Fields splits on
		// any whitespace and would incorrectly break filenames that contain spaces.
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

		// Skip lines that don't match either format
		if hash == "" || filename == "" {
			continue
		}

		filename = strings.TrimPrefix(filename, "./")
		result[filename] = hash
	}

	// If scanner encountered an error, log it and return partial results
	if err := scanner.Err(); err != nil {
		log.Printf("Controller: error scanning checksum file (returning partial results): %v", err)
	}

	return result
}

// findChecksumByPath finds a checksum using progressive path matching.
// It starts with just the filename, then progressively adds path components
// from the URL until exactly one match is found or all components are exhausted.
// Returns (hash, "found"), (hash, "multiple"), or ("", "not_found").
func findChecksumByPath(checksums map[string]string, fileURL string) (string, string) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return "", "not_found"
	}

	// Split path into components, e.g., "/debian/dists/.../netboot/mini.iso"
	// becomes ["debian", "dists", ..., "netboot", "mini.iso"]
	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(pathParts) == 0 {
		return "", "not_found"
	}

	// Try progressively longer suffixes: "mini.iso", "netboot/mini.iso", etc.
	for numParts := 1; numParts <= len(pathParts); numParts++ {
		suffix := strings.Join(pathParts[len(pathParts)-numParts:], "/")

		var matches []string
		for path, hash := range checksums {
			// Match if path equals suffix or ends with /suffix
			if path == suffix || strings.HasSuffix(path, "/"+suffix) {
				matches = append(matches, hash)
			}
		}

		if len(matches) == 1 {
			return matches[0], "found"
		}
		if len(matches) == 0 {
			continue // Try more components
		}
		// Multiple matches - if we've tried 3+ components, give up
		if numParts >= 3 {
			return "", "multiple"
		}
		// Otherwise try more components to disambiguate
	}

	return "", "not_found"
}

// formatHashMismatch returns a human-readable comparison of expected vs actual hash.
// Shows first 4 or last 4 hex chars with ellipsis to help identify the mismatch.
func formatHashMismatch(expected, actual string) string {
	if len(expected) < 8 || len(actual) < 8 {
		return fmt.Sprintf("expected %s, got %s", expected, actual)
	}

	expFirst4 := expected[:4]
	actFirst4 := actual[:4]
	expLast4 := expected[len(expected)-4:]
	actLast4 := actual[len(actual)-4:]

	if expFirst4 != actFirst4 {
		// First 4 differ - show "expected abcd..., got 1234..."
		return fmt.Sprintf("expected %s..., got %s...", expFirst4, actFirst4)
	}
	if expLast4 != actLast4 {
		// Last 4 differ - show "expected ...aabb, got ...ccdd"
		return fmt.Sprintf("expected ...%s, got ...%s", expLast4, actLast4)
	}
	// Both ends same, middle differs
	return "hash mismatch"
}

// verifyChecksum verifies a hash against discovered checksums.
// Returns "verified", "not_found", "multiple_matches", or "failed".
func verifyChecksum(checksums map[string]map[string]string, hashType string, h hash.Hash, fileURL string) string {
	typeChecksums, ok := checksums[hashType]
	if !ok {
		return "not_found"
	}

	expected, status := findChecksumByPath(typeChecksums, fileURL)
	if status == "not_found" {
		return "not_found"
	}
	if status == "multiple" {
		log.Printf("Controller: %s multiple checksums match %s, cannot verify", hashType, fileURL)
		return "multiple_matches"
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	if strings.EqualFold(actual, expected) {
		return "verified"
	}

	log.Printf("Controller: %s checksum mismatch for %s: %s", hashType, fileURL, formatHashMismatch(expected, actual))
	return "failed"
}

// filenameFromURL extracts the filename from a URL, handling query strings
func filenameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	filename := filepath.Base(u.Path)
	// filepath.Base returns "." for empty paths or paths ending in "/",
	// and "/" when the path is exactly "/"
	if filename == "." || filename == "/" {
		return "", fmt.Errorf("URL has no filename: %s", rawURL)
	}
	return filename, nil
}
