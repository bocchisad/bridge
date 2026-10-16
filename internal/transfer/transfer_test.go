package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareAndReceiveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(src, []byte("hello bridge"), 0o644); err != nil {
		t.Fatal(err)
	}
	prep, err := Prepare(src)
	if err != nil {
		t.Fatal(err)
	}
	if prep.Kind != KindFile {
		t.Fatalf("kind %s", prep.Kind)
	}

	var buf bytes.Buffer
	if err := Stream(&buf, prep, nil); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(dir, "inbox")
	res, err := Receive(bytes.NewReader(buf.Bytes()), inbox, prep.Name, prep.Size, prep.SHA256, prep.Kind, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatal("expected ok")
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello bridge" {
		t.Fatalf("got %q", got)
	}
}

func TestFolderTarDeterministic(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "folder")
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "1.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b1, b2 bytes.Buffer
	if err := writeTar(&b1, root); err != nil {
		t.Fatal(err)
	}
	if err := writeTar(&b2, root); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
		t.Fatal("tar output not deterministic")
	}
	h1 := sha256.Sum256(b1.Bytes())
	h2 := sha256.Sum256(b2.Bytes())
	if hex.EncodeToString(h1[:]) != hex.EncodeToString(h2[:]) {
		t.Fatal("hash mismatch")
	}
}

func TestHashMismatchCorrupt(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox")
	data := []byte("payload")
	res, err := Receive(bytes.NewReader(data), inbox, "x.bin", int64(len(data)), "deadbeef", KindFile, nil)
	if err == nil {
		t.Fatal("expected hash error")
	}
	if res.OK {
		t.Fatal("should not be ok")
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatal("expected corrupt file on disk")
	}
}
