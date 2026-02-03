package isoextract

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const sectorSize = 2048

// directoryRecord holds a parsed ISO 9660 directory record.
type directoryRecord struct {
	extentLBA  uint32
	dataLength uint32
	flags      byte
	fileID     string // ISO 9660 file identifier
	rrName     string // Rock Ridge NM name (empty if absent)
}

// name returns the Rock Ridge name if available, otherwise the ISO 9660 file ID.
func (d directoryRecord) name() string {
	if d.rrName != "" {
		return d.rrName
	}
	return d.fileID
}

// isDir reports whether the record represents a directory.
func (d directoryRecord) isDir() bool {
	return d.flags&0x02 != 0
}

// Extract reads an ISO 9660 image at isoPath and extracts the files listed in
// filePaths into destDir, preserving subdirectory structure. File paths must use
// forward slashes and should not have a leading slash.
//
// All requested paths are attempted; if any are missing, a single error listing
// every missing path is returned (extraction of found files still occurs).
//
// Security: callers must ensure that filePaths do not contain path traversal
// sequences (for example, "../" components) that would escape destDir. Extract
// rejects any resolved destination that falls outside destDir.
func Extract(isoPath string, filePaths []string, destDir string) error {
	f, err := os.Open(isoPath)
	if err != nil {
		return fmt.Errorf("opening ISO: %w", err)
	}
	defer func() { _ = f.Close() }()

	absDestDir, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolving destDir: %w", err)
	}

	root, err := readPVD(f)
	if err != nil {
		return err
	}

	var missing []string
	for _, p := range filePaths {
		p = strings.TrimPrefix(p, "/")
		rec, err := walkPath(f, root, p)
		if err != nil {
			missing = append(missing, p)
			continue
		}
		dest := filepath.Join(absDestDir, filepath.FromSlash(p))
		rel, err := filepath.Rel(absDestDir, dest)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path %q escapes destination directory", p)
		}
		if err := extractFile(f, *rec, dest); err != nil {
			return fmt.Errorf("extracting %s: %w", p, err)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("paths not found in ISO: %s", strings.Join(missing, ", "))
	}
	return nil
}

// readPVD reads the Primary Volume Descriptor at sector 16 and returns the
// root directory record.
func readPVD(r io.ReaderAt) (directoryRecord, error) {
	buf := make([]byte, sectorSize)
	if _, err := r.ReadAt(buf, 16*sectorSize); err != nil {
		return directoryRecord{}, fmt.Errorf("reading PVD: %w", err)
	}

	// Standard identifier must be "CD001".
	if string(buf[1:6]) != "CD001" {
		return directoryRecord{}, fmt.Errorf("not an ISO 9660 image: missing CD001 signature")
	}

	// Root directory record is at offset 156, length 34 bytes.
	root, err := parseDirectoryRecord(buf[156:190])
	if err != nil {
		return directoryRecord{}, fmt.Errorf("parsing root directory record: %w", err)
	}
	return root, nil
}

// parseDirectoryRecord parses a single directory record from the given byte
// slice. The slice must start at the record's length byte.
func parseDirectoryRecord(data []byte) (directoryRecord, error) {
	if len(data) < 33 {
		return directoryRecord{}, fmt.Errorf("data too short for directory record: %d bytes", len(data))
	}
	recLen := int(data[0])
	if recLen < 33 || recLen > len(data) {
		return directoryRecord{}, fmt.Errorf("directory record length invalid: %d bytes (available: %d)", recLen, len(data))
	}

	rec := directoryRecord{
		extentLBA:  binary.LittleEndian.Uint32(data[2:6]),
		dataLength: binary.LittleEndian.Uint32(data[10:14]),
		flags:      data[25],
	}

	idLen := int(data[32])
	if 33+idLen > recLen {
		return directoryRecord{}, fmt.Errorf("file ID overflows record")
	}
	rec.fileID = string(data[33 : 33+idLen])

	// Strip the ";1" version suffix from ISO 9660 file identifiers.
	if idx := strings.Index(rec.fileID, ";"); idx >= 0 {
		rec.fileID = rec.fileID[:idx]
	}

	// System Use area starts after the file ID, padded to an even offset.
	suOffset := 33 + idLen
	if idLen%2 == 0 {
		suOffset++ // padding byte
	}
	if suOffset < recLen {
		rec.rrName = parseRockRidgeName(data[suOffset:recLen])
	}

	return rec, nil
}

