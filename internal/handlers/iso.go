package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/isoboot/isoboot/internal/config"
	"github.com/isoboot/isoboot/internal/iso"
)

const streamChunkSize = 1024 * 1024 // 1MB chunks for streaming

type ISOHandler struct {
	basePath      string
	configWatcher *config.ConfigWatcher
}

func NewISOHandler(basePath string, configWatcher *config.ConfigWatcher) *ISOHandler {
	return &ISOHandler{
		basePath:      basePath,
		configWatcher: configWatcher,
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

	// Validate requested ISO filename matches config to prevent unauthorized file access and disk abuse
	expectedFilename := filepath.Base(targetConfig.ISO)
	if isoFilename != expectedFilename {
		http.Error(w, fmt.Sprintf("invalid ISO filename: expected %s", expectedFilename), http.StatusBadRequest)
		return
	}

	// Get DiskImage directory name for file path construction (may differ from target name when DiskImageRef is set)
	diskImageDir := targetConfig.DiskImageName(target)

	// Check if ISO exists (downloaded by controller)
	isoPath := config.ISOPathWithFilename(h.basePath, diskImageDir, isoFilename)
	if _, err := os.Stat(isoPath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "ISO file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to access ISO file", http.StatusInternalServerError)
		return
	}

	// Check if this is initrd.gz and we have firmware
	if filePath == "initrd.gz" {
		h.serveInitrdWithFirmware(w, r, diskImageDir, isoFilename, targetConfig)
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
func (h *ISOHandler) serveInitrdWithFirmware(w http.ResponseWriter, r *http.Request, diskImageDir, isoFilename string, targetConfig config.TargetConfig) {
	isoPath := config.ISOPathWithFilename(h.basePath, diskImageDir, isoFilename)
	firmwarePath := config.FirmwarePath(h.basePath, diskImageDir)

	// Check if optional firmware (downloaded by the controller) exists for this disk image.
	// If not present, hasFirmware remains false and the handler continues without firmware.
	hasFirmware := false
	if targetConfig.Firmware != "" {
		if _, err := os.Stat(firmwarePath); err == nil {
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

// RegisterRoutes registers ISO-related routes
func (h *ISOHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/iso/content/", h.ServeISOContent)
}
