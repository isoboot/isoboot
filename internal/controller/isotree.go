/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	"github.com/go-logr/logr"
)

// The NFS directory holds one exported tree per ISO-mode BootConfig, named
// after the BootConfig. Everything else in it starts with "." so that nfsd
// does not export it: the marker recording which ISO a tree came from, and
// the temporary directories used while (re)extracting. Kubernetes object
// names never contain "_", so the prefixes below cannot collide between
// BootConfigs.
const (
	isoTreeMarkerPrefix = ".source_"
	isoTreeExtractInfix = ".extract_"
	isoTreeOldInfix     = ".old_"

	// maxISOTreeDepth guards against directory loops in a crafted ISO.
	maxISOTreeDepth = 64
)

func isoTreeMarker(nfsDir, name string) string {
	return filepath.Join(nfsDir, isoTreeMarkerPrefix+name)
}

// isoSource identifies an ISO file well enough to notice when it changes.
// It is stored in the marker beside the extracted tree.
func isoSource(isoPath, expectedHash string) (string, error) {
	info, err := os.Stat(isoPath)
	if err != nil {
		return "", fmt.Errorf("stat iso %q: %w", isoPath, err)
	}
	return fmt.Sprintf("path=%s\nsize=%d\nmtime=%d\nhash=%s\n",
		isoPath, info.Size(), info.ModTime().UnixNano(), expectedHash), nil
}

