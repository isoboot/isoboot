package iso

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// FileInfo represents a file in the ISO
type FileInfo struct {
	Name  string
	Size  int64
	IsDir bool
}

const (
	sectorSize           = 2048
	primaryVolumeDescriptor = 1
	volumeDescriptorSetTerminator = 255
)

// ISO9660 provides access to files within an ISO9660 image
type ISO9660 struct {
	file           *os.File
	rootDir        directoryRecord
	rootDirExtent  uint32
	rootDirSize    uint32
}

type directoryRecord struct {
	Length            uint8
	ExtAttrLength     uint8
	ExtentLocation    uint32
	DataLength        uint32
	RecordingDateTime [7]byte
	FileFlags         uint8
	FileUnitSize      uint8
	InterleaveGap     uint8
	VolumeSeqNumber   uint16
	FileIdentifierLen uint8
	FileIdentifier    string
	IsDirectory       bool
}

// OpenISO9660 opens an ISO9660 image file
func OpenISO9660(path string) (*ISO9660, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	iso := &ISO9660{file: f}
	if err := iso.readPrimaryVolumeDescriptor(); err != nil {
		f.Close()
		return nil, err
	}

	return iso, nil
}

// Close closes the ISO file
func (iso *ISO9660) Close() error {
	return iso.file.Close()
}

func (iso *ISO9660) readPrimaryVolumeDescriptor() error {
	// Volume descriptors start at sector 16
	sector := make([]byte, sectorSize)

	for i := 16; i < 32; i++ {
		if _, err := iso.file.ReadAt(sector, int64(i)*sectorSize); err != nil {
			return fmt.Errorf("read sector %d: %w", i, err)
		}

		descriptorType := sector[0]
		if descriptorType == volumeDescriptorSetTerminator {
			return fmt.Errorf("no primary volume descriptor found")
		}

		if descriptorType == primaryVolumeDescriptor {
			// Verify ISO9660 signature
			if string(sector[1:6]) != "CD001" {
				return fmt.Errorf("invalid ISO9660 signature")
			}

			// Root directory record is at offset 156, length 34
			rootDirData := sector[156:190]
			iso.rootDir = iso.parseDirectoryRecord(rootDirData)
			iso.rootDirExtent = iso.rootDir.ExtentLocation
			iso.rootDirSize = iso.rootDir.DataLength
			return nil
		}
	}

	return fmt.Errorf("primary volume descriptor not found")
}

func (iso *ISO9660) parseDirectoryRecord(data []byte) directoryRecord {
	if len(data) < 33 || data[0] == 0 {
		return directoryRecord{}
	}

	rec := directoryRecord{
		Length:            data[0],
		ExtAttrLength:     data[1],
		ExtentLocation:    binary.LittleEndian.Uint32(data[2:6]),
		DataLength:        binary.LittleEndian.Uint32(data[10:14]),
		FileFlags:         data[25],
		FileUnitSize:      data[26],
		InterleaveGap:     data[27],
		VolumeSeqNumber:   binary.LittleEndian.Uint16(data[28:30]),
		FileIdentifierLen: data[32],
	}

	copy(rec.RecordingDateTime[:], data[18:25])

	// Parse file identifier
	if rec.FileIdentifierLen > 0 && int(33+rec.FileIdentifierLen) <= len(data) {
		rec.FileIdentifier = string(data[33 : 33+rec.FileIdentifierLen])
		// Remove version number (;1)
		if idx := strings.Index(rec.FileIdentifier, ";"); idx >= 0 {
			rec.FileIdentifier = rec.FileIdentifier[:idx]
		}
		// Remove trailing dot for directories
		rec.FileIdentifier = strings.TrimSuffix(rec.FileIdentifier, ".")
	}

	rec.IsDirectory = (rec.FileFlags & 0x02) != 0

	// Check for Rock Ridge extensions to get real filename
	if rec.Length > 33+rec.FileIdentifierLen {
		paddingLen := uint8(0)
		if rec.FileIdentifierLen%2 == 0 {
			paddingLen = 1
		}
		suOffset := 33 + rec.FileIdentifierLen + paddingLen
		if int(suOffset) < int(rec.Length) && int(rec.Length) <= len(data) {
			suData := data[suOffset:rec.Length]
			if altName := iso.parseRockRidgeName(suData); altName != "" {
				rec.FileIdentifier = altName
			}
		}
	}

	return rec
}

func (iso *ISO9660) parseRockRidgeName(suData []byte) string {
	// Look for NM (Alternate Name) entry
	for len(suData) >= 4 {
		sig := string(suData[0:2])
		length := int(suData[2])

		if length < 4 || length > len(suData) {
			break
		}

		if sig == "NM" && length > 5 {
			// NM entry: sig(2) + len(1) + version(1) + flags(1) + name(rest)
			flags := suData[4]
			name := string(suData[5:length])
			if flags&0x02 == 0 { // Not a continuation
				return name
			}
		}

		suData = suData[length:]
	}
	return ""
}

// ListDirectory lists all entries in a directory
func (iso *ISO9660) ListDirectory(path string) ([]FileInfo, error) {
	extent, size, err := iso.findDirectory(path)
	if err != nil {
		return nil, err
	}

	return iso.readDirectoryEntries(extent, size)
}

