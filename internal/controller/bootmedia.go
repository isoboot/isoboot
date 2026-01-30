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

	"github.com/isoboot/isoboot/internal/iso"
	"github.com/isoboot/isoboot/internal/k8s"
)

// downloadRequestTimeout is the timeout for the entire download operation.
const downloadRequestTimeout = 15 * time.Minute

// checksumDiscoveryTimeout is the timeout for fetching checksum files.
const checksumDiscoveryTimeout = 30 * time.Second

// reconcileBootMedias reconciles all BootMedia resources
func (c *Controller) reconcileBootMedias(ctx context.Context) {
	bootMedias, err := c.k8sClient.ListBootMedias(ctx)
	if err != nil {
		log.Printf("Controller: failed to list bootmedias: %v", err)
		return
	}

	for _, bm := range bootMedias {
		c.reconcileBootMedia(ctx, bm)
	}
}

// reconcileBootMedia reconciles a single BootMedia
func (c *Controller) reconcileBootMedia(ctx context.Context, bm *k8s.BootMedia) {
	// Initialize status if empty
	if bm.Status.Phase == "" {
		log.Printf("Controller: initializing BootMedia %s status to Pending", bm.Name)
		status := &k8s.BootMediaStatus{
			Phase:   "Pending",
			Message: "Waiting for download",
		}
		if err := c.k8sClient.UpdateBootMediaStatus(ctx, bm.Name, status); err != nil {
			log.Printf("Controller: failed to initialize BootMedia %s: %v", bm.Name, err)
		}
		return
	}

	// If already Complete or Failed, nothing to do
	if bm.Status.Phase == "Complete" || bm.Status.Phase == "Failed" {
		return
	}

	// If Pending or Downloading, ensure download is running
	if bm.Status.Phase == "Pending" || bm.Status.Phase == "Downloading" {
		if _, alreadyRunning := c.activeBootMediaDownloads.LoadOrStore(bm.Name, true); alreadyRunning {
			return
		}
		go c.downloadBootMedia(ctx, bm)
	}
}

// downloadBootMedia orchestrates downloading all files for a BootMedia
func (c *Controller) downloadBootMedia(parentCtx context.Context, bm *k8s.BootMedia) {
	defer c.activeBootMediaDownloads.Delete(bm.Name)

	statusCtx := context.Background()

	if c.filesBasePath == "" {
		c.failBootMedia(statusCtx, bm.Name, "Controller filesBasePath not configured")
		return
	}

	// Validate spec
	if err := bm.Validate(); err != nil {
		c.failBootMedia(statusCtx, bm.Name, fmt.Sprintf("Invalid spec: %v", err))
		return
	}

	bmDir := filepath.Join(c.filesBasePath, bm.Name)
	hasFirmware := bm.HasFirmware()

	// Initialize status
	status := initDownloadStatus(bm)
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s to Downloading: %v", bm.Name, err)
		return
	}

	// Dispatch to the appropriate download flow
	if bm.ISO != nil {
		c.downloadBootMediaISO(parentCtx, bm, status, bmDir, hasFirmware)
	} else {
		c.downloadBootMediaDirect(parentCtx, bm, status, bmDir, hasFirmware)
	}
}

