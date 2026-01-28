package handlers

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/isoboot/isoboot/internal/controllerclient"
	"github.com/isoboot/isoboot/internal/iso"
)

// validDiskImageRef matches alphanumeric, dash, underscore, with optional dot-separated segments.
// This prevents path traversal by rejecting ".." (dots must have chars between them).
var validDiskImageRef = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)*$`)

const streamChunkSize = 1024 * 1024 // 1MB chunks for streaming

type ISOHandler struct {
	basePath         string
	controllerClient *controllerclient.Client
}

func NewISOHandler(basePath string, controllerClient *controllerclient.Client) *ISOHandler {
	return &ISOHandler{
		basePath:         basePath,
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

	// Use diskImage from BootTarget for file path construction
	diskImage := bootTarget.DiskImage

	// Security: validate diskImage against allowlist pattern
	// This prevents path traversal by rejecting ".." (dots must have chars between them)
	if !validDiskImageRef.MatchString(diskImage) {
		log.Printf("iso: invalid diskImage %q", diskImage)
		http.Error(w, "invalid disk image reference", http.StatusBadRequest)
		return
	}

	// Construct ISO path
	// Note: isoFilename is not validated against a specific value because all files
	// in the disk image directory are extracted by the controller from the configured
	// DiskImage. Any file present is legitimate to serve (kernel, initrd, firmware, etc).
	isoPath := filepath.Join(h.basePath, diskImage, isoFilename)

	// Security: ensure path doesn't escape diskImage directory (prevent path traversal)
	diskImageDir := filepath.Join(h.basePath, diskImage) + string(os.PathSeparator)
	if !strings.HasPrefix(isoPath, diskImageDir) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Check if ISO exists (downloaded by controller)
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
		h.serveFileWithFirmware(w, r, diskImage, isoFilename, filePath)
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
func (h *ISOHandler) serveFileWithFirmware(w http.ResponseWriter, r *http.Request, diskImageRef, isoFilename, filePath string) {
	isoPath := filepath.Join(h.basePath, diskImageRef, isoFilename)
	firmwareFilePath := filepath.Join(h.basePath, diskImageRef, "firmware", "firmware.cpio.gz")

	// Check if firmware file exists (downloaded by controller if DiskImage has firmware URL)
	_, err := os.Stat(firmwareFilePath)
	hasFirmware := err == nil

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

// ServeISODownload serves full ISO files for download
// Path format: /iso/download/{bootTarget}/{diskImageFile}
// Example: /iso/download/ubuntu-24/ubuntu-24.04.1-live-server-amd64.iso
func (h *ISOHandler) ServeISODownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Parse path: /iso/download/{bootTarget}/{diskImageFile}
	path := strings.TrimPrefix(r.URL.Path, "/iso/download/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	bootTargetName := parts[0]
	diskImageFile := parts[1]

	// 2. Get BootTarget → disk image name
	bootTarget, err := h.controllerClient.GetBootTarget(ctx, bootTargetName)
	if err != nil {
		if errors.Is(err, controllerclient.ErrNotFound) {
			http.Error(w, "boot target not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to resolve boot target", http.StatusBadGateway)
		}
		return
	}
	diskImage := bootTarget.DiskImage

	// 3. Validate diskImage (reuse existing regex)
	if !validDiskImageRef.MatchString(diskImage) {
		http.Error(w, "invalid disk image", http.StatusBadRequest)
		return
	}

	// 4. Validate diskImageFile - no path traversal
	if strings.Contains(diskImageFile, "/") || strings.Contains(diskImageFile, "..") ||
		strings.HasPrefix(diskImageFile, ".") {
		http.Error(w, "invalid disk image file", http.StatusBadRequest)
		return
	}

	// 5. Construct path and verify containment
	diskImageDir := filepath.Join(h.basePath, diskImage)
	isoPath := filepath.Join(diskImageDir, diskImageFile)
	if !strings.HasPrefix(isoPath, diskImageDir+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// 6. Get file info for Content-Length
	fileInfo, err := os.Stat(isoPath)
	if err != nil {
		http.Error(w, "iso not found", http.StatusNotFound)
		return
	}

	// 7. Open file for streaming
	file, err := os.Open(isoPath)
	if err != nil {
		http.Error(w, "failed to open iso", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// 8. Set headers and stream
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", diskImageFile))
	w.WriteHeader(http.StatusOK)

	// 9. Stream in chunks (1MB, don't load entire ISO into memory)
	buf := make([]byte, streamChunkSize)
	if n, err := io.CopyBuffer(w, file, buf); err != nil {
		log.Printf("iso: error streaming %s: copied %d of %d bytes: %v", isoPath, n, fileInfo.Size(), err)
	}
}

// RegisterRoutes registers ISO-related routes
func (h *ISOHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/iso/content/", h.ServeISOContent)
	mux.HandleFunc("/iso/download/", h.ServeISODownload)
}
