package handlers

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/isoboot/isoboot/internal/config"
	"github.com/isoboot/isoboot/internal/controllerclient"
	"github.com/isoboot/isoboot/internal/iso"
)

const streamChunkSize = 1024 * 1024 // 1MB chunks for streaming

type ISOHandler struct {
	basePath         string
	configWatcher    *config.ConfigWatcher
	controllerClient *controllerclient.Client
}

func NewISOHandler(basePath string, configWatcher *config.ConfigWatcher, controllerClient *controllerclient.Client) *ISOHandler {
	return &ISOHandler{
		basePath:         basePath,
		configWatcher:    configWatcher,
		controllerClient: controllerClient,
	}
}

// ServeISOContent serves files from ISO images
// Path format: /iso/content/{target}/{isoFilename}/{filepath}
// Example: /iso/content/debian-13/mini.iso/linux
// Special handling for includeFirmwarePath - merges with firmware if present
func (h *ISOHandler) ServeISOContent(w http.ResponseWriter, r *http.Request) {
	// Parse path: /iso/content/debian-13/mini.iso/linux
	path := strings.TrimPrefix(r.URL.Path, "/iso/content/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 3 {
		http.Error(w, "invalid path: expected /iso/content/{target}/{isoFilename}/{filepath}", http.StatusBadRequest)
		return
	}

	bootTargetName := parts[0]
	isoFilename := parts[1]
	filePath := parts[2]

	// Get BootTarget first to determine diskImageRef and includeFirmwarePath.
	// This gRPC call is made per-request, consistent with other handlers (boot, answer).
	bootTarget, err := h.controllerClient.GetBootTarget(r.Context(), bootTargetName)
	if err != nil {
		if errors.Is(err, controllerclient.ErrNotFound) {
			http.Error(w, fmt.Sprintf("boot target not found: %s", bootTargetName), http.StatusNotFound)
		} else {
			log.Printf("iso: failed to get BootTarget %s: %v", bootTargetName, err)
			http.Error(w, "failed to resolve boot target", http.StatusBadGateway)
		}
		return
	}

	// Use diskImageRef from BootTarget for config lookup and file path construction
	diskImageRef := bootTarget.DiskImageRef

	// Get target config using diskImageRef for ISO filename validation
	targetConfig, ok := h.configWatcher.GetTarget(diskImageRef)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown disk image: %s", diskImageRef), http.StatusNotFound)
		return
	}

	// Validate requested ISO filename matches config to prevent unauthorized file access and disk abuse
	expectedFilename := filepath.Base(targetConfig.ISO)
	if isoFilename != expectedFilename {
		http.Error(w, fmt.Sprintf("invalid ISO filename: expected %s", expectedFilename), http.StatusBadRequest)
		return
	}

	// Check if ISO exists (downloaded by controller)
	isoPath := config.ISOPathWithFilename(h.basePath, diskImageRef, isoFilename)
	if _, err := os.Stat(isoPath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "ISO file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to access ISO file", http.StatusInternalServerError)
		return
	}

	// Check if this path should have firmware merged.
	// Firmware is only merged when includeFirmwarePath is explicitly set.
	if shouldMergeFirmware(filePath, bootTarget.IncludeFirmwarePath) {
		h.serveFileWithFirmware(w, r, diskImageRef, isoFilename, filePath, targetConfig)
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

// serveFileWithFirmware serves a file from the ISO, appending firmware if present.
// Per https://wiki.debian.org/DebianInstaller/NetbootFirmware:
// cat initrd.gz firmware.cpio.gz > combined.gz
func (h *ISOHandler) serveFileWithFirmware(w http.ResponseWriter, r *http.Request, diskImageRef, isoFilename, filePath string, targetConfig config.TargetConfig) {
	isoPath := config.ISOPathWithFilename(h.basePath, diskImageRef, isoFilename)
	firmwareFilePath := config.FirmwarePath(h.basePath, diskImageRef)

	// Check if optional firmware (downloaded by the controller) exists for this disk image.
	// If not present, hasFirmware remains false and the handler continues without firmware.
	hasFirmware := false
	if targetConfig.Firmware != "" {
		if _, err := os.Stat(firmwareFilePath); err == nil {
			hasFirmware = true
		}
	}

	// Open ISO and get the requested file
	isoFile, err := iso.OpenISO9660(isoPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to open ISO: %v", err), http.StatusInternalServerError)
		return
	}
	defer isoFile.Close()

	fileReader, fileSize, err := isoFile.OpenFile(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("file not found in ISO: %v", err), http.StatusNotFound)
		return
	}
	defer fileReader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")

	if hasFirmware {
		// Get firmware size
		firmwareInfo, err := os.Stat(firmwareFilePath)
		if err != nil {
			http.Error(w, "failed to stat firmware", http.StatusInternalServerError)
			return
		}

		// Set combined content length
		totalSize := fileSize + firmwareInfo.Size()
		w.Header().Set("Content-Length", fmt.Sprintf("%d", totalSize))

		// Stream file in chunks
		buf := make([]byte, streamChunkSize)
		for {
			n, err := fileReader.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Printf("iso: error streaming file: %v", err)
				return
			}
		}

		// Stream firmware in chunks
		firmwareFile, err := os.Open(firmwareFilePath)
		if err != nil {
			log.Printf("iso: error opening firmware: %v", err)
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
				log.Printf("iso: error streaming firmware: %v", err)
				return
			}
		}
	} else {
		// No firmware, just serve file in chunks
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))

		buf := make([]byte, streamChunkSize)
		for {
			n, err := fileReader.Read(buf)
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

// shouldMergeFirmware returns true if the requested file path matches the
// configured includeFirmwarePath. Both paths are normalized with a leading slash.
// Returns false if includeFirmwarePath is empty (firmware merging disabled).
func shouldMergeFirmware(requestedFile, includeFirmwarePath string) bool {
	if includeFirmwarePath == "" {
		return false
	}
	// Normalize both paths to have leading slash
	if !strings.HasPrefix(includeFirmwarePath, "/") {
		includeFirmwarePath = "/" + includeFirmwarePath
	}
	requestPath := "/" + requestedFile
	return requestPath == includeFirmwarePath
}

// RegisterRoutes registers ISO-related routes
func (h *ISOHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/iso/content/", h.ServeISOContent)
}
