package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/isoboot/isoboot/internal/config"
	"github.com/isoboot/isoboot/internal/downloader"
	"github.com/isoboot/isoboot/internal/iso"
)

const streamChunkSize = 1024 * 1024 // 1MB chunks for streaming

type ISOHandler struct {
	basePath      string
	configWatcher *config.ConfigWatcher
	downloader    *downloader.Downloader
}

func NewISOHandler(basePath string, configWatcher *config.ConfigWatcher) *ISOHandler {
	return &ISOHandler{
		basePath:      basePath,
		configWatcher: configWatcher,
		downloader:    downloader.New(),
	}
}

// ServeISOContent serves files from ISO images
// Path format: /iso/content/{target}/{isoFilename}/{filepath}
// Example: /iso/content/debian-13/mini.iso/linux
// Special handling for initrd.gz - merges with firmware if present
func (h *ISOHandler) ServeISOContent(w http.ResponseWriter, r *http.Request) {
	// Parse path: /iso/content/debian-13/mini.iso/linux
	path := strings.TrimPrefix(r.URL.Path, "/iso/content/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 3 {
		http.Error(w, "invalid path: expected /iso/content/{target}/{isoFilename}/{filepath}", http.StatusBadRequest)
		return
	}

	target := parts[0]
	isoFilename := parts[1]
	filePath := parts[2]

	// Get target config
	targetConfig, ok := h.configWatcher.GetTarget(target)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown target: %s", target), http.StatusNotFound)
		return
	}

	// Ensure ISO is downloaded (blocks if download in progress)
	isoPath := config.ISOPathWithFilename(h.basePath, target, isoFilename)
	if err := h.downloader.EnsureFile(isoPath, targetConfig.ISO); err != nil {
		http.Error(w, fmt.Sprintf("failed to get ISO: %v", err), http.StatusInternalServerError)
		return
	}

	// Check if this is initrd.gz and we have firmware
	if filePath == "initrd.gz" {
		h.serveInitrdWithFirmware(w, r, target, isoFilename, targetConfig)
		return
	}

	// Serve from ISO
	h.serveFileFromISO(w, isoPath, filePath)
}

// serveFileFromISO serves a file from the ISO in chunks
func (h *ISOHandler) serveFileFromISO(w http.ResponseWriter, isoPath, filePath string) {
	isoFile, err := iso.OpenISO9660(isoPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to open ISO: %v", err), http.StatusInternalServerError)
		return
	}
	defer isoFile.Close()

	rc, size, err := isoFile.OpenFile(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("file not found in ISO: %v", err), http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))

	// Stream in 1MB chunks
	buf := make([]byte, streamChunkSize)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return
		}
	}
}

// serveInitrdWithFirmware serves initrd.gz, merging with firmware if present
// Per https://wiki.debian.org/DebianInstaller/NetbootFirmware:
// cat initrd.gz firmware.cpio.gz > combined.gz
func (h *ISOHandler) serveInitrdWithFirmware(w http.ResponseWriter, r *http.Request, target, isoFilename string, targetConfig config.TargetConfig) {
	isoPath := config.ISOPathWithFilename(h.basePath, target, isoFilename)
	firmwarePath := config.FirmwarePath(h.basePath, target)

	// Check if firmware is configured and download if needed
	hasFirmware := false
	if targetConfig.Firmware != "" {
		if err := h.downloader.EnsureFile(firmwarePath, targetConfig.Firmware); err != nil {
			fmt.Printf("Warning: failed to download firmware: %v\n", err)
		} else {
			hasFirmware = true
		}
	}

	// Open ISO and get initrd.gz
	isoFile, err := iso.OpenISO9660(isoPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to open ISO: %v", err), http.StatusInternalServerError)
		return
	}
	defer isoFile.Close()

	initrdReader, initrdSize, err := isoFile.OpenFile("initrd.gz")
	if err != nil {
		http.Error(w, fmt.Sprintf("initrd.gz not found in ISO: %v", err), http.StatusNotFound)
		return
	}
	defer initrdReader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")

	if hasFirmware {
		// Get firmware size
		firmwareInfo, err := os.Stat(firmwarePath)
		if err != nil {
			http.Error(w, "failed to stat firmware", http.StatusInternalServerError)
			return
		}

		// Set combined content length
		totalSize := initrdSize + firmwareInfo.Size()
		w.Header().Set("Content-Length", fmt.Sprintf("%d", totalSize))

		// Stream initrd in chunks
		buf := make([]byte, streamChunkSize)
		for {
			n, err := initrdReader.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Printf("Error streaming initrd: %v\n", err)
				return
			}
		}

		// Stream firmware in chunks
		firmwareFile, err := os.Open(firmwarePath)
		if err != nil {
			fmt.Printf("Error opening firmware: %v\n", err)
			return
		}
		defer firmwareFile.Close()

		for {
			n, err := firmwareFile.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Printf("Error streaming firmware: %v\n", err)
				return
			}
		}
	} else {
		// No firmware, just serve initrd in chunks
		w.Header().Set("Content-Length", fmt.Sprintf("%d", initrdSize))

		buf := make([]byte, streamChunkSize)
		for {
			n, err := initrdReader.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
		}
	}
}

// ListISOContents lists files in an ISO directory (for debugging)
func (h *ISOHandler) ListISOContents(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/iso/list/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	target := parts[0]
	dirPath := ""
	if len(parts) > 1 {
		dirPath = parts[1]
	}

	// Get target config
	targetConfig, ok := h.configWatcher.GetTarget(target)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown target: %s", target), http.StatusNotFound)
		return
	}

	// Ensure ISO is downloaded
	isoPath := config.ISOPath(h.basePath, target)
	if err := h.downloader.EnsureFile(isoPath, targetConfig.ISO); err != nil {
		http.Error(w, fmt.Sprintf("failed to get ISO: %v", err), http.StatusInternalServerError)
		return
	}

	isoFile, err := iso.OpenISO9660(isoPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to open ISO: %v", err), http.StatusInternalServerError)
		return
	}
	defer isoFile.Close()

	files, err := isoFile.ListDirectory(dirPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list directory: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	for _, f := range files {
		typeChar := "-"
		if f.IsDir {
			typeChar = "d"
		}
		fmt.Fprintf(w, "%s %10d %s\n", typeChar, f.Size, f.Name)
	}
}

// RegisterRoutes registers ISO-related routes
func (h *ISOHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/iso/content/", h.ServeISOContent)
	mux.HandleFunc("/iso/list/", h.ListISOContents)
}