// initDownloadStatus creates initial status with Pending entries for each file
func initDownloadStatus(bm *k8s.BootMedia) *k8s.BootMediaStatus {
	status := &k8s.BootMediaStatus{
		Phase:   "Downloading",
		Message: "Starting downloads",
	}

	if bm.Kernel != nil {
		name, _ := k8s.FilenameFromURL(bm.Kernel.URL)
		status.Kernel = &k8s.FileStatus{Name: name, Phase: "Pending"}
	}
	if bm.Initrd != nil {
		name, _ := k8s.FilenameFromURL(bm.Initrd.URL)
		status.Initrd = &k8s.FileStatus{Name: name, Phase: "Pending"}
	}
	if bm.ISO != nil {
		name, _ := k8s.FilenameFromURL(bm.ISO.URL)
		status.ISO = &k8s.FileStatus{Name: name, Phase: "Pending"}
		status.Kernel = &k8s.FileStatus{Name: path.Base(bm.ISO.Kernel), Phase: "Pending"}
		status.Initrd = &k8s.FileStatus{Name: path.Base(bm.ISO.Initrd), Phase: "Pending"}
	}
	if bm.Firmware != nil {
		name, _ := k8s.FilenameFromURL(bm.Firmware.URL)
		status.Firmware = &k8s.FileStatus{Name: name, Phase: "Pending"}
		status.FirmwareInitrd = &k8s.FileStatus{Name: bm.InitrdFilename(), Phase: "Pending"}
	}

	return status
}

// downloadBootMediaDirect downloads kernel and initrd directly from URLs
func (c *Controller) downloadBootMediaDirect(parentCtx context.Context, bm *k8s.BootMedia, status *k8s.BootMediaStatus, bmDir string, hasFirmware bool) {
	statusCtx := context.Background()

	// Download kernel -> {bmDir}/{kernelFilename}
	kernelFilename := bm.KernelFilename()
	kernelDest := filepath.Join(bmDir, kernelFilename)

	status.Kernel.Phase = "Downloading"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	dlCtx, cancel := context.WithTimeout(parentCtx, downloadRequestTimeout)
	sha, err := c.downloadFile(dlCtx, bm.Kernel.URL, bm.Kernel.ChecksumURL, kernelDest)
	cancel()
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Kernel, fmt.Sprintf("Failed to download kernel: %v", err))
		return
	}
	status.Kernel.Phase = "Complete"
	status.Kernel.SHA256 = sha

	// Download initrd
	initrdFilename := bm.InitrdFilename()
	var initrdDest string
	if hasFirmware {
		initrdDest = filepath.Join(bmDir, "no-firmware", initrdFilename)
	} else {
		initrdDest = filepath.Join(bmDir, initrdFilename)
	}

	status.Initrd.Phase = "Downloading"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	dlCtx, cancel = context.WithTimeout(parentCtx, downloadRequestTimeout)
	sha, err = c.downloadFile(dlCtx, bm.Initrd.URL, bm.Initrd.ChecksumURL, initrdDest)
	cancel()
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Initrd, fmt.Sprintf("Failed to download initrd: %v", err))
		return
	}
	status.Initrd.Phase = "Complete"
	status.Initrd.SHA256 = sha

	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	// Download and concatenate firmware if present
	if hasFirmware {
		c.downloadAndConcatenateFirmware(parentCtx, bm, status, bmDir)
		if status.Phase == "Failed" {
			return
		}
	}

	// All done
	status.Phase = "Complete"
	status.Message = "All files downloaded"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s to Complete: %v", bm.Name, err)
	}
	log.Printf("Controller: BootMedia %s download complete", bm.Name)
}

