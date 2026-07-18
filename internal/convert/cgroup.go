// Package convert's cgroup.go reads the container's real cgroup v2 CPU quota
// so whisper-cli's --threads flag can be sized to what the container is
// actually entitled to run, instead of the host's full core count
// (PITFALLS.md Pitfall 5). This is the first OctoConv engine that needs
// runtime container-resource introspection -- the other three engine
// classes (libvips, LibreOffice, chromium) invoke single-threaded-by-nature
// CLI tools with no comparable knob.
package convert

import (
	"os"
	"strconv"
	"strings"
)

// parseCPUMax parses the two-field content of a cgroup v2 cpu.max file
// ("$QUOTA $PERIOD", e.g. "200000 100000" for --cpus=2.0). Returns the
// floored quota/period thread count and true on success. The floor (not
// ceil) is deliberate: rounding UP would size --threads beyond the CFS
// quota the kernel actually grants, oversubscribing and inviting CPU
// throttling rather than avoiding it (PITFALLS.md Pitfall 5). The result is
// clamped to a minimum of 1 -- a sub-1-core quota (e.g. --cpus=0.5) must
// still run whisper-cli with at least one thread. "max" in the first field
// means the cgroup has no CPU quota (unlimited); the caller must fall back
// (e.g. to runtime.NumCPU()) rather than trying to size --threads to an
// unbounded budget. Any other unparseable shape also falls back.
func parseCPUMax(s string) (int, bool) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, false
	}
	if fields[0] == "max" {
		return 0, false
	}
	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || period == 0 {
		return 0, false
	}
	n := int(quota / period) // floor, not ceil -- see doc comment above
	if n < 1 {
		n = 1
	}
	return n, true
}

// CgroupCPULimit reads /sys/fs/cgroup/cpu.max and returns the floored
// quota/period thread count. Fails open (returns (0, false)) when the file
// is unreadable -- cgroup v1 hosts, or a process running outside any
// container (local `go run` dev flow) -- so the caller can fall back to
// runtime.NumCPU() instead of erroring.
func CgroupCPULimit() (int, bool) {
	b, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0, false
	}
	return parseCPUMax(string(b))
}