// ensureISOTree makes nfsDir/name hold the whole tree of the ISO at isoPath.
// It extracts only when the marker does not match source or the tree is
// missing: re-extracting changes inode numbers, and NFS clients that hold
// handles into the old tree would get ESTALE. It returns the marker's
// modification time, which changes exactly when the tree is replaced.
func ensureISOTree(log logr.Logger, isoPath, source, nfsDir, name string) (time.Time, error) {
	if err := os.MkdirAll(nfsDir, 0o755); err != nil {
		return time.Time{}, fmt.Errorf("creating nfs dir: %w", err)
	}
	dest := filepath.Join(nfsDir, name)
	marker := isoTreeMarker(nfsDir, name)

	if b, err := os.ReadFile(marker); err == nil && string(b) == source {
		if fi, err := os.Lstat(dest); err == nil && fi.IsDir() {
			mi, err := os.Stat(marker)
			if err != nil {
				return time.Time{}, fmt.Errorf("stat marker: %w", err)
			}
			return mi.ModTime(), nil
		}
	}

	removeStaleISOTemps(nfsDir, name)
	tmp, err := os.MkdirTemp(nfsDir, isoTreeExtractInfix+name+"_")
	if err != nil {
		return time.Time{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }() // no-op after the rename

	log.Info("Extracting ISO tree", "iso", isoPath, "dest", dest)
	start := time.Now()
	if err := extractISOTree(log, isoPath, tmp); err != nil {
		return time.Time{}, err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return time.Time{}, fmt.Errorf("chmod tree: %w", err)
	}

	// Drop the marker first: if we stop before writing the new one, the next
	// reconcile extracts again instead of trusting a half-replaced state.
	if err := os.Remove(marker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, fmt.Errorf("removing marker: %w", err)
	}
	var old string
	if _, err := os.Lstat(dest); err == nil {
		old = filepath.Join(nfsDir, isoTreeOldInfix+name+"_"+strings.TrimPrefix(filepath.Base(tmp), isoTreeExtractInfix+name+"_"))
		if err := os.Rename(dest, old); err != nil {
			return time.Time{}, fmt.Errorf("moving old tree aside: %w", err)
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return time.Time{}, fmt.Errorf("renaming tree into place: %w", err)
	}
	if old != "" {
		if err := os.RemoveAll(old); err != nil {
			log.Error(err, "Failed to remove old ISO tree", "path", old)
		}
	}
	if err := writeFileAtomic(marker, []byte(source)); err != nil {
		return time.Time{}, fmt.Errorf("writing marker: %w", err)
	}
	mi, err := os.Stat(marker)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat marker: %w", err)
	}
	log.Info("ISO tree extracted", "dest", dest, "seconds", time.Since(start).Seconds())
	return mi.ModTime(), nil
}

// removeISOTree deletes the tree, the marker and any leftovers for name.
func removeISOTree(nfsDir, name string) error {
	if nfsDir == "" {
		return nil
	}
	if err := os.Remove(isoTreeMarker(nfsDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	removeStaleISOTemps(nfsDir, name)
	return os.RemoveAll(filepath.Join(nfsDir, name))
}

// removeStaleISOTemps removes temporary directories left behind for name by
// an interrupted extraction.
func removeStaleISOTemps(nfsDir, name string) {
	entries, err := os.ReadDir(nfsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, isoTreeExtractInfix+name+"_") || strings.HasPrefix(n, isoTreeOldInfix+name+"_") {
			_ = os.RemoveAll(filepath.Join(nfsDir, n))
		}
	}
}

// writeFileAtomic writes data to p (mode 0644) through a temporary file.
func writeFileAtomic(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, p)
}

// isoSymlink is a Rock Ridge symlink found during extraction.
type isoSymlink struct{ path, target string }

type isoTreeExtractor struct {
	log    logr.Logger
	fsys   filesystem.FileSystem
	root   *os.Root
	budget int64 // bytes still allowed; the files of a sane ISO fit in the image
	links  []isoSymlink
	buf    []byte
}

// extractISOTree unpacks every directory, regular file and in-tree symlink of
// the ISO at isoPath into the existing directory dest. Rock Ridge names are
// kept; directories get mode 0755 and files 0644 so the tree is readable
// through the "other" permission bits alone.
func extractISOTree(log logr.Logger, isoPath, dest string) error {
	info, err := os.Stat(isoPath)
	if err != nil {
		return fmt.Errorf("stat iso %q: %w", isoPath, err)
	}
	disk, err := diskfs.Open(isoPath, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return fmt.Errorf("opening iso %q: %w", isoPath, err)
	}
	defer func() { _ = disk.Close() }()
	fsys, err := disk.GetFilesystem(0)
	if err != nil {
		return fmt.Errorf("reading iso filesystem: %w", err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("opening %q: %w", dest, err)
	}
	defer func() { _ = root.Close() }()

	x := &isoTreeExtractor{log: log, fsys: fsys, root: root, budget: info.Size(), buf: make([]byte, 1<<20)}
	if err := x.walk(".", 0); err != nil {
		return err
	}
	for _, l := range x.links {
		if !symlinkStaysInside(l.path, l.target) {
			log.Info("Skipping ISO symlink that leaves the tree", "path", l.path, "target", l.target)
			continue
		}
		if err := root.Symlink(l.target, l.path); err != nil {
			return fmt.Errorf("creating symlink %q: %w", l.path, err)
		}
	}
	return nil
}

func (x *isoTreeExtractor) walk(dir string, depth int) error {
	if depth > maxISOTreeDepth {
		return fmt.Errorf("iso directory %q nested too deep", dir)
	}
	entries, err := x.fsys.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading iso directory %q: %w", dir, err)
	}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !isSafeEntryName(name) {
			return fmt.Errorf("unsafe name %q in iso directory %q", name, dir)
		}
		if seen[name] {
			return fmt.Errorf("duplicate name %q in iso directory %q", name, dir)
		}
		seen[name] = true
		rel := path.Join(dir, name)

		info, err := e.Info()
		if err != nil {
			return fmt.Errorf("stat %q in iso: %w", rel, err)
		}
		st, _ := info.Sys().(*iso9660.StatT)
		if st != nil && st.ExtAttrSize != 0 {
			return fmt.Errorf("%q in iso has an extended attribute record (unsupported)", rel)
		}

		switch mode := info.Mode(); {
		case mode&fs.ModeSymlink != 0:
			target := ""
			if st != nil {
				target = st.LinkTarget
			}
			x.links = append(x.links, isoSymlink{path: rel, target: target})
		case info.IsDir():
			if err := x.root.Mkdir(rel, 0o755); err != nil {
				return fmt.Errorf("creating directory %q: %w", rel, err)
			}
			if err := x.root.Chmod(rel, 0o755); err != nil {
				return fmt.Errorf("chmod %q: %w", rel, err)
			}
			if err := x.walk(rel, depth+1); err != nil {
				return err
			}
		case mode.IsRegular():
			if err := x.copyFile(rel, info.Size()); err != nil {
				return err
			}
		default:
			x.log.Info("Skipping special file in ISO", "path", rel, "mode", mode.String())
		}
	}
	return nil
}

func (x *isoTreeExtractor) copyFile(rel string, size int64) error {
	x.budget -= size
	if x.budget < 0 {
		return fmt.Errorf("iso files add up to more than the image size (at %q)", rel)
	}
	src, err := x.fsys.Open(rel)
	if err != nil {
		return fmt.Errorf("opening %q in iso: %w", rel, err)
	}
	defer func() { _ = src.Close() }()
	dst, err := x.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("creating %q: %w", rel, err)
	}
	n, err := io.CopyBuffer(&syncWriter{f: dst}, io.LimitReader(src, size+1), x.buf)
	if err != nil {
		_ = dst.Close()
		return fmt.Errorf("copying %q: %w", rel, err)
	}
	if n != size {
		_ = dst.Close()
		return fmt.Errorf("copying %q: got %d bytes, want %d", rel, n, size)
	}
	if err := dst.Chmod(0o644); err != nil {
		_ = dst.Close()
		return fmt.Errorf("chmod %q: %w", rel, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("closing %q: %w", rel, err)
	}
	return nil
}