// downloadBootMediaISO downloads an ISO and extracts kernel/initrd from it
func (c *Controller) downloadBootMediaISO(parentCtx context.Context, bm *k8s.BootMedia, status *k8s.BootMediaStatus, bmDir string, hasFirmware bool) {
	statusCtx := context.Background()

	// Download ISO to temp directory
	tmpDir, err := os.MkdirTemp("", "isoboot-iso-*")
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.ISO, fmt.Sprintf("Failed to create temp dir: %v", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	isoFilename, _ := k8s.FilenameFromURL(bm.ISO.URL)
	isoDest := filepath.Join(tmpDir, isoFilename)

	status.ISO.Phase = "Downloading"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	dlCtx, cancel := context.WithTimeout(parentCtx, downloadRequestTimeout)
	sha, err := c.downloadFile(dlCtx, bm.ISO.URL, bm.ISO.ChecksumURL, isoDest)
	cancel()
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.ISO, fmt.Sprintf("Failed to download ISO: %v", err))
		return
	}
	status.ISO.Phase = "Complete"
	status.ISO.SHA256 = sha

	// Open ISO and extract files
	isoReader, err := iso.OpenISO9660(isoDest)
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.ISO, fmt.Sprintf("Failed to open ISO: %v", err))
		return
	}
	defer isoReader.Close()

	// Extract kernel
	status.Kernel.Phase = "Extracting"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	kernelData, err := isoReader.ReadFile(bm.ISO.Kernel)
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Kernel, fmt.Sprintf("Failed to extract kernel from ISO: %v", err))
		return
	}

	kernelFilename := path.Base(bm.ISO.Kernel)
	kernelDest := filepath.Join(bmDir, kernelFilename)
	sha, err = writeFileAtomic(kernelDest, kernelData)
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Kernel, fmt.Sprintf("Failed to write kernel: %v", err))
		return
	}
	status.Kernel.Phase = "Complete"
	status.Kernel.SHA256 = sha

	// Extract initrd
	status.Initrd.Phase = "Extracting"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	initrdData, err := isoReader.ReadFile(bm.ISO.Initrd)
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Initrd, fmt.Sprintf("Failed to extract initrd from ISO: %v", err))
		return
	}

	initrdFilename := path.Base(bm.ISO.Initrd)
	var initrdDest string
	if hasFirmware {
		initrdDest = filepath.Join(bmDir, "no-firmware", initrdFilename)
	} else {
		initrdDest = filepath.Join(bmDir, initrdFilename)
	}
	sha, err = writeFileAtomic(initrdDest, initrdData)
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Initrd, fmt.Sprintf("Failed to write initrd: %v", err))
		return
	}
	status.Initrd.Phase = "Complete"
	status.Initrd.SHA256 = sha

	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	// Download and concatenate firmware if present
	if hasFirmware {
		c.downloadAndConcatenateFirmware(parentCtx, bm, status, bmDir)
		if status.Phase == "Failed" {
			return
		}
	}

	// All done
	status.Phase = "Complete"
	status.Message = "All files downloaded and extracted"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s to Complete: %v", bm.Name, err)
	}
	log.Printf("Controller: BootMedia %s download complete", bm.Name)
}

// downloadAndConcatenateFirmware downloads firmware and concatenates it with the initrd
func (c *Controller) downloadAndConcatenateFirmware(parentCtx context.Context, bm *k8s.BootMedia, status *k8s.BootMediaStatus, bmDir string) {
	statusCtx := context.Background()

	// Download firmware to temp directory
	tmpDir, err := os.MkdirTemp("", "isoboot-fw-*")
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Firmware, fmt.Sprintf("Failed to create temp dir: %v", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	fwFilename, _ := k8s.FilenameFromURL(bm.Firmware.URL)
	fwDest := filepath.Join(tmpDir, fwFilename)

	status.Firmware.Phase = "Downloading"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	dlCtx, cancel := context.WithTimeout(parentCtx, downloadRequestTimeout)
	sha, err := c.downloadFile(dlCtx, bm.Firmware.URL, bm.Firmware.ChecksumURL, fwDest)
	cancel()
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.Firmware, fmt.Sprintf("Failed to download firmware: %v", err))
		return
	}
	status.Firmware.Phase = "Complete"
	status.Firmware.SHA256 = sha

	// Concatenate: no-firmware/initrd + firmware -> with-firmware/initrd
	initrdFilename := bm.InitrdFilename()
	noFwInitrd := filepath.Join(bmDir, "no-firmware", initrdFilename)
	withFwInitrd := filepath.Join(bmDir, "with-firmware", initrdFilename)

	status.FirmwareInitrd.Phase = "Building"
	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}

	sha, err = concatenateFiles(withFwInitrd, noFwInitrd, fwDest)
	if err != nil {
		c.failBootMediaStatus(statusCtx, bm.Name, status, status.FirmwareInitrd, fmt.Sprintf("Failed to build firmware initrd: %v", err))
		return
	}
	status.FirmwareInitrd.Phase = "Complete"
	status.FirmwareInitrd.SHA256 = sha

	if err := c.k8sClient.UpdateBootMediaStatus(statusCtx, bm.Name, status); err != nil {
		log.Printf("Controller: failed to update BootMedia %s status: %v", bm.Name, err)
	}
}

