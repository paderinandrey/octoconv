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
//   - stderr is captured and surfaced in the returned error for diagnostics.
//
// The caller is expected to pass a ctx carrying the engine timeout.
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	// Kill the entire process group on context cancellation/timeout.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		// Negative pid targets the process group led by the child.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done // reap
		return fmt.Errorf("%s killed: %w", name, ctx.Err())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s failed: %w: %s", name, err, stderr.String())
		}
		return nil
	}
}