// syncEvery is how many bytes syncWriter writes between two fsyncs.
const syncEvery = 32 << 20

// syncWriter writes to f and flushes it to disk every syncEvery bytes.
// Page cache dirtied by a write is charged to the container's memory cgroup
// until it is written back. Downloading a 3 GB ISO over a fast link, or
// unpacking it disk to disk, writes faster than writeback, so without the
// fsyncs the dirty cache got the controller OOM-killed at its 128Mi limit.
// Written-back pages are clean and the kernel reclaims them under the limit.
// Every large file the controller writes goes through it.
// It has no ReadFrom, so io.CopyBuffer uses the caller's buffer.
type syncWriter struct {
	f       *os.File
	pending int64
}

func (w *syncWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.pending += int64(n)
	if err == nil && w.pending >= syncEvery {
		w.pending = 0
		err = w.f.Sync()
	}
	return n, err
}

// isSafeEntryName reports whether name is a single, plain path element.
func isSafeEntryName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\\x00")
}

// symlinkStaysInside reports whether a symlink at linkPath (relative to the
// tree root) with the given target resolves inside the tree. Only relative
// targets whose ".." elements all come first are accepted: they resolve
// lexically from the link's real directory, so nested symlinks cannot be
// used to climb out of the tree.
func symlinkStaysInside(linkPath, target string) bool {
	if target == "" || strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\\\x00") {
		return false
	}
	descended := false
	for el := range strings.SplitSeq(target, "/") {
		switch el {
		case "", ".":
		case "..":
			if descended {
				return false
			}
		default:
			descended = true
		}
	}
	resolved := path.Join(path.Dir(linkPath), target)
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

// copyFromTree copies the file at rel inside treeDir (following symlinks only
// within the tree) to dst, atomically and with mode 0444.
func copyFromTree(treeDir, rel, dst string) error {
	root, err := os.OpenRoot(treeDir)
	if err != nil {
		return fmt.Errorf("opening tree: %w", err)
	}
	defer func() { _ = root.Close() }()
	src, err := root.Open(strings.TrimPrefix(rel, "/"))
	if err != nil {
		return fmt.Errorf("opening %q in iso: %w", rel, err)
	}
	defer func() { _ = src.Close() }()
	if fi, err := src.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%q in iso is not a regular file", rel)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".extract-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err := io.Copy(&syncWriter{f: tmp}, src); err != nil {
		return fmt.Errorf("copying %q: %w", rel, err)
	}
	if err := tmp.Chmod(0o444); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// treeFileMatches reports whether dst already holds the same bytes as rel in
// the extracted tree at treeDir.
func treeFileMatches(treeDir, rel, dst string) (bool, error) {
	root, err := os.OpenRoot(treeDir)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	src, err := root.Open(strings.TrimPrefix(rel, "/"))
	if err != nil {
		return false, err
	}
	defer func() { _ = src.Close() }()
	cur, err := os.Open(dst)
	if err != nil {
		return false, err
	}
	defer func() { _ = cur.Close() }()

	srcInfo, err := src.Stat()
	if err != nil {
		return false, err
	}
	curInfo, err := cur.Stat()
	if err != nil {
		return false, err
	}
	if !srcInfo.Mode().IsRegular() || !curInfo.Mode().IsRegular() || srcInfo.Size() != curInfo.Size() {
		return false, nil
	}

	srcBuf := make([]byte, 1<<20)
	curBuf := make([]byte, 1<<20)
	for {
		srcN, srcErr := io.ReadFull(src, srcBuf)
		curN, curErr := io.ReadFull(cur, curBuf)
		if srcN != curN || !bytes.Equal(srcBuf[:srcN], curBuf[:curN]) {
			return false, nil
		}
		srcDone := errors.Is(srcErr, io.EOF) || errors.Is(srcErr, io.ErrUnexpectedEOF)
		curDone := errors.Is(curErr, io.EOF) || errors.Is(curErr, io.ErrUnexpectedEOF)
		if srcDone || curDone {
			return srcDone && curDone, nil
		}
		if srcErr != nil {
			return false, srcErr
		}
		if curErr != nil {
			return false, curErr
		}
	}
}