// failBootMedia sets the BootMedia to Failed with the given message
func (c *Controller) failBootMedia(ctx context.Context, name, message string) {
	status := &k8s.BootMediaStatus{
		Phase:   "Failed",
		Message: message,
	}
	if updateErr := c.k8sClient.UpdateBootMediaStatus(ctx, name, status); updateErr != nil {
		log.Printf("Controller: failed to update BootMedia %s to Failed: %v", name, updateErr)
	}
}

// failBootMediaStatus sets a file status to Failed and the overall status to Failed
func (c *Controller) failBootMediaStatus(ctx context.Context, name string, status *k8s.BootMediaStatus, fileStatus *k8s.FileStatus, message string) {
	status.Phase = "Failed"
	status.Message = message
	if fileStatus != nil {
		fileStatus.Phase = "Failed"
	}
	if updateErr := c.k8sClient.UpdateBootMediaStatus(ctx, name, status); updateErr != nil {
		log.Printf("Controller: failed to update BootMedia %s to Failed: %v", name, updateErr)
	}
}

// writeFileAtomic writes data to destPath atomically and returns the SHA256 hash
func writeFileAtomic(destPath string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	tmpPath := destPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}

	h := sha256.Sum256(data)
	sha := fmt.Sprintf("%x", h[:])

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("rename: %w", err)
	}

	return sha, nil
}

// concatenateFiles concatenates source files into destPath and returns the SHA256 hash
func concatenateFiles(destPath string, sources ...string) (string, error) {
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

	for _, src := range sources {
		f, err := os.Open(src)
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
	return sha, nil
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
				key := checksumKey(fileURL, checksumURL)
				if expected, ok := checksums[key]; ok {
					if strings.EqualFold(sha, expected) {
						log.Printf("Controller: existing file %s matches checksum, skipping download", filepath.Base(destPath))
						return sha, nil
					}
					log.Printf("Controller: existing file %s checksum mismatch, re-downloading", filepath.Base(destPath))
				} else {
					log.Printf("Controller: no checksum entry for %s in %s, re-downloading", key, checksumURL)
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
		key := checksumKey(fileURL, checksumURL)
		if expected, ok := checksums[key]; ok {
			if !strings.EqualFold(sha, expected) {
				os.Remove(tmpPath)
				return "", fmt.Errorf("checksum mismatch: expected %s, got %s", truncHash(expected), truncHash(sha))
			}
			log.Printf("Controller: checksum verified for %s", key)
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	log.Printf("Controller: downloaded %s (%d bytes, sha256=%s)", filepath.Base(destPath), written, sha[:16]+"...")
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

// checksumKey computes the key to look up in the parsed checksums map.
// It calculates the file's path relative to the checksum file's directory.
// Example: checksumURL "https://host/images/SHA256SUMS" + fileURL
// "https://host/images/netboot/amd64/linux" → "netboot/amd64/linux"
func checksumKey(fileURL, checksumURL string) string {
	fu, err := url.Parse(fileURL)
	if err != nil {
		return path.Base(fileURL)
	}
	cu, err := url.Parse(checksumURL)
	if err != nil {
		return path.Base(fu.Path)
	}
	checksumDir := path.Dir(cu.Path) + "/"
	if strings.HasPrefix(fu.Path, checksumDir) {
		return strings.TrimPrefix(fu.Path, checksumDir)
	}
	return path.Base(fu.Path)
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
