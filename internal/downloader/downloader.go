package downloader

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	chunkSize       = 1024 * 1024 // 1MB chunks
	pollInterval    = 500 * time.Millisecond
	downloadTimeout = 30 * time.Minute
)

// Downloader handles ISO and firmware downloads with locking
type Downloader struct {
	mu    sync.Mutex
	locks map[string]chan struct{} // per-file completion channels
}

// New creates a new Downloader
func New() *Downloader {
	return &Downloader{
		locks: make(map[string]chan struct{}),
	}
}

// EnsureFile ensures a file exists, downloading if necessary
// Blocks until file is available
func (d *Downloader) EnsureFile(destPath, url string) error {
	// Fast path: file already exists
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}

	// Check if download is in progress or start one
	d.mu.Lock()

	// Double-check after lock
	if _, err := os.Stat(destPath); err == nil {
		d.mu.Unlock()
		return nil
	}

	// Check for existing download
	if ch, ok := d.locks[destPath]; ok {
		d.mu.Unlock()
		// Wait for existing download to complete
		<-ch
		// Check if it succeeded
		if _, err := os.Stat(destPath); err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		return nil
	}

	// Start new download
	ch := make(chan struct{})
	d.locks[destPath] = ch
	d.mu.Unlock()

	// Do the download
	err := d.download(destPath, url)

	// Signal completion
	d.mu.Lock()
	delete(d.locks, destPath)
	close(ch)
	d.mu.Unlock()

	return err
}

func (d *Downloader) download(destPath, url string) error {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	tmpPath := destPath + ".tmp"
	lockPath := destPath + ".downloading"

	// Create lock file (exclusive)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Another process is downloading, wait for it
			return d.waitForDownload(destPath, lockPath)
		}
		return fmt.Errorf("create lock file: %w", err)
	}
	lockFile.Close()
	defer os.Remove(lockPath)

	// Clean up any partial download
	os.Remove(tmpPath)

	// Start download
	fmt.Printf("Downloading %s from %s\n", filepath.Base(destPath), url)

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status: %s", resp.Status)
	}

	// Create temp file
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	// Download in chunks
	buf := make([]byte, chunkSize)
	var written int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmpFile.Write(buf[:n]); werr != nil {
				tmpFile.Close()
				os.Remove(tmpPath)
				return fmt.Errorf("write: %w", werr)
			}
			written += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("read: %w", err)
		}
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}

	fmt.Printf("Downloaded %s (%d bytes)\n", filepath.Base(destPath), written)
	return nil
}

func (d *Downloader) waitForDownload(destPath, lockPath string) error {
	deadline := time.Now().Add(downloadTimeout)

	for time.Now().Before(deadline) {
		// Check if file is ready
		if _, err := os.Stat(destPath); err == nil {
			return nil
		}

		// Check if lock still exists
		if _, err := os.Stat(lockPath); os.IsNotExist(err) {
			// Lock gone - check file one more time (race condition fix)
			if _, err := os.Stat(destPath); err == nil {
				return nil
			}
			// Lock gone and file not there - download failed
			return fmt.Errorf("download failed")
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for download")
}