// parseRockRidgeName scans the System Use area for a Rock Ridge NM entry and
// returns the alternate name. It handles the CONTINUE flag by concatenating
// multiple NM entries.
func parseRockRidgeName(data []byte) string {
	var name strings.Builder
	i := 0
	for i+4 <= len(data) {
		sig := string(data[i : i+2])
		sueLen := int(data[i+2])
		if sueLen < 4 || i+sueLen > len(data) {
			break
		}

		if sig == "NM" {
			// NM entry: byte 3 = version, byte 4 = flags, bytes 5+ = name
			if sueLen > 5 {
				name.Write(data[i+5 : i+sueLen])
			}
			flags := data[i+4]
			if flags&0x01 == 0 { // no CONTINUE flag
				return name.String()
			}
		}

		i += sueLen
	}
	if name.Len() > 0 {
		return name.String()
	}
	return ""
}

// readDirectory reads the full extent of a directory and returns all parsed
// records, excluding the "." and ".." entries.
func readDirectory(r io.ReaderAt, lba, size uint32) ([]directoryRecord, error) {
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, int64(lba)*sectorSize); err != nil {
		return nil, fmt.Errorf("reading directory extent: %w", err)
	}

	var records []directoryRecord
	offset := 0
	for offset < int(size) {
		// A zero length byte means the rest of this sector is padding.
		if buf[offset] == 0 {
			// Skip to the next sector boundary.
			nextSector := ((offset / sectorSize) + 1) * sectorSize
			if nextSector >= int(size) {
				break
			}
			offset = nextSector
			continue
		}

		rec, err := parseDirectoryRecord(buf[offset:])
		if err != nil {
			return nil, fmt.Errorf("at offset %d: %w", offset, err)
		}

		recLen := int(buf[offset])
		offset += recLen

		// Skip "." (0x00) and ".." (0x01) entries.
		if len(rec.fileID) == 1 && (rec.fileID[0] == 0x00 || rec.fileID[0] == 0x01) {
			continue
		}

		records = append(records, rec)
	}

	return records, nil
}

// walkPath traverses the directory tree starting from root to find the record
// at the given slash-separated path. A case-insensitive fallback is used for
// each path component.
func walkPath(r io.ReaderAt, root directoryRecord, path string) (*directoryRecord, error) {
	if path == "" {
		return &root, nil
	}
	parts := strings.Split(path, "/")
	current := root

	for _, part := range parts {
		if !current.isDir() {
			return nil, fmt.Errorf("not a directory: %s", part)
		}

		entries, err := readDirectory(r, current.extentLBA, current.dataLength)
		if err != nil {
			return nil, err
		}

		found := false
		for _, e := range entries {
			if e.name() == part {
				current = e
				found = true
				break
			}
		}

		// Case-insensitive fallback.
		if !found {
			for _, e := range entries {
				if strings.EqualFold(e.name(), part) {
					current = e
					found = true
					break
				}
			}
		}

		if !found {
			return nil, fmt.Errorf("path component %q not found", part)
		}
	}

	return &current, nil
}

// extractFile copies the file data described by rec into destPath, creating
// parent directories as needed.
func extractFile(r io.ReaderAt, rec directoryRecord, destPath string) error {
	if rec.isDir() {
		return fmt.Errorf("cannot extract directory %q as file", destPath)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	sr := io.NewSectionReader(r, int64(rec.extentLBA)*sectorSize, int64(rec.dataLength))
	if _, err := io.Copy(out, sr); err != nil {
		_ = out.Close()
		return err
	}

	return out.Close()
}
