package checksum

import (
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
)

var hexPattern = regexp.MustCompile(`(?i)^[0-9a-f]+$`)

// DetectAlgorithm returns the crypto.Hash for the given hex-encoded hash string.
// 64 hex characters = SHA-256, 128 hex characters = SHA-512.
// The input must be valid hexadecimal (upper or lowercase).
func DetectAlgorithm(h string) (crypto.Hash, error) {
	if !hexPattern.MatchString(h) {
		return 0, fmt.Errorf("hash contains invalid characters (expected hex)")
	}
	switch len(h) {
	case 64:
		return crypto.SHA256, nil
	case 128:
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("unsupported hash length %d (expected 64 for sha256 or 128 for sha512)", len(h))
	}
}

// VerifyFile reads the file at path, computes its hash using the algorithm
// auto-detected from expectedHash length, and returns an error if they don't match.
func VerifyFile(filePath, expectedHash string) error {
	expectedHash = strings.ToLower(expectedHash)

	algo, err := DetectAlgorithm(expectedHash)
	if err != nil {
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	var h hash.Hash
	switch algo {
	case crypto.SHA256:
		h = sha256.New()
	case crypto.SHA512:
		h = sha512.New()
	default:
		return fmt.Errorf("unsupported hash algorithm: %v", algo)
	}

	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHash {
		return fmt.Errorf("hash mismatch: expected %s, got %s", expectedHash, actual)
	}

	return nil
}

// ParseShasumFile parses a shasum file's content and returns the hash for the
// file identified by fileURL. The shasumURL is used to compute relative paths
// for matching.
//
// The function supports both hash-first and filename-first line formats, and
// handles ./prefix stripping and longest-suffix fallback matching.
func ParseShasumFile(content, fileURL, shasumURL string) (string, error) {
	// Compute the relative path from the shasum directory to the file URL.
	rel, err := relativePath(fileURL, shasumURL)
	if err != nil {
		return "", fmt.Errorf("computing relative path: %w", err)
	}

	lines := strings.Split(content, "\n")
	type match struct {
		hash     string
		filename string
	}

	entries := make([]match, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		h, filename, err := parseLine(line)
		if err != nil {
			continue // skip unparseable lines
		}
		entries = append(entries, match{hash: h, filename: filename})
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("no valid entries found in shasum file")
	}

	// Try exact match first (after stripping ./ from both sides).
	cleanRel := stripDotSlash(rel)
	for _, e := range entries {
		cleanFilename := stripDotSlash(e.filename)
		if cleanFilename == cleanRel {
			return e.hash, nil
		}
	}

	// Longest suffix fallback: progressively strip leading path components
	// from the relative path until a match is found.
	parts := strings.Split(cleanRel, "/")
	for i := 1; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], "/")
		var matches []match
		for _, e := range entries {
			cleanFilename := stripDotSlash(e.filename)
			if cleanFilename == suffix || strings.HasSuffix(cleanFilename, "/"+suffix) {
				matches = append(matches, e)
			}
		}
		if len(matches) == 1 {
			return matches[0].hash, nil
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("ambiguous match: %d entries match suffix %q", len(matches), suffix)
		}
	}

	return "", fmt.Errorf("no matching entry found for %s", fileURL)
}

// parseLine parses a single line from a shasum file, returning the hash and filename.
// Supports both formats:
//   - hash-first:     <hash>  <filename>
//   - filename-first: <filename>  <hash>
func parseLine(line string) (string, string, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", fmt.Errorf("not enough fields")
	}

	// Try hash-first format: <hash>  <filename>
	// Use the raw line to extract the filename so spaces are preserved.
	first := strings.ToLower(fields[0])
	if isHash(first) {
		// Find everything after the hash token, strip separator whitespace
		// and optional binary-mode indicator (*).
		rest := line[len(fields[0]):]
		rest = strings.TrimLeft(rest, " \t")
		rest = strings.TrimPrefix(rest, "*")
		if rest == "" {
			return "", "", fmt.Errorf("missing filename")
		}
		return first, rest, nil
	}

	// Try filename-first format: <filename>  <hash>
	last := strings.ToLower(fields[len(fields)-1])
	filename := strings.Join(fields[:len(fields)-1], " ")
	if isHash(last) {
		return last, filename, nil
	}

	return "", "", fmt.Errorf("no valid hash found in line")
}

// isHash returns true if s looks like a valid hex-encoded SHA-256 or SHA-512 hash.
func isHash(s string) bool {
	return (len(s) == 64 || len(s) == 128) && hexPattern.MatchString(s)
}

// relativePath computes the relative path from the shasumURL's parent directory
// to the fileURL. Both URLs must use the https scheme and share the same host.
func relativePath(fileURL, shasumURL string) (string, error) {
	fURL, err := url.Parse(fileURL)
	if err != nil {
		return "", fmt.Errorf("parsing file URL: %w", err)
	}
	sURL, err := url.Parse(shasumURL)
	if err != nil {
		return "", fmt.Errorf("parsing shasum URL: %w", err)
	}

	// Only https is allowed.
	if fURL.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q in file URL: only https is allowed", fURL.Scheme)
	}
	if sURL.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q in shasum URL: only https is allowed", sURL.Scheme)
	}

	// Both URLs must reference the same host.
	if fURL.Host != sURL.Host {
		return "", fmt.Errorf("host mismatch: file URL host %q does not match shasum URL host %q", fURL.Host, sURL.Host)
	}

	// Get the directory of the shasum file.
	shasumDir := path.Dir(sURL.Path)

	// Compute relative path.
	if rel, ok := strings.CutPrefix(fURL.Path, shasumDir+"/"); ok {
		return rel, nil
	}

	// If they don't share a prefix, just return the file's basename as fallback.
	return path.Base(fURL.Path), nil
}

// stripDotSlash removes a leading "./" from a path.
func stripDotSlash(p string) string {
	return strings.TrimPrefix(p, "./")
}
