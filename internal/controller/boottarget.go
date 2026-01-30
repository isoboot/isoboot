package controller

import (
	"bufio"
	"context"
	"crypto/sha256"
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

// reconcileBootTargets reconciles all BootTarget resources
func (c *Controller) reconcileBootTargets(ctx context.Context) {
	bootTargets, err := c.k8sClient.ListBootTargets(ctx)
	if err != nil {
		log.Printf("Controller: failed to list boottargets: %v", err)
		return
	}

	for _, bt := range bootTargets {
		c.reconcileBootTarget(ctx, bt)
	}
}

// reconcileBootTarget reconciles a single BootTarget
func (c *Controller) reconcileBootTarget(ctx context.Context, bt *k8s.BootTarget) {
	// Initialize status if empty
	if bt.Status.Phase == "" {
		log.Printf("Controller: initializing BootTarget %s status to Pending", bt.Name)
		status := &k8s.BootTargetStatus{
			Phase:   "Pending",
			Message: "Waiting for download",
		}
		if err := c.k8sClient.UpdateBootTargetStatus(ctx, bt.Name, status); err != nil {
			log.Printf("Controller: failed to initialize BootTarget %s: %v", bt.Name, err)
		}
		return
	}

	// If already Complete or Failed, nothing to do
	if bt.Status.Phase == "Complete" || bt.Status.Phase == "Failed" {
		return
	}

	// If Pending or Downloading, ensure download is running
	if bt.Status.Phase == "Pending" || bt.Status.Phase == "Downloading" {
		if _, alreadyRunning := c.activeBootTargetDownloads.LoadOrStore(bt.Name, true); alreadyRunning {
			return
		}
		go c.downloadBootTarget(ctx, bt)
	}
}

// downloadBootTarget downloads all files for a BootTarget and builds combined files
func (c *Controller) downloadBootTarget(parentCtx context.Context, bt *k8s.BootTarget) {
	defer c.activeBootTargetDownloads.Delete(bt.Name)

	statusCtx := context.Background()

	if c.filesBasePath == "" {
		status := &k8s.BootTargetStatus{
			Phase:   "Failed",
			Message: "Controller filesBasePath not configured",
		}
		if updateErr := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); updateErr != nil {
			log.Printf("Controller: failed to update BootTarget %s to Failed: %v", bt.Name, updateErr)
		}
		return
	}

	btDir := filepath.Join(c.filesBasePath, bt.Name)

	// Initialize status with file entries
	status := &k8s.BootTargetStatus{
		Phase:   "Downloading",
		Message: "Starting downloads",
	}
	for _, f := range bt.Files {
		fname, err := filenameFromURL(f.URL)
		if err != nil {
			status.Phase = "Failed"
			status.Message = fmt.Sprintf("Invalid URL: %v", err)
			c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status)
			return
		}
		status.Files = append(status.Files, k8s.FileStatus{
			Name:  fname,
			Phase: "Pending",
		})
	}
	for _, cf := range bt.CombinedFiles {
		status.CombinedFiles = append(status.CombinedFiles, k8s.FileStatus{
			Name:  cf.Name,
			Phase: "Pending",
		})
	}
	if err := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); err != nil {
		log.Printf("Controller: failed to update BootTarget %s to Downloading: %v", bt.Name, err)
		return
	}

	// Download each file
	for i, f := range bt.Files {
		fname, _ := filenameFromURL(f.URL)
		destPath := filepath.Join(btDir, fname)

		status.Files[i].Phase = "Downloading"
		if err := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); err != nil {
			log.Printf("Controller: failed to update BootTarget %s status: %v", bt.Name, err)
		}

		dlCtx, cancel := context.WithTimeout(parentCtx, downloadRequestTimeout)
		sha, err := c.downloadFile(dlCtx, f.URL, f.ChecksumURL, destPath)
		cancel()

		if err != nil {
			status.Phase = "Failed"
			status.Message = fmt.Sprintf("Failed to download %s: %v", fname, err)
			status.Files[i].Phase = "Failed"
			if updateErr := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); updateErr != nil {
				log.Printf("Controller: failed to update BootTarget %s to Failed: %v", bt.Name, updateErr)
			}
			return
		}

		status.Files[i].Phase = "Complete"
		status.Files[i].SHA256 = sha
		if err := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); err != nil {
			log.Printf("Controller: failed to update BootTarget %s status: %v", bt.Name, err)
		}
	}

	// Build combined files
	for i, cf := range bt.CombinedFiles {
		status.CombinedFiles[i].Phase = "Building"
		if err := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); err != nil {
			log.Printf("Controller: failed to update BootTarget %s status: %v", bt.Name, err)
		}

		destPath := filepath.Join(btDir, cf.Name)
		sha, err := c.buildCombinedFile(btDir, cf, destPath)
		if err != nil {
			status.Phase = "Failed"
			status.Message = fmt.Sprintf("Failed to build %s: %v", cf.Name, err)
			status.CombinedFiles[i].Phase = "Failed"
			if updateErr := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); updateErr != nil {
				log.Printf("Controller: failed to update BootTarget %s to Failed: %v", bt.Name, updateErr)
			}
			return
		}

		status.CombinedFiles[i].Phase = "Complete"
		status.CombinedFiles[i].SHA256 = sha
		if err := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); err != nil {
			log.Printf("Controller: failed to update BootTarget %s status: %v", bt.Name, err)
		}
	}

	// All done
	status.Phase = "Complete"
	status.Message = "All files downloaded and combined"
	if err := c.k8sClient.UpdateBootTargetStatus(statusCtx, bt.Name, status); err != nil {
		log.Printf("Controller: failed to update BootTarget %s to Complete: %v", bt.Name, err)
	}
	log.Printf("Controller: BootTarget %s download complete", bt.Name)
}

