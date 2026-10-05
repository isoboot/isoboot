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
package nfsd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
)

const (
	// HandleLen is the length of every file handle: a multiple of four
	// (go-nfs does not consume XDR padding) and within the NFSv3 limit of 64.
	HandleLen = 32
	// maxLoggedPath bounds how much of a client-supplied path is logged.
	maxLoggedPath = 128
	// rescanInterval limits how often an unknown handle may trigger a
	// rescan of the exports.
	rescanInterval = 5 * time.Second
)

// handleMagic starts every handle; the last byte is the format version.
var handleMagic = [4]byte{'i', 's', 'o', 1}

// exportNameRegexp is what an export directory must be called to be served.
var exportNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

var errStaleHandle = errors.New("unknown file handle")

type handleKey [HandleLen]byte

// export is one immediate subdirectory of the root.
type export struct {
	name string
	fs   *roFS
	info os.FileInfo
}

type handleEntry struct {
	exp  *export
	path []string
}

// Handler serves the immediate subdirectories of a root directory as
// read-only NFS exports named "/<name>".
//
// A file handle is a hash of the export name and the path inside it, so
// the same file has the same handle after a restart. The handles of every
// file are computed when an export is first seen, which is how a handle
// issued before a restart is found again.
type Handler struct {
	log  *slog.Logger
	root *os.Root

	mu         sync.RWMutex
	exports    map[string]*export
	handles    map[handleKey]handleEntry
	lastRescan time.Time
}

var _ nfs.Handler = (*Handler)(nil)

// NewHandler opens rootDir and indexes the exports found in it.
func NewHandler(rootDir string, log *slog.Logger) (*Handler, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}
	h := &Handler{
		log:     log,
		root:    root,
		exports: map[string]*export{},
		handles: map[handleKey]handleEntry{},
	}
	if err := h.Rescan(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return h, nil
}

// Close releases the directories held open by the handler.
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for name := range h.exports {
		h.dropExport(name)
	}
	return h.root.Close()
}

// Exports returns the names of the exports currently known, sorted.
func (h *Handler) Exports() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.exports))
	for name := range h.exports {
		names = append(names, "/"+name)
	}
	sort.Strings(names)
	return names
}

// Rescan brings the set of exports in line with the root directory.
func (h *Handler) Rescan() error {
	dir, err := h.root.Open(".")
	if err != nil {
		return fmt.Errorf("list root: %w", err)
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return fmt.Errorf("list root: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastRescan = time.Now()
	found := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !exportNameRegexp.MatchString(name) {
			continue
		}
		if _, err := h.loadExport(name); err != nil {
			h.log.Warn("export skipped", "export", "/"+name, "error", err)
			continue
		}
		found[name] = true
	}
	for name := range h.exports {
		if !found[name] {
			h.dropExport(name)
			h.log.Info("export removed", "export", "/"+name)
		}
	}
	return nil
}

// loadExport returns the export called name, opening and indexing it if it
// is new or its directory has been replaced. The caller holds h.mu.
func (h *Handler) loadExport(name string) (*export, error) {
	info, err := h.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
	}
	if cur := h.exports[name]; cur != nil && os.SameFile(cur.info, info) {
		return cur, nil
	}

	root, err := h.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(opened, info) {
		_ = root.Close()
		return nil, fmt.Errorf("%s: changed while opening", name)
	}

	h.dropExport(name)
	exp := &export{name: name, fs: &roFS{name: name, root: root}, info: info}
	h.exports[name] = exp
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if p == "." {
				return walkErr
			}
			h.log.Warn("unreadable path in export", "export", "/"+name, "path", p, "error", walkErr)
			return fs.SkipDir
		}
		var elems []string
		if p != "." {
			elems = strings.Split(p, "/")
		}
		h.handles[makeHandle(name, elems)] = handleEntry{exp: exp, path: elems}
		count++
		return nil
	})
	if err != nil {
		h.dropExport(name)
		return nil, err
	}
	h.log.Info("export indexed", "export", "/"+name, "entries", count)
	return exp, nil
}

// dropExport forgets an export and its handles. The caller holds h.mu.
func (h *Handler) dropExport(name string) {
	exp := h.exports[name]
	if exp == nil {
		return
	}
	for key, entry := range h.handles {
		if entry.exp == exp {
			delete(h.handles, key)
		}
	}
	delete(h.exports, name)
	_ = exp.fs.root.Close()
}

