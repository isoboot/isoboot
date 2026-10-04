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
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
)

// handlerMount sends a MOUNT request straight to the handler.
func handlerMount(t *testing.T, h *Handler, dirpath string) (nfs.MountStatus, billy.Filesystem) {
	t.Helper()
	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()
	status, fsys, _ := h.Mount(context.Background(), server, nfs.MountRequest{Dirpath: []byte(dirpath)})
	return status, fsys
}

func newTestHandler(t *testing.T, root string) *Handler {
	t.Helper()
	h, err := NewHandler(root, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func TestExports(t *testing.T) {
	h := newTestHandler(t, newTree(t))
	// Not exported: the file "secret", the hidden directory, the symlink.
	if got, want := h.Exports(), []string{"/iso", "/other"}; !slices.Equal(got, want) {
		t.Errorf("Exports() = %v, want %v", got, want)
	}
}

func TestMountValidation(t *testing.T) {
	h := newTestHandler(t, newTree(t))
	tests := []struct {
		dirpath string
		want    nfs.MountStatus
	}{
		{"/iso", nfs.MountStatusOk},
		{"/other", nfs.MountStatusOk},
		{"/missing", nfs.MountStatusErrNoEnt},
		{"/secret", nfs.MountStatusErrNoEnt}, // a file, not a directory
		{"/link", nfs.MountStatusErrNoEnt},   // a symlink to an export
		{"", nfs.MountStatusErrAcces},
		{"/", nfs.MountStatusErrAcces},
		{"iso", nfs.MountStatusErrAcces},
		{"//iso", nfs.MountStatusErrAcces},
		{"/iso/", nfs.MountStatusErrAcces},
		{"/iso/casper", nfs.MountStatusErrAcces},
		{"/.", nfs.MountStatusErrAcces},
		{"/..", nfs.MountStatusErrAcces},
		{"/../iso", nfs.MountStatusErrAcces},
		{"/iso/..", nfs.MountStatusErrAcces},
		{"/iso/../secret", nfs.MountStatusErrAcces},
		{"/.hidden", nfs.MountStatusErrAcces},
		{"/iso\x00", nfs.MountStatusErrAcces},
		{"/iso,port=20049", nfs.MountStatusErrAcces},
		{"/etc", nfs.MountStatusErrNoEnt},
		{"/etc/passwd", nfs.MountStatusErrAcces},
	}
	for _, tt := range tests {
		t.Run(tt.dirpath, func(t *testing.T) {
			status, fsys := handlerMount(t, h, tt.dirpath)
			if status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
			if (fsys != nil) != (tt.want == nfs.MountStatusOk) {
				t.Errorf("filesystem returned = %v", fsys != nil)
			}
			if status != nfs.MountStatusOk && len(h.ToHandle(fsys, nil)) != 0 {
				t.Error("a refused mount must not yield a root handle")
			}
		})
	}
}

func TestHandlesSurviveRestart(t *testing.T) {
	root := newTree(t)
	paths := [][]string{nil, {"hello.txt"}, {"casper"}, {"casper", "vmlinuz"}, {".disk", "info"}, {"ubuntu"}}

	first, err := NewHandler(root, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	_, fsys := handlerMount(t, first, "/iso")
	_, otherFS := handlerMount(t, first, "/other")
	issued := make([][]byte, len(paths))
	for i, p := range paths {
		issued[i] = first.ToHandle(fsys, p)
		if len(issued[i]) != HandleLen || len(issued[i])%4 != 0 || len(issued[i]) > 64 {
			t.Fatalf("handle for %v has length %d", p, len(issued[i]))
		}
	}
	if bytes.Equal(first.ToHandle(fsys, []string{"hello.txt"}), first.ToHandle(otherFS, []string{"hello.txt"})) {
		t.Error("the same path in two exports has the same handle")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// A new process: nothing is mounted and no handle has been issued,
	// yet every handle from before resolves to the same file.
	second := newTestHandler(t, root)
	for i, p := range paths {
		gotFS, gotPath, err := second.FromHandle(issued[i])
		if err != nil {
			t.Fatalf("handle for %v after restart: %v", p, err)
		}
		if !slices.Equal(gotPath, p) {
			t.Errorf("handle for %v resolved to %v", p, gotPath)
		}
		if again := second.ToHandle(gotFS, gotPath); !bytes.Equal(again, issued[i]) {
			t.Errorf("handle for %v changed after restart", p)
		}
	}
}

func TestFromHandleRejects(t *testing.T) {
	h := newTestHandler(t, newTree(t))
	_, fsys := handlerMount(t, h, "/iso")
	good := h.ToHandle(fsys, []string{"hello.txt"})
	flipped := slices.Clone(good)
	flipped[HandleLen-1] ^= 1
	wrongMagic := slices.Clone(good)
	wrongMagic[3] = 2
	tests := map[string][]byte{
		"empty":       nil,
		"short":       good[:28],
		"long":        append(slices.Clone(good), 0, 0, 0, 0),
		"unknown":     flipped,
		"wrong magic": wrongMagic,
		"zero":        make([]byte, HandleLen),
		"bad name":    h.ToHandle(fsys, []string{"..", "secret"}),
		"slash name":  h.ToHandle(fsys, []string{"casper/vmlinuz"}),
	}
	for name, fh := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := h.FromHandle(fh); err == nil {
				t.Error("FromHandle accepted the handle")
			}
		})
	}
}

// go-nfs appends the looked-up name to the path FromHandle returns.
func TestFromHandleReturnsCopy(t *testing.T) {
	h := newTestHandler(t, newTree(t))
	_, fsys := handlerMount(t, h, "/iso")
	fh := h.ToHandle(fsys, []string{"casper"})
	_, p1, _ := h.FromHandle(fh)
	_ = append(p1, "x")
	p1[0] = "changed"
	if _, p2, _ := h.FromHandle(fh); !slices.Equal(p2, []string{"casper"}) {
		t.Errorf("stored path was modified through the returned slice: %v", p2)
	}
}

func TestExportAddedAndReplaced(t *testing.T) {
	root := newTree(t)
	h := newTestHandler(t, root)

	// An export unpacked after the server started is found at mount time.
	mustWrite(t, filepath.Join(root, "new", "file"), []byte("v1"))
	status, fsys := handlerMount(t, h, "/new")
	if status != nfs.MountStatusOk {
		t.Fatalf("mount of new export: status %d", status)
	}
	fh := h.ToHandle(fsys, []string{"file"})

	// Replacing the directory makes the handler open the new one.
	if err := os.RemoveAll(filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "new", "file"), []byte("version 2"))
	if status, _ := handlerMount(t, h, "/new"); status != nfs.MountStatusOk {
		t.Fatalf("mount of replaced export: status %d", status)
	}
	gotFS, gotPath, err := h.FromHandle(fh)
	if err != nil {
		t.Fatal(err)
	}
	info, err := gotFS.Stat(gotFS.Join(gotPath...))
	if err != nil || info.Size() != int64(len("version 2")) {
		t.Errorf("handle after replacement: info %v, err %v", info, err)
	}

	// A removed export disappears at the next rescan.
	if err := os.RemoveAll(filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	if err := h.Rescan(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.FromHandle(fh); err == nil {
		t.Error("handle of a removed export still resolves")
	}
	if status, _ := handlerMount(t, h, "/new"); status != nfs.MountStatusErrNoEnt {
		t.Errorf("mount of removed export: status %d", status)
	}
}

func TestFilesystemIsConfined(t *testing.T) {
	h := newTestHandler(t, newTree(t))
	_, fsys := handlerMount(t, h, "/iso")

	joins := [][]string{
		{".."}, {"..", "secret"}, {"casper", ".."}, {"."}, {""}, {"casper/vmlinuz"},
		{"/etc/passwd"}, {"a\x00b"}, {"casper", "../../secret"},
	}
	for _, elems := range joins {
		p := fsys.Join(elems...)
		if _, err := fsys.Lstat(p); err == nil {
			t.Errorf("Lstat(Join(%q)) succeeded", elems)
		}
		if _, err := fsys.Open(p); err == nil {
			t.Errorf("Open(Join(%q)) succeeded", elems)
		}
	}
	// Paths that did not come from Join are checked as well.
	for _, p := range []string{"../secret", "casper/../../secret", "/etc/hosts", "./hello.txt", "casper//vmlinuz"} {
		if _, err := fsys.Stat(p); err == nil {
			t.Errorf("Stat(%q) succeeded", p)
		}
		if _, err := fsys.ReadDir(p); err == nil {
			t.Errorf("ReadDir(%q) succeeded", p)
		}
	}

	// Symbolic links are served as links, and never followed out.
	for _, link := range []string{"escape", "absolute"} {
		info, err := fsys.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Lstat(%q) = %v, %v; want a symlink", link, info, err)
		}
		if _, err := fsys.Stat(link); err == nil {
			t.Errorf("Stat(%q) followed a link out of the export", link)
		}
		if _, err := fsys.Open(link); err == nil {
			t.Errorf("Open(%q) followed a link out of the export", link)
		}
	}
	if target, err := fsys.Readlink("escape"); err != nil || target != "../secret" {
		t.Errorf("Readlink = %q, %v", target, err)
	}
	if info, err := fsys.Stat("ubuntu"); err != nil || !info.IsDir() {
		t.Errorf("Stat of a link inside the export = %v, %v", info, err)
	}
}

func TestFilesystemIsReadOnly(t *testing.T) {
	root := newTree(t)
	h := newTestHandler(t, root)
	_, fsys := handlerMount(t, h, "/iso")

	if billy.CapabilityCheck(fsys, billy.WriteCapability) {
		t.Error("filesystem declares write capability")
	}
	if !billy.CapabilityCheck(fsys, billy.ReadCapability) {
		t.Error("filesystem does not declare read capability")
	}
	if h.Change(fsys) != nil {
		t.Error("Change() must be nil for a read-only filesystem")
	}

	check := func(op string, err error) {
		t.Helper()
		if err != billy.ErrReadOnly {
			t.Errorf("%s: err = %v, want ErrReadOnly", op, err)
		}
	}
	_, err := fsys.Create("new.txt")
	check("Create", err)
	_, err = fsys.OpenFile("hello.txt", os.O_RDWR, 0o644)
	check("OpenFile(O_RDWR)", err)
	_, err = fsys.OpenFile("new.txt", os.O_CREATE|os.O_RDONLY, 0o644)
	check("OpenFile(O_CREATE)", err)
	_, err = fsys.OpenFile("hello.txt", os.O_TRUNC, 0o644)
	check("OpenFile(O_TRUNC)", err)
	_, err = fsys.TempFile("", "x")
	check("TempFile", err)
	check("Rename", fsys.Rename("hello.txt", "bye.txt"))
	check("Remove", fsys.Remove("hello.txt"))
	check("MkdirAll", fsys.MkdirAll("dir", 0o755))
	check("Symlink", fsys.Symlink("hello.txt", "link"))

	file, err := fsys.Open("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, err = file.Write([]byte("x"))
	check("File.Write", err)
	check("File.Truncate", file.Truncate(0))

	if data, err := os.ReadFile(filepath.Join(root, "iso", "hello.txt")); err != nil || string(data) != "hello over NFS\n" {
		t.Errorf("file changed on disk: %q, %v", data, err)
	}
	for _, name := range []string{"new.txt", "bye.txt", "dir", "link"} {
		if _, err := os.Lstat(filepath.Join(root, "iso", name)); !os.IsNotExist(err) {
			t.Errorf("%s exists on disk", name)
		}
	}
}

func TestOpenRefusesSpecialFiles(t *testing.T) {
	root := newTree(t)
	if err := syscall.Mkfifo(filepath.Join(root, "iso", "fifo"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	h := newTestHandler(t, root)
	_, fsys := handlerMount(t, h, "/iso")
	for _, name := range []string{"fifo", "casper"} {
		if _, err := fsys.Open(name); err == nil {
			t.Errorf("Open(%q) succeeded", name)
		}
	}
}

func TestReadDirIsSorted(t *testing.T) {
	h := newTestHandler(t, newTree(t))
	_, fsys := handlerMount(t, h, "/iso")
	infos, err := fsys.ReadDir("")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name()
	}
	want := []string{".disk", "absolute", "casper", "escape", "hello.txt", "many", "ubuntu"}
	if !slices.Equal(names, want) {
		t.Errorf("ReadDir = %v, want %v", names, want)
	}
}
