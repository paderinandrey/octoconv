package convert

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// statfsFreeBytes reads dir's real available-to-unprivileged-user free
// space via the same unix.Statfs call EnforceMinFreeDisk itself uses, so
// tests can construct exact-boundary and guaranteed-insufficient cases
// without assuming any specific free-space number for this host.
func statfsFreeBytes(t *testing.T, dir string) uint64 {
	t.Helper()
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		t.Fatalf("statfs %q: %v", dir, err)
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize)
}

// TestEnforceMinFreeDisk pins the five fail-closed behaviors from the plan's
// <behavior> block: success at/above the threshold, failure below it, the
// exact-boundary edge (free == needed is NOT an error), a statfs error on a
// bogus path (distinguishable from the insufficient-space sentinel), and
// errors.Is matching on the insufficient-space path.
func TestEnforceMinFreeDisk(t *testing.T) {
	dir := t.TempDir()
	free := statfsFreeBytes(t, dir)

	t.Run("sufficient space passes", func(t *testing.T) {
		// A tiny input size relative to any real filesystem's free space --
		// stable on any host, never assumes a specific free-space number.
		if err := EnforceMinFreeDisk(dir, 1, 3.0); err != nil {
			t.Fatalf("EnforceMinFreeDisk(dir, 1, 3.0) = %v, want nil", err)
		}
	})

	t.Run("insufficient space fails closed", func(t *testing.T) {
		if free == 0 {
			t.Skip("host reports zero free bytes; cannot construct a guaranteed-insufficient case")
		}
		// safetyFactor 2.0 against an input sized to the ENTIRE free space
		// guarantees needed (2x free) exceeds free, regardless of how much
		// free space this host happens to report.
		err := EnforceMinFreeDisk(dir, int64(free), 2.0)
		if err == nil {
			t.Fatal("EnforceMinFreeDisk(dir, free, 2.0) = nil, want error")
		}
		if !errors.Is(err, ErrAVInsufficientDiskSpace) {
			t.Errorf("error = %v, want errors.Is(err, ErrAVInsufficientDiskSpace)", err)
		}
	})

	t.Run("exact boundary passes", func(t *testing.T) {
		if free == 0 {
			t.Skip("host reports zero free bytes; cannot construct the exact-boundary case")
		}
		// needed == inputSizeBytes * safetyFactor == free * 1.0 == free --
		// the guard's condition is free < needed (strict), so free == needed
		// must NOT be an error.
		if err := EnforceMinFreeDisk(dir, int64(free), 1.0); err != nil {
			t.Fatalf("EnforceMinFreeDisk at exact boundary (free==needed) = %v, want nil", err)
		}
	})

	t.Run("statfs error on bogus path is not the insufficient-space sentinel", func(t *testing.T) {
		err := EnforceMinFreeDisk("/nonexistent/bogus/path/for/avdiskguard/test", 1, 1.0)
		if err == nil {
			t.Fatal("EnforceMinFreeDisk on a nonexistent path = nil, want a statfs error")
		}
		if errors.Is(err, ErrAVInsufficientDiskSpace) {
			t.Errorf("statfs error on a bogus path = %v, want a plain wrapped statfs error, NOT ErrAVInsufficientDiskSpace", err)
		}
	})

	t.Run("errors.Is matches sentinel on the insufficient-space path", func(t *testing.T) {
		if free == 0 {
			t.Skip("host reports zero free bytes; cannot construct a guaranteed-insufficient case")
		}
		err := EnforceMinFreeDisk(dir, int64(free)+1, 1.0)
		if !errors.Is(err, ErrAVInsufficientDiskSpace) {
			t.Errorf("errors.Is(err, ErrAVInsufficientDiskSpace) = false for err=%v, want true", err)
		}
	})
}
