package convert

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ErrAVInsufficientDiskSpace is returned when EnforceMinFreeDisk determines
// the destination filesystem does not have enough free space to safely
// absorb an ffmpeg transcode's working set -- fail-closed (mirrors
// ErrAVResolutionExceeded's shape/doc-comment, avduration.go), rejecting a
// job BEFORE the expensive ffmpeg subprocess ever runs (D-06/T-36-01).
var ErrAVInsufficientDiskSpace = errors.New("av: insufficient free disk space")

// EnforceMinFreeDisk fail-closes when dir's filesystem does not currently
// have at least inputSizeBytes*safetyFactor bytes of REAL free space
// available to an unprivileged process. It reads unix.Statfs's Bavail field
// (blocks available to an unprivileged user), never Bfree (total free
// blocks, which includes root-reserved blocks this process could never
// actually use) -- the same distinction that makes df's "available" column
// differ from its "free" column.
//
// This checks the REAL free space at guard time rather than reserving a
// fixed per-job allocation (Assumption A2, 36-RESEARCH.md): reading live
// filesystem state automatically reflects whatever OTHER jobs are
// concurrently writing to the same container filesystem at the moment this
// guard runs. That is strictly better information than a static
// per-container budget divided by expected concurrency, at the cost of a
// theoretical TOCTOU race between this check and ffmpeg's actual writes --
// acceptable because ffmpeg hitting ENOSPC mid-write is still a safe,
// terminal failure (the job fails, no corruption, no crash), so this guard
// exists to make that outcome RARE under normal load, not to make it
// impossible under adversarial concurrency. Defense-in-depth, not a hard
// reservation.
func EnforceMinFreeDisk(dir string, inputSizeBytes int64, safetyFactor float64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("av: statfs %q: %w", dir, err)
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	needed := uint64(float64(inputSizeBytes) * safetyFactor)
	if free < needed {
		return fmt.Errorf("%w: free=%d needed=%d input=%d factor=%.2f",
			ErrAVInsufficientDiskSpace, free, needed, inputSizeBytes, safetyFactor)
	}
	return nil
}