// downloadFile downloads a single file, optionally verifying checksums
func (c *Controller) downloadFile(ctx context.Context, fileURL, checksumURL, destPath string) (string, error) {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	// Discover checksums if checksumURL is provided
	var checksums map[string]string
	if checksumURL != "" {
		checksums = c.fetchChecksumFile(ctx, checksumURL)
	}

	// Check if file already exists
	if _, err := os.Stat(destPath); err == nil {
		sha, err := computeSHA256(destPath)
		if err == nil {
			if checksums != nil {
				fname, _ := filenameFromURL(fileURL)
				if expected, ok := lookupChecksum(checksums, fname); ok {
					if strings.EqualFold(sha, expected) {
						log.Printf("Controller: existing file %s matches checksum, skipping download", filepath.Base(destPath))
						return sha, nil
					}
					log.Printf("Controller: existing file %s checksum mismatch, re-downloading", filepath.Base(destPath))
				} else {
					log.Printf("Controller: no checksum entry for existing file %s, re-downloading", filepath.Base(destPath))
				}
			} else {
				log.Printf("Controller: existing file %s verified (no remote checksum), skipping download", filepath.Base(destPath))
				return sha, nil
			}
		}
		// Can't read existing file or checksum mismatch, re-download
	}

	// Download file
	log.Printf("Controller: downloading %s", fileURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Write to temp file while computing hash
	tmpPath := destPath + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	h := sha256.New()
	multiWriter := io.MultiWriter(tmpFile, h)

	written, err := io.Copy(multiWriter, resp.Body)
	if cerr := tmpFile.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("close temp file: %w", cerr)
	}
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}

	sha := fmt.Sprintf("%x", h.Sum(nil))

	// Verify checksum if available
	if checksums != nil {
		fname, _ := filenameFromURL(fileURL)
		if expected, ok := lookupChecksum(checksums, fname); ok {
			if !strings.EqualFold(sha, expected) {
				os.Remove(tmpPath)
				return "", fmt.Errorf("checksum mismatch: expected %s, got %s", truncHash(expected), truncHash(sha))
			}
			log.Printf("Controller: checksum verified for %s", fname)
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	log.Printf("Controller: downloaded %s (%d bytes, sha256=%s)", filepath.Base(destPath), written, sha[:16]+"...")
	return sha, nil
}

// buildCombinedFile creates a combined file by concatenating source files
func (c *Controller) buildCombinedFile(baseDir string, cf k8s.CombinedFile, destPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	tmpPath := destPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("create output file: %w", err)
	}
	defer os.Remove(tmpPath)

	h := sha256.New()
	w := io.MultiWriter(out, h)

	for _, src := range cf.Sources {
		// Validate source name to prevent path traversal
		if src == "" || strings.ContainsAny(src, "/\\") || strings.Contains(src, "..") {
			if cerr := out.Close(); cerr != nil {
				log.Printf("Controller: close output file after invalid source %q: %v", src, cerr)
			}
			return "", fmt.Errorf("invalid source name %q", src)
		}
		srcPath := filepath.Join(baseDir, src)
		f, err := os.Open(srcPath)
		if err != nil {
			if cerr := out.Close(); cerr != nil {
				log.Printf("Controller: close output file after open error for %s: %v", src, cerr)
			}
			return "", fmt.Errorf("open source %s: %w", src, err)
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			if cerr := out.Close(); cerr != nil {
				log.Printf("Controller: close output file after copy error for %s: %v", src, cerr)
			}
			return "", fmt.Errorf("copy source %s: %w", src, err)
		}
	}

	if err := out.Close(); err != nil {
		return "", fmt.Errorf("close output file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	sha := fmt.Sprintf("%x", h.Sum(nil))
	log.Printf("Controller: built combined file %s (sha256=%s)", cf.Name, sha[:16]+"...")
	return sha, nil
}

// fetchChecksumFile downloads and parses a checksum file (SHA256SUMS format)
func (c *Controller) fetchChecksumFile(ctx context.Context, checksumURL string) map[string]string {
	reqCtx, cancel := context.WithTimeout(ctx, checksumDiscoveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return nil
	}

	resp, err := c.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	return parseChecksumFile(resp.Body)
}

// parseChecksumFile parses a checksum file (SHA256SUMS format).
func parseChecksumFile(r io.Reader) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var checksum, filename string
		if i := strings.Index(line, "  "); i != -1 {
			checksum = strings.TrimSpace(line[:i])
			filename = strings.TrimSpace(line[i+2:])
		} else if i := strings.Index(line, " *"); i != -1 {
			checksum = strings.TrimSpace(line[:i])
			filename = strings.TrimSpace(line[i+2:])
		}

		if checksum == "" || filename == "" {
			continue
		}

		filename = strings.TrimPrefix(filename, "./")
		result[filename] = checksum
	}

	return result
}

// lookupChecksum looks up a checksum by filename (tries basename and full path)
func lookupChecksum(checksums map[string]string, filename string) (string, bool) {
	if h, ok := checksums[filename]; ok {
		return h, true
	}
	basename := path.Base(filename)
	if h, ok := checksums[basename]; ok {
		return h, true
	}
	return "", false
}

// computeSHA256 computes the SHA256 hash of a file
func computeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// filenameFromURL extracts the filename from a URL
func filenameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	filename := filepath.Base(u.Path)
	if filename == "." || filename == "/" {
		return "", fmt.Errorf("URL has no filename: %s", rawURL)
	}
	return filename, nil
}

// truncHash returns the first 8 chars of a hash string with "..." suffix,
// or the full string if shorter than 8 chars.
func truncHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8] + "..."
}

// Keep hash.Hash import used by tests
var _ hash.Hash = sha256.New()