// exportName extracts the export name from the path of a MOUNT request.
// Only "/<name>" is accepted: no sub-paths, no "..", no hidden names.
func exportName(dirpath string) (string, bool) {
	name, ok := strings.CutPrefix(dirpath, "/")
	if !ok || !exportNameRegexp.MatchString(name) {
		return "", false
	}
	return name, true
}

func makeHandle(exportName string, path []string) handleKey {
	sum := sha256.Sum256([]byte(exportName + "\x00" + strings.Join(path, "/")))
	var key handleKey
	copy(key[:], handleMagic[:])
	copy(key[len(handleMagic):], sum[:])
	return key
}

// Mount answers a MOUNT request. Only existing exports can be mounted.
func (h *Handler) Mount(
	_ context.Context, conn net.Conn, req nfs.MountRequest,
) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	client := conn.RemoteAddr().String()
	requested := string(req.Dirpath[:min(len(req.Dirpath), maxLoggedPath)])

	name, ok := exportName(string(req.Dirpath))
	if !ok {
		h.log.Warn("mount refused", "client", client, "path", requested, "reason", "not an export path")
		return nfs.MountStatusErrAcces, nil, nil
	}
	h.mu.Lock()
	exp, err := h.loadExport(name)
	h.mu.Unlock()
	if err != nil {
		h.log.Warn("mount refused", "client", client, "path", requested, "reason", "no such export")
		return nfs.MountStatusErrNoEnt, nil, nil
	}
	h.log.Info("mount", "client", client, "export", "/"+name)
	return nfs.MountStatusOk, exp.fs, []nfs.AuthFlavor{nfs.AuthFlavorUnix, nfs.AuthFlavorNull}
}

// Change returns nil: the exports are read-only.
func (h *Handler) Change(billy.Filesystem) billy.Change { return nil }

// FSStat keeps the defaults go-nfs fills in for a read-only filesystem.
func (h *Handler) FSStat(context.Context, billy.Filesystem, *nfs.FSStat) error { return nil }

// ToHandle returns the handle of a path inside an export.
func (h *Handler) ToHandle(f billy.Filesystem, path []string) []byte {
	rf, ok := f.(*roFS)
	if !ok || rf == nil {
		return nil
	}
	if slices.ContainsFunc(path, func(e string) bool { return !validName(e) }) {
		return make([]byte, HandleLen) // never resolves: wrong magic
	}
	key := makeHandle(rf.name, path)

	h.mu.RLock()
	_, known := h.handles[key]
	h.mu.RUnlock()
	if !known {
		h.mu.Lock()
		if exp := h.exports[rf.name]; exp != nil && exp.fs == rf {
			h.handles[key] = handleEntry{exp: exp, path: slices.Clone(path)}
		}
		h.mu.Unlock()
	}
	return key[:]
}

// FromHandle resolves a handle to an export and a path inside it.
func (h *Handler) FromHandle(fh []byte) (billy.Filesystem, []string, error) {
	if len(fh) != HandleLen || [4]byte(fh[:4]) != handleMagic {
		return nil, nil, errStaleHandle
	}
	key := handleKey(fh)
	entry, ok := h.lookup(key)
	if !ok && h.rescanDue() {
		if err := h.Rescan(); err != nil {
			h.log.Error("rescan failed", "error", err)
		}
		entry, ok = h.lookup(key)
	}
	if !ok {
		return nil, nil, errStaleHandle
	}
	// A copy: go-nfs appends to the slice it is given.
	return entry.exp.fs, slices.Clone(entry.path), nil
}

func (h *Handler) lookup(key handleKey) (handleEntry, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entry, ok := h.handles[key]
	return entry, ok
}

func (h *Handler) rescanDue() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return time.Since(h.lastRescan) >= rescanInterval
}

// InvalidateHandle does nothing: handles are derived, not allocated.
func (h *Handler) InvalidateHandle(billy.Filesystem, []byte) error { return nil }

// HandleLimit is unbounded: handles cost nothing to keep.
func (h *Handler) HandleLimit() int { return math.MaxInt32 }
