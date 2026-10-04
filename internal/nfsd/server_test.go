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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	nfsc "github.com/willscott/go-nfs-client/nfs"
)

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestServeRead(t *testing.T) {
	root := newTree(t)
	addr, _ := startServer(t, root)
	target := mustMount(t, addr, "/iso")

	for _, name := range []string{"hello.txt", ".disk/info", "casper/vmlinuz", "casper/big.squashfs"} {
		want, err := os.ReadFile(filepath.Join(root, "iso", name))
		if err != nil {
			t.Fatal(err)
		}
		file, err := target.Open("/" + name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		got, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: read %d bytes, want %d, content differs", name, len(got), len(want))
		}
		_ = file.Close()
	}
}

func TestServeMountRefused(t *testing.T) {
	addr, _ := startServer(t, newTree(t))
	for _, export := range []string{"/", "/missing", "/iso/casper", "/../iso", "/secret", "/.hidden", "/link"} {
		if _, err := mount(t, addr, export); err == nil {
			t.Errorf("mount %q succeeded", export)
		}
	}
}

// File IDs are the inode numbers on disk, and symlinks are served as links.
func TestServeAttributes(t *testing.T) {
	root := newTree(t)
	addr, _ := startServer(t, root)
	target := mustMount(t, addr, "/iso")

	const typeRegular, typeDirectory, typeLink = 1, 2, 5
	tests := []struct {
		name     string
		wantType uint32
	}{
		{"hello.txt", typeRegular},
		{"casper", typeDirectory},
		{"casper/vmlinuz", typeRegular},
		{"ubuntu", typeLink},
		{"escape", typeLink},
	}
	for _, tt := range tests {
		attr, err := target.Getattr("/" + tt.name)
		if err != nil {
			t.Fatalf("getattr %s: %v", tt.name, err)
		}
		if attr.Type != tt.wantType {
			t.Errorf("%s: type = %d, want %d", tt.name, attr.Type, tt.wantType)
		}
		if want := inode(t, filepath.Join(root, "iso", tt.name)); attr.Fileid != want {
			t.Errorf("%s: fileid = %d, want inode %d", tt.name, attr.Fileid, want)
		}
	}
}

// Listings must be complete across READDIRPLUS pages: casper finds its
// squashfs files with a shell glob.
func TestServeReadDirPlusPaging(t *testing.T) {
	root := newTree(t)
	addr, _ := startServer(t, root)
	target := mustMount(t, addr, "/iso")

	entries, err := target.ReadDirPlus("/many")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.FileName] {
			t.Errorf("%s listed twice", entry.FileName)
		}
		seen[entry.FileName] = true
		if want := inode(t, filepath.Join(root, "iso", "many", entry.FileName)); entry.FileId != want {
			t.Errorf("%s: fileid = %d, want inode %d", entry.FileName, entry.FileId, want)
		}
	}
	for i := range 100 {
		if name := fmt.Sprintf("f-%03d.deb", i); !seen[name] {
			t.Errorf("%s missing from listing", name)
		}
	}
	if len(seen) != 100 {
		t.Errorf("listed %d entries, want 100", len(seen))
	}
}

func TestServeReadOnly(t *testing.T) {
	root := newTree(t)
	addr, _ := startServer(t, root)
	target := mustMount(t, addr, "/iso")

	wantROFS := func(op string, err error) {
		t.Helper()
		var nfsErr *nfsc.Error
		if !errors.As(err, &nfsErr) || nfsErr.ErrorNum != nfsc.NFS3ErrROFS {
			t.Errorf("%s: err = %v, want NFS3ERR_ROFS", op, err)
		}
	}
	_, err := target.Create("/new.txt", 0o644)
	wantROFS("CREATE", err)
	_, err = target.Mkdir("/newdir", 0o755)
	wantROFS("MKDIR", err)
	wantROFS("REMOVE", target.Remove("/hello.txt"))
	wantROFS("RMDIR", target.RmDir("/many"))
	wantROFS("RENAME", target.Rename("/hello.txt", "/bye.txt"))
	wantROFS("SYMLINK", target.Symlink("/hello.txt", "/newlink"))
	wantROFS("SETATTR", target.Setattr("/hello.txt", nfsc.Sattr3{Mode: nfsc.SetMode{SetIt: true, Mode: 0o777}}))

	file, err := target.Open("/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("overwritten")); err == nil {
		err = file.Close()
		wantROFS("WRITE", err)
	} else {
		wantROFS("WRITE", err)
	}

	const accessAll, accessModify, accessExtend, accessDelete = 0x3f, 0x04, 0x08, 0x10
	granted, err := target.Access("/hello.txt", accessAll)
	if err != nil {
		t.Fatal(err)
	}
	if granted&(accessModify|accessExtend|accessDelete) != 0 || granted&1 == 0 {
		t.Errorf("ACCESS granted %#x: want read without any write bit", granted)
	}

	if data, err := os.ReadFile(filepath.Join(root, "iso", "hello.txt")); err != nil || string(data) != "hello over NFS\n" {
		t.Errorf("file changed on disk: %q, %v", data, err)
	}
	for _, name := range []string{"new.txt", "newdir", "bye.txt", "newlink"} {
		if _, err := os.Lstat(filepath.Join(root, "iso", name)); !os.IsNotExist(err) {
			t.Errorf("%s exists on disk", name)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "iso", "many")); err != nil {
		t.Errorf("directory removed: %v", err)
	}
}

