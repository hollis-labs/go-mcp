package staleness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testIdentity(data []byte) Identity {
	h := sha256.Sum256(data)
	return Identity{Scheme: "sha256", Digest: hex.EncodeToString(h[:]), Product: "test/product", Platform: "test/platform"}
}

func inspectBytes(_ context.Context, path string) (Identity, error) {
	b, err := os.ReadFile(path)
	return testIdentity(b), err
}

func TestCompareRequiresComparableContentEvidence(t *testing.T) {
	a := testIdentity([]byte("original"))
	b := testIdentity([]byte("rollback"))
	other := a
	other.Product = "wrapper"
	for _, tc := range []struct {
		name               string
		running, candidate Identity
		state              State
	}{
		{"identical", a, a, Same},
		{"different including rollback", a, b, Different},
		{"missing running", Identity{}, a, Unknown},
		{"missing candidate", a, Identity{}, Unknown},
		{"different product", a, other, Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if state, _ := Compare(tc.running, tc.candidate); state != tc.state {
				t.Fatalf("got %s want %s", state, tc.state)
			}
		})
	}
}

func TestObserveReplacementAndSymlinkSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable snapshot provider")
	}
	dir := t.TempDir()
	a, b, selector := filepath.Join(dir, "elsewhere-A"), filepath.Join(dir, "elsewhere-B"), filepath.Join(dir, "selected")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(a, "original")
	write(b, "changed!")
	if err := os.Symlink(a, selector); err != nil {
		t.Fatal(err)
	}
	running := testIdentity([]byte("original"))
	target := Target{Kind: "absolute", Selector: selector}
	var snapshot string
	inspect := func(ctx context.Context, path string) (Identity, error) {
		snapshot = path
		info, err := os.Stat(path)
		if err != nil {
			return Identity{}, err
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("snapshot exposed permissions: %v", info.Mode())
		}
		return inspectBytes(ctx, path)
	}
	if got := Observe(context.Background(), running, target, inspect); got.State != Same {
		t.Fatalf("same image: %+v", got)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot not removed: %v", err)
	}
	// Replacing with identical bytes changes the inode, not the identity.
	copyPath := filepath.Join(dir, "same-bytes")
	write(copyPath, "original")
	if err := os.Rename(copyPath, a); err != nil {
		t.Fatal(err)
	}
	if got := Observe(context.Background(), running, target, inspect); got.State != Same {
		t.Fatalf("identical-byte replacement: %+v", got)
	}
	if err := os.Remove(selector); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b, selector); err != nil {
		t.Fatal(err)
	}
	if got := Observe(context.Background(), running, target, inspect); got.State != Different {
		t.Fatalf("symlink replacement: %+v", got)
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if got := Observe(context.Background(), running, target, inspect); got.State != Unknown {
		t.Fatalf("missing candidate: %+v", got)
	}
}

func TestObserveRejectsChangesDuringInspection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable snapshot provider")
	}
	for _, mode := range []string{"atomic", "in-place-same-size-and-mtime", "symlink", "removed-exec"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			selected, other := filepath.Join(dir, "selected"), filepath.Join(dir, "other")
			for _, path := range []string{selected, other} {
				if err := os.WriteFile(path, []byte("original"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			target := Target{Kind: "absolute", Selector: selected}
			before, err := os.Stat(selected)
			if err != nil {
				t.Fatal(err)
			}
			inspect := func(ctx context.Context, snapshot string) (Identity, error) {
				identity, err := inspectBytes(ctx, snapshot)
				if err != nil {
					return identity, err
				}
				switch mode {
				case "removed-exec":
					err = os.Chmod(selected, 0600)
				case "atomic":
					err = os.Rename(other, selected)
				case "in-place-same-size-and-mtime":
					err = os.WriteFile(selected, []byte("modified"), 0700)
					if err == nil {
						err = os.Chtimes(selected, before.ModTime(), before.ModTime())
					}
				case "symlink":
					err = os.Remove(selected)
					if err == nil {
						err = os.Symlink(other, selected)
					}
				}
				return identity, err
			}
			got := Observe(context.Background(), testIdentity([]byte("original")), target, inspect)
			if got.State != Unknown || got.Reason != "replacement-changed-during-inspection" {
				t.Fatalf("race accepted: %+v", got)
			}
		})
	}
}

func TestObserveFailsClosedBeforeInspectingUnusableInputs(t *testing.T) {
	for _, target := range []Target{{Kind: "path", Selector: "app"}, {Kind: "relative", Selector: "./app"}, {Kind: "absolute", Selector: "./app"}, {}} {
		got := Observe(context.Background(), testIdentity([]byte("original")), target, func(context.Context, string) (Identity, error) {
			t.Fatal("inspected an unresolvable selector")
			return Identity{}, nil
		})
		if got.State != Unknown {
			t.Fatalf("unverified selector: %+v", got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := Observe(ctx, testIdentity([]byte("original")), Target{Kind: "absolute", Selector: "/unopened"}, inspectBytes)
	if got.State != Unknown {
		t.Fatal(got)
	}
}

func TestObserveDoesNotPublishInspectorErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable snapshot provider")
	}
	path := filepath.Join(t.TempDir(), "candidate")
	if err := os.WriteFile(path, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	got := Observe(context.Background(), testIdentity([]byte("original")), Target{Kind: "absolute", Selector: path}, func(context.Context, string) (Identity, error) { return Identity{}, fmt.Errorf("private-credential") })
	if got.State != Unknown || strings.Contains(fmt.Sprint(got), "private-credential") {
		t.Fatalf("unsafe result: %+v", got)
	}
}
