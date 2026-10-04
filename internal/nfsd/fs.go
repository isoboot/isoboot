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
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5"
)

// invalidPath is what Join returns when an element is not a plain file
// name. It contains a NUL byte, so no lookup can ever resolve it.
const invalidPath = "\x00invalid"

// writeFlags are the open flags that would modify the filesystem.
const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC

// roFS is a read-only billy.Filesystem confined to one export directory.
//
// All access goes through os.Root, so neither ".." nor a symbolic link can
// reach outside the export. It reports no write capability, which makes
// go-nfs answer every mutating call with NFS3ERR_ROFS.
type roFS struct {
	name string
	root *os.Root
}

var (
	_ billy.Filesystem = (*roFS)(nil)
	_ billy.Capable    = (*roFS)(nil)
)

// validName reports whether name is a single, plain path element.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

// local converts a path built by Join into a path for os.Root.
func local(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	for elem := range strings.SplitSeq(p, "/") {
		if !validName(elem) {
			return "", fs.ErrNotExist
		}
	}
	return p, nil
}

// Capabilities declares the filesystem read-only.
func (f *roFS) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.SeekCapability
}

// Join joins path elements. go-nfs passes the name from a LOOKUP request
// as one element without checking it, so every element is validated here:
// a name holding a slash, "." or ".." yields a path that never resolves.
func (f *roFS) Join(elem ...string) string {
	for _, e := range elem {
		if !validName(e) {
			return invalidPath
		}
	}
	return strings.Join(elem, "/")
}

func (f *roFS) Stat(filename string) (os.FileInfo, error) {
	p, err := local(filename)
	if err != nil {
		return nil, err
	}
	return f.root.Stat(p)
}

func (f *roFS) Lstat(filename string) (os.FileInfo, error) {
	p, err := local(filename)
	if err != nil {
		return nil, err
	}
	return f.root.Lstat(p)
}

func (f *roFS) Readlink(link string) (string, error) {
	p, err := local(link)
	if err != nil {
		return "", err
	}
	return f.root.Readlink(p)
}

// Open opens a regular file for reading. Anything else (a FIFO would block
// the server, a device has no business in an export) is refused.
func (f *roFS) Open(filename string) (billy.File, error) {
	p, err := local(filename)
	if err != nil {
		return nil, err
	}
	info, err := f.root.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fs.ErrPermission
	}
	file, err := f.root.Open(p)
	if err != nil {
		return nil, err
	}
	return &roFile{File: file}, nil
}

func (f *roFS) OpenFile(filename string, flag int, _ os.FileMode) (billy.File, error) {
	if flag&writeFlags != 0 {
		return nil, billy.ErrReadOnly
	}
	return f.Open(filename)
}

// ReadDir lists a directory, sorted by name so that READDIR cookies mean
// the same thing on every call and after a restart.
func (f *roFS) ReadDir(dir string) ([]os.FileInfo, error) {
	p, err := local(dir)
	if err != nil {
		return nil, err
	}
	d, err := f.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	infos := make([]os.FileInfo, 0, len(names))
	for _, name := range names {
		if !validName(name) {
			continue
		}
		child := name
		if p != "." {
			child = p + "/" + name
		}
		info, err := f.root.Lstat(child)
		if err != nil {
			continue // removed while listing
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (f *roFS) Create(string) (billy.File, error)           { return nil, billy.ErrReadOnly }
func (f *roFS) Rename(string, string) error                 { return billy.ErrReadOnly }
func (f *roFS) Remove(string) error                         { return billy.ErrReadOnly }
func (f *roFS) TempFile(string, string) (billy.File, error) { return nil, billy.ErrReadOnly }
func (f *roFS) MkdirAll(string, os.FileMode) error          { return billy.ErrReadOnly }
func (f *roFS) Symlink(string, string) error                { return billy.ErrReadOnly }
func (f *roFS) Chroot(string) (billy.Filesystem, error)     { return nil, billy.ErrNotSupported }

// Root returns the export path as clients see it.
func (f *roFS) Root() string { return "/" + f.name }

// roFile is a read-only billy.File.
type roFile struct {
	*os.File
}

func (f *roFile) Write([]byte) (int, error) { return 0, billy.ErrReadOnly }
func (f *roFile) Truncate(int64) error      { return billy.ErrReadOnly }
func (f *roFile) Lock() error               { return nil }
func (f *roFile) Unlock() error             { return nil }
