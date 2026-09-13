// Package staleness compares verified image identities without managing any
// process lifecycle. Products supply image verification; launchers supply the
// actual replacement selector. Missing evidence always produces Unknown.
package staleness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type State string

const (
	Same          State = "same"
	Different     State = "different"
	Unknown       State = "unknown"
	maxImageBytes int64 = 256 << 20
)

// Identity names verified executable content, not a release label or a path.
// Scheme determines what content is covered (for example, a validated Darwin
// CodeDirectory or an entire Linux executable). Product and Platform must also
// agree before two identities are comparable. Evidence acquisition belongs to
// the product; populating this struct does not authenticate a caller's claim.
type Identity struct {
	Scheme   string `json:"scheme"`
	Digest   string `json:"digest"`
	Product  string `json:"product"`
	Platform string `json:"platform"`
}

func (i Identity) valid() bool {
	if i.Scheme == "" || i.Product == "" || i.Platform == "" || len(i.Digest) < 40 || len(i.Digest)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(i.Digest)
	return err == nil
}

// Target describes a launch selector. Only an absolute selector can be
// resolved here without reconstructing a different process's PATH or cwd.
type Target struct {
	Kind     string `json:"kind"`
	Selector string `json:"selector,omitempty"`
}

type Observation struct {
	State     State     `json:"state"`
	Reason    string    `json:"reason"`
	Running   Identity  `json:"running"`
	Candidate *Identity `json:"candidate,omitempty"`
	Target    Target    `json:"target"`
}

// Inspector verifies a private, immutable snapshot of an executable. It must
// not execute the image and must return an error for unverifiable content.
// It is called synchronously; implementations should honor ctx where possible.
type Inspector func(ctx context.Context, snapshotPath string) (Identity, error)

// Compare reports content equality, difference or missing/incompatible proof.
// It makes no version-ordering judgment and grants no replacement permission.
func Compare(running, candidate Identity) (State, string) {
	if !running.valid() {
		return Unknown, "running-identity-unavailable"
	}
	if !candidate.valid() {
		return Unknown, "candidate-identity-unavailable"
	}
	if running.Scheme != candidate.Scheme || running.Product != candidate.Product || running.Platform != candidate.Platform {
		return Unknown, "candidate-not-comparable"
	}
	if strings.EqualFold(running.Digest, candidate.Digest) {
		return Same, "same-image"
	}
	return Different, "different-replacement-available"
}

// Observe resolves the selector afresh, verifies a bounded private snapshot,
// then checks that the selector and source bytes still match that snapshot.
// Timestamps/inodes only detect races; they never establish content equality.
// This is a point-in-time observation, not a reservation of the next exec.
func Observe(ctx context.Context, running Identity, target Target, inspect Inspector) Observation {
	o := Observation{State: Unknown, Running: running, Target: target}
	if !running.valid() {
		o.Reason = "running-identity-unavailable"
		return o
	}
	if target.Kind != "absolute" || !filepath.IsAbs(target.Selector) {
		o.Reason = "replacement-selector-unavailable"
		return o
	}
	if inspect == nil || ctx.Err() != nil {
		o.Reason = "inspection-unavailable"
		return o
	}
	resolved, err := filepath.EvalSymlinks(target.Selector)
	if err != nil {
		o.Reason = "replacement-unavailable"
		return o
	}
	f, err := openImage(resolved)
	if err != nil {
		o.Reason = "replacement-unavailable"
		return o
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Size() <= 0 || before.Size() > maxImageBytes {
		o.Reason = "replacement-not-executable"
		return o
	}
	dir, err := os.MkdirTemp("", "mcp-image-*")
	if err != nil {
		o.Reason = "snapshot-unavailable"
		return o
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "image")
	snapshot, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		o.Reason = "snapshot-unavailable"
		return o
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(snapshot, hash), io.LimitReader(contextReader{ctx, f}, maxImageBytes+1))
	closeErr := snapshot.Close()
	if copyErr != nil || closeErr != nil || n != before.Size() {
		o.Reason = "replacement-changed-during-inspection"
		return o
	}
	identity, err := inspect(ctx, path)
	if err != nil || ctx.Err() != nil {
		o.Reason = "candidate-verification-failed"
		return o
	}
	// Read the same open file again, detecting in-place writes as well as
	// atomic replacement and symlink retargeting. A path-only inspector could
	// accidentally validate a different inode between lookup and inspection.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		o.Reason = "replacement-changed-during-inspection"
		return o
	}
	check := sha256.New()
	n, err = io.Copy(check, io.LimitReader(contextReader{ctx, f}, maxImageBytes+1))
	after, statErr := f.Stat()
	selected, resolveErr := filepath.EvalSymlinks(target.Selector)
	current, currentErr := os.Stat(target.Selector)
	if err != nil || statErr != nil || resolveErr != nil || currentErr != nil ||
		n != before.Size() || selected != resolved || !os.SameFile(before, current) ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) ||
		current.Mode().Perm()&0111 == 0 ||
		!before.ModTime().Equal(current.ModTime()) || current.Size() != before.Size() ||
		hex.EncodeToString(hash.Sum(nil)) != hex.EncodeToString(check.Sum(nil)) {
		o.Reason = "replacement-changed-during-inspection"
		return o
	}
	o.Candidate = &identity
	o.State, o.Reason = Compare(running, identity)
	return o
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
