// Package execution binds isolated check results to immutable input and policy.
package execution

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxArchiveBytes = 32 << 20
const maxTreeBytes = 128 << 20
const maxTreeFiles = 10000

// Snapshot carries a caller-pinned source revision and an uncompressed archive.
type Snapshot struct {
	Head    string
	SHA256  string
	Archive []byte
}

func validateSnapshot(snapshot Snapshot) error {
	if !commitID(snapshot.Head) {
		return errors.New("execution requires an immutable source commit")
	}
	sum := sha256.Sum256(snapshot.Archive)
	if snapshot.SHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("snapshot hash differs from the pinned input")
	}
	return walkArchive(snapshot.Archive, nil)
}

func commitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func walkArchive(raw []byte, visit func(*tar.Header, io.Reader) error) error {
	if len(raw) == 0 || len(raw) > maxArchiveBytes {
		return errors.New("snapshot archive size is unsupported")
	}
	reader := tar.NewReader(bytes.NewReader(raw))
	seen := map[string]bool{}
	var total int64
	files := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("snapshot archive is malformed")
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || name == ".." || strings.ContainsAny(name, "\\\x00") || name == ".git" || strings.HasPrefix(name, ".git/") {
			return errors.New("snapshot contains an unsafe path")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errors.New("snapshot links and special files are unsupported")
		}
		if seen[name] {
			return errors.New("snapshot contains duplicate paths")
		}
		seen[name] = true
		files++
		total += header.Size
		if files > maxTreeFiles || header.Size < 0 || total > maxTreeBytes {
			return errors.New("snapshot exceeds file or unpacked size limits")
		}
		if visit != nil {
			if err := visit(header, reader); err != nil {
				return err
			}
		}
	}
}

// ExtractSnapshot validates the entire archive before creating files. Links,
// ownership metadata and special permission bits never reach the destination.
func ExtractSnapshot(snapshot Snapshot, destination string) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("snapshot destination must be empty")
	}
	return walkArchive(snapshot.Archive, func(header *tar.Header, reader io.Reader) error {
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		if header.Typeflag == tar.TypeDir {
			return os.MkdirAll(target, 0700)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600) | os.FileMode(header.Mode)&0100
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("unpack snapshot: %w", copyErr)
		}
		return closeErr
	})
}