// rawLookup sends an NFSv3 LOOKUP with an arbitrary name, which the client
// library would never do, and returns the NFS status of the reply.
func rawLookup(t *testing.T, addr string, dir []byte, name string) uint32 {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	const procLookup = 3
	var body []byte
	for _, w := range []uint32{1, rpcCall, rpcVersion, progNFS, versNFS, procLookup, 0, 0, 0, 0} {
		body = binary.BigEndian.AppendUint32(body, w)
	}
	body = binary.BigEndian.AppendUint32(body, uint32(len(dir)))
	body = append(body, dir...)
	body = binary.BigEndian.AppendUint32(body, uint32(len(name)))
	body = append(body, name...)
	body = append(body, make([]byte, (4-len(name)%4)%4)...)
	call := binary.BigEndian.AppendUint32(nil, lastFragment|uint32(len(body)))
	if _, err := conn.Write(append(call, body...)); err != nil {
		t.Fatal(err)
	}

	// record mark, xid, REPLY, MSG_ACCEPTED, verifier (2 words), accept
	// status, NFS status.
	reply := make([]byte, 32)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("lookup %q: %v", name, err)
	}
	if accept := binary.BigEndian.Uint32(reply[24:]); accept != acceptSuccess {
		t.Fatalf("lookup %q: RPC accept status %d", name, accept)
	}
	return binary.BigEndian.Uint32(reply[28:])
}

// go-nfs hands the LOOKUP name to the filesystem unchecked.
func TestServeLookupTraversal(t *testing.T) {
	addr, _ := startServer(t, newTree(t))
	mustMount(t, addr, "/iso")
	rootFH := makeHandle("iso", nil)
	casperFH := makeHandle("iso", []string{"casper"})

	if status := rawLookup(t, addr, rootFH[:], "hello.txt"); status != nfsc.NFS3Ok {
		t.Fatalf("lookup of a plain name: status %d", status)
	}
	if status := rawLookup(t, addr, casperFH[:], ".."); status != nfsc.NFS3Ok {
		t.Errorf("lookup of .. inside the export: status %d", status)
	}
	if status := rawLookup(t, addr, rootFH[:], ".."); status == nfsc.NFS3Ok {
		t.Error("lookup of .. at the export root succeeded")
	}
	hostile := []string{
		"../secret", "../other/hello.txt", "casper/vmlinuz", "casper/../hello.txt", "./hello.txt",
		"/etc/passwd", "hello.txt/", "escape/x", "ubuntu/hello.txt", "hello.txt\x00", "...",
	}
	for _, name := range hostile {
		if status := rawLookup(t, addr, rootFH[:], name); status != nfsc.NFS3ErrNoEnt {
			t.Errorf("lookup %q: status %d, want NFS3ERR_NOENT", name, status)
		}
	}
	for _, name := range []string{"../../secret", "../hello.txt", "vmlinuz/.."} {
		if status := rawLookup(t, addr, casperFH[:], name); status != nfsc.NFS3ErrNoEnt {
			t.Errorf("lookup %q in casper: status %d, want NFS3ERR_NOENT", name, status)
		}
	}
	if status := rawLookup(t, addr, make([]byte, HandleLen), "hello.txt"); status != nfsc.NFS3ErrStale {
		t.Errorf("lookup with an unknown handle: status %d, want NFS3ERR_STALE", status)
	}
}

// A client that mounted before a restart keeps working afterwards: same
// handles, same file IDs.
func TestServeRestart(t *testing.T) {
	root := newTree(t)
	addr, stop := startServer(t, root)
	target := mustMount(t, addr, "/iso")
	_, fh, err := target.Lookup("/casper/vmlinuz")
	if err != nil {
		t.Fatal(err)
	}
	before, err := target.GetAttr(fh)
	if err != nil {
		t.Fatal(err)
	}
	stop()

	addr, _ = startServer(t, root)
	// The new server has seen no MOUNT for this handle's export through
	// this connection; the old handle must simply work.
	other := mustMount(t, addr, "/other")
	after, err := other.GetAttr(fh)
	if err != nil {
		t.Fatalf("handle from before the restart: %v", err)
	}
	wantID := inode(t, filepath.Join(root, "iso", "casper", "vmlinuz"))
	if after.Fileid != wantID || before.Fileid != wantID {
		t.Errorf("fileid before %d, after %d, inode %d", before.Fileid, after.Fileid, wantID)
	}
	if status := rawLookup(t, addr, makeHandleSlice("iso"), "hello.txt"); status != nfsc.NFS3Ok {
		t.Errorf("lookup under the old root handle: status %d", status)
	}
}

func makeHandleSlice(export string, path ...string) []byte {
	key := makeHandle(export, path)
	return key[:]
}
