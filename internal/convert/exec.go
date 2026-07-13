package convert

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

// runCommand executes an external engine with hardened process handling:
//
//   - the child runs in its own process group (Setpgid), so when ctx is
//     cancelled or its deadline fires we kill the whole group — children such as
//     LibreOffice's soffice.bin do not get orphaned/left hanging;
//   - stdout is captured and returned to the caller (some engines, e.g.
//     veraPDF's `--format xml`, write their entire machine-readable result to
//     stdout rather than a file, D-04/phase 23 -- verified live in
//     scripts/verapdf-measure.sh) and stderr is captured and folded into the
//     returned error for diagnostics.
//
// The caller is expected to pass a ctx carrying the engine timeout. stdout is
// returned even when the process exits non-zero (D-09: some engines use a
// non-zero exit code to report a valid-but-negative result rather than a
// process failure -- e.g. veraPDF exits 1 for a non-compliant-but-valid
// report -- so the caller, not runCommand, decides how to interpret
// exit-code-vs-output).
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	// Kill the entire process group on context cancellation/timeout.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		// Negative pid targets the process group led by the child.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done // reap
		return nil, fmt.Errorf("%s killed: %w", name, ctx.Err())
	case err := <-done:
		if err != nil {
			return stdout.Bytes(), fmt.Errorf("%s failed: %w: %s", name, err, stderr.String())
		}
		return stdout.Bytes(), nil
	}
}
