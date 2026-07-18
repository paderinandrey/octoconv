package convert

import "testing"

// TestCgroupCPULimit table-tests parseCPUMax -- the host-testable parsing
// core of CgroupCPULimit -- against every case the phase's behavior block
// requires, with no filesystem/container dependency.
func TestCgroupCPULimit(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantN  int
		wantOK bool
	}{
		{"cpus=2.0 floors to 2", "200000 100000", 2, true},
		{"cpus=1.5 floors to 1, never below 1", "150000 100000", 1, true},
		{"cpus=0.5 clamps to minimum 1", "50000 100000", 1, true},
		{"unlimited quota falls back", "max 100000", 0, false},
		{"garbage falls back", "garbage", 0, false},
		{"empty falls back", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := parseCPUMax(tc.in)
			if n != tc.wantN || ok != tc.wantOK {
				t.Errorf("parseCPUMax(%q) = (%d, %v), want (%d, %v)", tc.in, n, ok, tc.wantN, tc.wantOK)
			}
		})
	}
}

// TestCgroupCPULimit_UnreadableFile pins CgroupCPULimit's fail-open
// contract: on a non-cgroup-v2 host (or outside any container),
// /sys/fs/cgroup/cpu.max is either absent or not in the expected format,
// and the function must return (0, false) rather than panicking or
// erroring loudly -- the caller (cmd/audio-worker) falls back to
// runtime.NumCPU() in that case.
func TestCgroupCPULimit_UnreadableFile(t *testing.T) {
	n, ok := CgroupCPULimit()
	// This test's host may or may not be running under cgroup v2 (CI
	// runners, OrbStack host, developer laptops all differ) -- so it
	// cannot assert a specific (n, ok) pair. It only asserts the function
	// does not panic and returns a value consistent with its own contract:
	// ok implies n >= 1; !ok implies n == 0.
	if ok && n < 1 {
		t.Errorf("CgroupCPULimit() = (%d, true), want n >= 1 when ok", n)
	}
	if !ok && n != 0 {
		t.Errorf("CgroupCPULimit() = (%d, false), want n == 0 when not ok", n)
	}
}