func (iso *ISO9660) findDirectory(path string) (uint32, uint32, error) {
	if path == "" || path == "/" {
		return iso.rootDirExtent, iso.rootDirSize, nil
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	currentExtent := iso.rootDirExtent
	currentSize := iso.rootDirSize

	for _, part := range parts {
		entries, err := iso.readDirectoryEntries(currentExtent, currentSize)
		if err != nil {
			return 0, 0, err
		}

		found := false
		for _, entry := range entries {
			if strings.EqualFold(entry.Name, part) && entry.IsDir {
				// Need to get the actual directory record to find extent
				rec, err := iso.findEntryRecord(currentExtent, currentSize, part)
				if err != nil {
					return 0, 0, err
				}
				currentExtent = rec.ExtentLocation
				currentSize = rec.DataLength
				found = true
				break
			}
		}
		if !found {
			return 0, 0, fmt.Errorf("directory not found: %s", part)
		}
	}

	return currentExtent, currentSize, nil
}

func (iso *ISO9660) findEntryRecord(extent, size uint32, name string) (directoryRecord, error) {
	data := make([]byte, size)
	if _, err := iso.file.ReadAt(data, int64(extent)*sectorSize); err != nil {
		return directoryRecord{}, fmt.Errorf("read directory: %w", err)
	}

	offset := 0
	for offset < len(data) {
		// Check for sector boundary - records don't span sectors
		sectorOffset := offset % sectorSize
		if data[offset] == 0 {
			// Padding to next sector
			if sectorOffset > 0 {
				offset += sectorSize - sectorOffset
				continue
			}
			break
		}

		recLen := int(data[offset])
		if recLen < 33 || offset+recLen > len(data) {
			break
		}

		rec := iso.parseDirectoryRecord(data[offset : offset+recLen])
		if strings.EqualFold(rec.FileIdentifier, name) {
			return rec, nil
		}

		offset += recLen
	}

	return directoryRecord{}, fmt.Errorf("entry not found: %s", name)
}

func (iso *ISO9660) readDirectoryEntries(extent, size uint32) ([]FileInfo, error) {
	data := make([]byte, size)
	if _, err := iso.file.ReadAt(data, int64(extent)*sectorSize); err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	var entries []FileInfo
	offset := 0

	for offset < len(data) {
		// Check for sector boundary - records don't span sectors
		sectorOffset := offset % sectorSize

		// If we hit a zero byte, skip to next sector boundary
		if data[offset] == 0 {
			if sectorOffset > 0 {
				offset += sectorSize - sectorOffset
				continue
			}
			break
		}

		recLen := int(data[offset])
		if recLen < 33 || offset+recLen > len(data) {
			break
		}

		rec := iso.parseDirectoryRecord(data[offset : offset+recLen])

		// Skip . and .. entries
		if rec.FileIdentifier != "" && rec.FileIdentifier != "\x00" && rec.FileIdentifier != "\x01" {
			entries = append(entries, FileInfo{
				Name:  rec.FileIdentifier,
				Size:  int64(rec.DataLength),
				IsDir: rec.IsDirectory,
			})
		}

		offset += recLen
	}

	return entries, nil
}

// ReadFile reads a file from the ISO
func (iso *ISO9660) ReadFile(path string) ([]byte, error) {
	rec, err := iso.findFile(path)
	if err != nil {
		return nil, err
	}

	data := make([]byte, rec.DataLength)
	if _, err := iso.file.ReadAt(data, int64(rec.ExtentLocation)*sectorSize); err != nil {
		return nil, fmt.Errorf("read file data: %w", err)
	}

	return data, nil
}

// OpenFile opens a file for streaming
func (iso *ISO9660) OpenFile(path string) (io.ReadCloser, int64, error) {
	rec, err := iso.findFile(path)
	if err != nil {
		return nil, 0, err
	}

	return &isoFileStream{
		iso:    iso,
		offset: int64(rec.ExtentLocation) * sectorSize,
		size:   int64(rec.DataLength),
		pos:    0,
	}, int64(rec.DataLength), nil
}

type isoFileStream struct {
	iso    *ISO9660
	offset int64
	size   int64
	pos    int64
}

func (s *isoFileStream) Read(p []byte) (int, error) {
	if s.pos >= s.size {
		return 0, io.EOF
	}

	remaining := s.size - s.pos
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}

	n, err := s.iso.file.ReadAt(p, s.offset+s.pos)
	s.pos += int64(n)
	return n, err
}

func (s *isoFileStream) Close() error {
	return nil // ISO file stays open
}

func (iso *ISO9660) findFile(path string) (directoryRecord, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return directoryRecord{}, fmt.Errorf("invalid path")
	}

	// Navigate to parent directory
	dirExtent := iso.rootDirExtent
	dirSize := iso.rootDirSize

	for i := 0; i < len(parts)-1; i++ {
		rec, err := iso.findEntryRecord(dirExtent, dirSize, parts[i])
		if err != nil {
			return directoryRecord{}, err
		}
		if !rec.IsDirectory {
			return directoryRecord{}, fmt.Errorf("not a directory: %s", parts[i])
		}
		dirExtent = rec.ExtentLocation
		dirSize = rec.DataLength
	}

	// Find the file in the final directory
	fileName := parts[len(parts)-1]
	rec, err := iso.findEntryRecord(dirExtent, dirSize, fileName)
	if err != nil {
		return directoryRecord{}, err
	}
	if rec.IsDirectory {
		return directoryRecord{}, fmt.Errorf("is a directory: %s", fileName)
	}

	return rec, nil
}
