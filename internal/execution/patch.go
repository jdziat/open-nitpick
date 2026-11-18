package execution

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// NewSnapshot binds complete file contents to an immutable source revision.
func NewSnapshot(head string, files map[string][]byte) (Snapshot, error) {
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		data := files[path]
		if archive.Len()+len(data) > maxArchiveBytes {
			return Snapshot{}, errors.New("snapshot exceeds archive size limit")
		}
		if err := w.WriteHeader(&tar.Header{Name: path, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return Snapshot{}, err
		}
		if _, err := w.Write(data); err != nil {
			return Snapshot{}, err
		}
	}
	if err := w.Close(); err != nil {
		return Snapshot{}, err
	}
	sum := sha256.Sum256(archive.Bytes())
	snapshot := Snapshot{Head: head, SHA256: hex.EncodeToString(sum[:]), Archive: archive.Bytes()}
	return snapshot, validateSnapshot(snapshot)
}

// Comparison records baseline failures separately from the candidate's checks.
// Passed requires both snapshots to pass; it is not a semantic repair verdict.
type Comparison struct {
	Baseline    Result `json:"baseline"`
	Candidate   Result `json:"candidate"`
	PatchSHA256 string `json:"patch_sha256"`
}

func patchedSnapshot(base Snapshot, edits map[string][]byte) (Snapshot, string, error) {
	if err := validateSnapshot(base); err != nil {
		return Snapshot{}, "", err
	}
	if len(edits) == 0 {
		return Snapshot{}, "", errors.New("no patch to check")
	}
	files := map[string][]byte{}
	if err := walkArchive(base.Archive, func(header *tar.Header, reader io.Reader) error {
		if header.Typeflag == tar.TypeDir {
			return nil
		}
		data, err := io.ReadAll(reader)
		files[header.Name] = data
		return err
	}); err != nil {
		return Snapshot{}, "", err
	}
	changed := false
	for path, content := range edits {
		old, ok := files[path]
		if !ok {
			return Snapshot{}, "", errors.New("patch names a path outside the source snapshot")
		}
		changed = changed || !bytes.Equal(old, content)
		files[path] = content
	}
	if !changed {
		return Snapshot{}, "", errors.New("patch does not change the source snapshot")
	}
	candidate, err := NewSnapshot(base.Head, files)
	raw, marshalErr := json.Marshal(edits)
	if marshalErr != nil {
		return Snapshot{}, "", marshalErr
	}
	sum := sha256.Sum256(raw)
	return candidate, hex.EncodeToString(sum[:]), err
}

// RunPatch runs one candidate and its baseline with the same profile and policy.
func (r *Runner) RunPatch(ctx context.Context, base Snapshot, edits map[string][]byte, check Check) (Comparison, error) {
	candidate, digest, err := patchedSnapshot(base, edits)
	if err != nil {
		return Comparison{}, err
	}
	comparison := Comparison{PatchSHA256: digest, Baseline: r.Run(ctx, base, check)}
	comparison.Candidate = r.Run(ctx, candidate, check)
	return comparison, nil
}

// VerifyPatch refuses reused evidence after any input, check or head changes.
func (r *Runner) VerifyPatch(comparison Comparison, base Snapshot, edits map[string][]byte, check Check, currentHead string) error {
	candidate, digest, err := patchedSnapshot(base, edits)
	if err != nil {
		return err
	}
	if digest != comparison.PatchSHA256 {
		return errors.New("checked patch differs from the proposed edits")
	}
	if err := r.VerifyReceipt(comparison.Baseline, base, check, currentHead); err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	if err := r.VerifyReceipt(comparison.Candidate, candidate, check, currentHead); err != nil {
		return fmt.Errorf("candidate: %w", err)
	}
	return nil
}
