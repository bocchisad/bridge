package transfer

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Kind string

const (
	KindFile   Kind = "file"
	KindFolder Kind = "folder"
)

type Prepared struct {
	Name   string
	Size   int64
	SHA256 string
	Kind   Kind
	Path   string
}

func Prepare(path string) (*Prepared, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		hash, size, err := hashTar(abs)
		if err != nil {
			return nil, err
		}
		return &Prepared{
			Name:   filepath.Base(abs) + ".tar",
			Size:   size,
			SHA256: hash,
			Kind:   KindFolder,
			Path:   abs,
		}, nil
	}
	hash, size, err := hashFile(abs)
	if err != nil {
		return nil, err
	}
	return &Prepared{
		Name:   filepath.Base(abs),
		Size:   size,
		SHA256: hash,
		Kind:   KindFile,
		Path:   abs,
	}, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func hashTar(root string) (string, int64, error) {
	h := sha256.New()
	cw := &countingWriter{w: h}
	if err := writeTar(cw, root); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), cw.n, nil
}

func Stream(w io.Writer, prepared *Prepared, progress func(sent int64)) error {
	var src io.Reader
	var closer io.Closer
	pr, pw := io.Pipe()

	if prepared.Kind == KindFolder {
		go func() {
			err := writeTar(pw, prepared.Path)
			_ = pw.CloseWithError(err)
		}()
		src = pr
		closer = pr
	} else {
		f, err := os.Open(prepared.Path)
		if err != nil {
			return err
		}
		src = f
		closer = f
	}
	defer closer.Close()

	buf := make([]byte, 32*1024)
	var sent int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			sent += int64(n)
			if progress != nil {
				progress(sent)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func writeTar(w io.Writer, root string) error {
	tw := tar.NewWriter(w)
	defer tw.Close()
	base := filepath.Base(root)
	var paths []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(base, rel))
		if rel == "." {
			name = base + "/"
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if info.IsDir() {
			if !strings.HasSuffix(hdr.Name, "/") {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type ReceiveResult struct {
	Path string
	OK   bool
}

func Receive(r io.Reader, inbox, name string, expectedSize int64, expectedHash string, kind Kind, progress func(got int64)) (ReceiveResult, error) {
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return ReceiveResult{}, err
	}
	tmp := filepath.Join(inbox, name+".partial")
	final := filepath.Join(inbox, name)
	final = uniquePath(final)

	f, err := os.Create(tmp)
	if err != nil {
		return ReceiveResult{}, err
	}
	h := sha256.New()
	mw := io.MultiWriter(f, h)
	buf := make([]byte, 32*1024)
	var got int64
	for expectedSize < 0 || got < expectedSize {
		toRead := len(buf)
		if expectedSize >= 0 {
			remain := expectedSize - got
			if remain == 0 {
				break
			}
			if int64(toRead) > remain {
				toRead = int(remain)
			}
		}
		n, err := r.Read(buf[:toRead])
		if n > 0 {
			if _, werr := mw.Write(buf[:n]); werr != nil {
				f.Close()
				_ = os.Remove(tmp)
				return ReceiveResult{}, werr
			}
			got += int64(n)
			if progress != nil {
				progress(got)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return ReceiveResult{}, err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return ReceiveResult{}, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if expectedSize >= 0 && got != expectedSize {
		corrupt := final + ".corrupt"
		_ = os.Rename(tmp, corrupt)
		return ReceiveResult{Path: corrupt, OK: false}, fmt.Errorf("size mismatch: got %d want %d", got, expectedSize)
	}
	if sum != expectedHash {
		corrupt := final + ".corrupt"
		_ = os.Rename(tmp, corrupt)
		return ReceiveResult{Path: corrupt, OK: false}, fmt.Errorf("hash mismatch")
	}
	if kind == KindFolder {
		extractDir := strings.TrimSuffix(final, ".tar")
		extractDir = uniquePath(extractDir)
		if err := extractTar(tmp, extractDir); err != nil {
			_ = os.Remove(tmp)
			return ReceiveResult{}, err
		}
		_ = os.Remove(tmp)
		return ReceiveResult{Path: extractDir, OK: true}, nil
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return ReceiveResult{}, err
	}
	return ReceiveResult{Path: final, OK: true}, nil
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}

func extractTar(tarPath, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Strip top-level folder name; extract into dest
		name := hdr.Name
		parts := strings.SplitN(name, "/", 2)
		rel := name
		if len(parts) == 2 {
			rel = parts[1]
		} else if strings.HasSuffix(name, "/") {
			continue
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dest)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(dest) {
			return fmt.Errorf("invalid tar path: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}
