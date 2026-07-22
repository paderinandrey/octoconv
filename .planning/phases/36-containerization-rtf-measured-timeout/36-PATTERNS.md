# Phase 36: Containerization & RTF-Measured Timeout - Pattern Map

**Mapped:** 2026-07-22
**Files analyzed:** 8 (new/modified)
**Analogs found:** 8 / 8 (1 partial — disk guard has no direct precedent, mapped to the nearest structural analog)

This phase is the VIDEO analog of Phase 32 (audio containerization + RTF-measured
timeout). Every artifact below has a direct Phase-32 precedent except the
disk-space guard, which is genuinely novel.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `Dockerfile.av-worker` (NEW) | config (Docker multi-stage build) | file-I/O (build artifact pipeline) | `Dockerfile.audio-worker` | exact |
| `scripts/av-rtf-measure.sh` (NEW) | utility (ops measurement script) | batch (timed subprocess loop) | `scripts/audio-rtf-measure.sh` | exact |
| `internal/convert/avdiskguard.go` (NEW) | utility (fail-closed guard) | request-response (probe → compare → error) | `internal/convert/avduration.go` (`EnforceMaxResolution`) | role-match (novel syscall, familiar shape) |
| `internal/convert/av.go` (MODIFIED — thread config into `AVConverter`) | service/model (converter registration) | CRUD (struct field config, not env-in-package) | `internal/convert/whisper.go` (`SetAudioModelPath`/`SetAudioThreads`, `AudioConverter`) + `internal/api/api.go` (`Config` struct + `NewServer(cfg)`) | role-match |
| `internal/convert/converters.go` (MODIFIED — registration ordering) | config (init wiring) | event-driven (package `init()`) | itself (existing `Default.Register(AVConverter{})` call site) | exact |
| `cmd/av-worker/main.go` (MODIFIED — env wiring, threads/RAM sizing, disk-guard env) | controller (worker entrypoint) | event-driven (asynq consumer bootstrap) | `cmd/audio-worker/main.go` | exact |
| `docker-compose.yml` (MODIFIED — new `av-worker` service + env parity on all queue-client services) | config | request-response / event-driven (service wiring) | `docker-compose.yml`'s own `audio-worker` service block + the `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` parity lines already threaded through every other service | exact |
| `.github/workflows/ci.yml` (MODIFIED — bake matrix + e2e cache-from entries) | config (CI) | batch | `ci.yml`'s existing `audio-worker.cache-to`/`cache-from` lines (docker-build + e2e jobs) | exact |

## Pattern Assignments

### `Dockerfile.av-worker` (NEW)

**Analog:** `Dockerfile.audio-worker` (full file read, 78 lines)

**Full 3-stage shape to mirror** (`Dockerfile.audio-worker:1-78`):
1. `FROM golang:1.26-bookworm AS build` — standard Go binary build (`go mod download`, `COPY . .`, `CGO_ENABLED=0 go build -o /out/<worker> ./cmd/<worker>`).
2. `FROM debian:bookworm-slim AS <engine>-build` — throwaway stage: installs build toolchain, clones the pinned tag with `--depth 1 --branch <tag>`, then `checkout --detach <COMMIT>` + a `[ "$(git -C /path rev-parse HEAD)" = "${COMMIT}" ]` fail-loud guard.
3. Runtime `FROM debian:bookworm-slim` (final, untagged stage) — installs only runtime shared-lib packages, `COPY --from=<engine>-build` the compiled artifact(s), `COPY --from=build /out/<worker> /usr/local/bin/<worker>`, `USER nobody`, `ENTRYPOINT`.

**Build-stage commit-pin guard** (`Dockerfile.audio-worker:29-33`):
```dockerfile
ARG WHISPER_COMMIT=f049fff95a089aa9969deb009cdd4892b3e74916
RUN git clone --depth 1 --branch v1.9.1 \
      https://github.com/ggml-org/whisper.cpp.git /whisper \
 && git -C /whisper checkout --detach "${WHISPER_COMMIT}" \
 && [ "$(git -C /whisper rev-parse HEAD)" = "${WHISPER_COMMIT}" ]
```
For ffmpeg (per RESEARCH.md Pattern 1, D-10), the exact adapted form — note the annotated-tag peeled-commit caveat, which whisper's lightweight tag did NOT need:
```dockerfile
ARG FFMPEG_COMMIT=38b88335f99e76ed89ff3c93f877fdefce736c13
RUN git clone --depth 1 --branch n8.1.2 \
      https://github.com/FFmpeg/FFmpeg.git /ffmpeg \
 && git -C /ffmpeg checkout --detach "${FFMPEG_COMMIT}" \
 && [ "$(git -C /ffmpeg rev-parse HEAD)" = "${FFMPEG_COMMIT}" ]
```
Add a belt-and-suspenders runtime version-string assertion (RESEARCH.md "FFmpeg version/CVE guard", no precedent in `Dockerfile.audio-worker` — audio has no equivalent check because whisper-cli has no analogous `-version` grep convention in this repo):
```dockerfile
RUN /usr/local/bin/ffmpeg -version | grep -q "ffmpeg version n\?8\.1\.2" \
 || { echo "FATAL: built ffmpeg does not report version 8.1.2" >&2; exit 1; }
```

**KEY DIFFERENCE — runtime shared-lib COPY vs whisper's self-contained build:**
`Dockerfile.audio-worker:60-68` COPYs whisper.cpp's OWN compiled `.so` files out of the throwaway build stage (they don't exist as Debian packages):
```dockerfile
COPY --from=whisper-build /whisper/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper-build /whisper/build/bin/*.so* /usr/local/lib/
RUN ldconfig
```
ffmpeg's codec dependencies (`libx264`/`libx265`/`libvpx`/`libopus`/`libmp3lame`/`libwebp`) ARE real Debian bookworm packages — per RESEARCH.md's verified apt-cache audit, install them as runtime `apt-get` packages instead of COPYing `.so` files, mirroring the audio runtime stage's own `ffmpeg` apt-install line (`Dockerfile.audio-worker:55-59`, which the av-worker Dockerfile now REPLACES with the from-source ffmpeg build):
```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates libx264-164 libx265-199 libvpx7 libopus0 libmp3lame0 libwebp7 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg-build /usr/local/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-build /usr/local/bin/ffprobe /usr/local/bin/ffprobe
COPY --from=build /out/av-worker /usr/local/bin/av-worker
USER nobody
ENTRYPOINT ["/usr/local/bin/av-worker"]
```
(No `ldconfig`/`/usr/local/lib` COPY needed for ffmpeg's own binary the way whisper needed it, because the codec libs are proper apt-installed shared libraries already on `ld`'s default search path — only ffmpeg's OWN compiled binary is a throwaway-stage artifact.)

**Build-stage toolchain deps** (`RESEARCH.md` "Installation", verified live against a `debian:bookworm-slim` container):
```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential pkg-config git ca-certificates curl nasm yasm \
      libx264-dev libx265-dev libvpx-dev libopus-dev libmp3lame-dev libwebp-dev
```

**Minimal configure invocation** (RESEARCH.md Pattern 2, verified against `n8.1.2` source):
```dockerfile
RUN ./configure \
      --disable-everything --disable-doc --disable-debug \
      --enable-gpl --enable-nonfree \
      --enable-libx264 --enable-libx265 --enable-libvpx --enable-libopus \
      --enable-libmp3lame --enable-libwebp \
      --enable-encoder=libx264,libx265,libvpx_vp9,aac,libopus,libmp3lame,pcm_s16le,mjpeg,png,libwebp \
      --enable-decoder=h264,hevc,vp8,vp9,aac,mp3,mp3float,opus,pcm_s16le,mjpeg,png \
      --enable-muxer=mp4,mov,ipod,webm,matroska,mp3,wav,image2 \
      --enable-demuxer=mov,matroska,avi,wav \
      --enable-protocol=file,crypto \
      --enable-filter=scale \
 && make -j"$(nproc)" && make install
```
Do NOT pass `--cpu=host`/`--disable-runtime-cpudetect` (cross-arch portability is automatic; no `-DGGML_NATIVE=OFF` equivalent needed — see RESEARCH.md Pattern 3).

**Comment style to mirror:** `Dockerfile.audio-worker`'s WR-xx-tagged inline comments explaining *why* each pin/flag exists (e.g. lines 21-28, 35-39, 42-46, 61-67, 73-77) — the planner/executor should write equivalent AVE/D-xx-tagged comments, not bare instructions.

---

### `scripts/av-rtf-measure.sh` (NEW)

**Analog:** `scripts/audio-rtf-measure.sh` (full file read, 150 lines)

**Full structural skeleton to clone, section by section:**

1. **Header comment block** (`audio-rtf-measure.sh:1-33`) — documents the metric (RTF = wall/duration), the fixture-synthesis strategy, the full pipeline being timed, that peak memory is a companion observation not a gate, and that the script gates MEASUREMENT INTEGRITY ONLY (build/container/fixture/pipeline succeed) — the derived-timeout GO/NO-GO is a SEPARATE decision made by reading the printed p95. Mirror this exactly, adjusted for: matrix (codec × resolution × preset) instead of single fixture; lavfi-synthesized fixture instead of looping a committed WAV.

2. **Knobs** (`audio-rtf-measure.sh:38-46`):
```bash
IMAGE_TAG="${AUDIO_RTF_IMAGE_TAG:-octoconv-audio-worker:rtf-measure}"
RUNS="${AUDIO_RTF_MEASURE_RUNS:-10}"
CPUS="${AUDIO_RTF_CPUS:-2.0}"
MEMORY="${AUDIO_RTF_MEMORY:-1g}"
FIXTURE_DURATION_S="${AUDIO_RTF_FIXTURE_DURATION_S:-300}"
CONTAINER="octoconv-audio-rtf-measure-$$"
WORKDIR=$(mktemp -d)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$WORKDIR"' EXIT
```
Mirror the env-var-overridable knob naming convention (`AV_RTF_*`), the `$$`-suffixed container name, and the `mktemp -d` + EXIT trap. Add a `CODECS`/`RESOLUTIONS` (or similar) matrix-axis knob since D-04 requires a sweep, not one fixture — e.g. `CODECS="${AV_RTF_CODECS:-h264 hevc vp9}"`, `RESOLUTIONS="${AV_RTF_RESOLUTIONS:-480 720 1080}"`.

3. **OrbStack k8s-down discipline gate** (`audio-rtf-measure.sh:55-60`) — copy VERBATIM, same rationale (D-05):
```bash
if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
	echo "FAIL: kubectl reports a live cluster -- stop k8s before running this measurement" >&2
	exit 1
fi
```

4. **Build + long-lived container start** (`audio-rtf-measure.sh:63-70`):
```bash
docker build -f Dockerfile.audio-worker -t "$IMAGE_TAG" .
docker run -d --name "$CONTAINER" \
	--cpus="$CPUS" --memory="$MEMORY" \
	--entrypoint sleep "$IMAGE_TAG" infinity >/dev/null
```
Swap `Dockerfile.audio-worker`/`octoconv-audio-worker` for the av equivalents; `--cpus`/`--memory` must match the eventual compose `av-worker` service block exactly (same discipline as audio).

5. **cgroup-derived thread count** (`audio-rtf-measure.sh:73-77`) — copy VERBATIM (identical cgroup v2 `cpu.max` read, identical awk floor formula):
```bash
CPU_MAX=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/cpu.max)
THREADS=$(echo "$CPU_MAX" | awk '{ if ($1 == "max") { print 1 } else { n = int($1/$2); print (n < 1) ? 1 : n } }')
```

6. **In-container fixture synthesis via lavfi** — the audio script loops a COMMITTED wav (`jfk.wav`); D-04 requires ffmpeg `lavfi` (`testsrc`+`sine`) synthesis instead, never a committed large binary. Adapted shape, one fixture per resolution cell in the matrix:
```bash
docker exec "$CONTAINER" ffmpeg -y -f lavfi \
  -i "testsrc=duration=${FIXTURE_DURATION_S}:size=${W}x${H}:rate=30" \
  -f lavfi -i "sine=frequency=440:duration=${FIXTURE_DURATION_S}" \
  -c:v libx264 -preset ultrafast -c:a pcm_s16le \
  "/tmp/work/fixture_${H}p.mp4"
```
Keep the audio script's `FIXTURE_ACTUAL_DURATION_S=$(... ffprobe -show_entries format=duration ...)` measured-not-assumed re-check pattern (`audio-rtf-measure.sh:84`) for every synthesized fixture.

7. **Timed loop over the FULL production pipeline** (`audio-rtf-measure.sh:98-108`) — same `date +%s%N` before/after, same `seq 1 $RUNS` shape, but iterate over the (codec × resolution × preset) matrix cells, invoking the actual argv the app builds (`transcodeToMP4Args`/`transcodeToWebMArgs` shape from `av.go`) instead of the two-stage ffmpeg-normalize/whisper-cli pipeline:
```bash
RAW_MILLIS=$(docker exec "$CONTAINER" sh -c "
  set -e
  for i in \$(seq 1 $RUNS); do
    start=\$(date +%s%N)
    ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:/tmp/work/fixture_${H}p.mp4 \
      -c:v ${VCODEC} -preset veryfast -crf ${CRF} -c:a aac -b:a 128k -movflags +faststart \
      -threads $THREADS file:/tmp/work/out_\${i}.mp4 >/tmp/work/ffmpeg_\${i}.log 2>&1
    end=\$(date +%s%N)
    echo \$(( (end - start) / 1000000 ))
  done
")
```
Run this per matrix cell (nested loop or separate invocations), tagging each RESULT line with its cell (codec/resolution/preset) — the audio script has no matrix so this is the one genuinely new structural piece; keep every other line (p95 calc, memory, image size) unchanged per cell or aggregated at the end.

8. **p95 nearest-rank calculation** (`audio-rtf-measure.sh:114-127`) — copy VERBATIM per matrix cell:
```bash
RAW_RTF=$(echo "$RAW_MILLIS" | awk -v dur="$FIXTURE_ACTUAL_DURATION_S" '{ printf "%.6f\n", ($1/1000)/dur }')
SORTED=$(echo "$RAW_RTF" | sort -n)
N=$(echo "$SORTED" | wc -l | tr -d ' ')
RANK=$(awk -v n="$N" 'BEGIN { r = n * 0.95; rr = (r == int(r)) ? r : int(r) + 1; print rr }')
P95_RTF=$(echo "$SORTED" | sed -n "${RANK}p")
```

9. **Peak memory (cgroup v2 `memory.peak`)** (`audio-rtf-measure.sh:130-137`) — copy VERBATIM:
```bash
PEAK_BYTES=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo "unavailable")
```

10. **Image size + host-arch caveat + final RESULT line** (`audio-rtf-measure.sh:140-149`) — copy VERBATIM shape, extend the `RESULT` line to include the matrix cell identifier (codec/resolution) per line, e.g.:
```
RESULT cell=hevc@1080 N=$N p95_RTF=${P95_RTF} threads=${THREADS} ...
RESULT cell=vp9@1080 N=$N p95_RTF=${P95_RTF} threads=${THREADS} ...
```
**D-09 requirement:** the VP9/webm cell MUST be measured, not assumed secondary — put it first or explicitly alongside HEVC, per RESEARCH.md Pitfall 1 (35-RESEARCH.md's own prior data shows VP9 already likely to dominate, RTF≈1.41 untuned).

---

### `internal/convert/avdiskguard.go` (NEW, no direct precedent — genuinely novel per D-06)

**Structural analog:** `internal/convert/avduration.go`'s `EnforceMaxResolution`/`enforceMaxResolutionOf` shape (full file read, 195 lines) — same "probe → compare against ceiling → fail-closed sentinel" guard-function convention already used by `EnforceMaxDuration` (`internal/convert/audioduration.go:88-118`, `internal/convert/av.go:421-426` call site) and `EnforceMaxResolution` (`avduration.go:170-194`).

**Sentinel error pattern to mirror** (`avduration.go:10-15`):
```go
// ErrAVResolutionExceeded is returned when the probed video resolution
// exceeds the configured maximum height -- fail-closed (mirrors
// ErrAudioDurationExceeded's shape/doc-comment, audioduration.go), rejecting
// a huge-resolution decode bomb before the expensive ffmpeg stage ever runs
// (AVE-02/T-34-07).
var ErrAVResolutionExceeded = errors.New("declared video resolution exceeds configured maximum")
```
→ `ErrAVInsufficientDiskSpace = errors.New("av: insufficient free disk space for transcode")`

**Guard function shape to mirror** (`avduration.go:170-194`, `EnforceMaxResolution`/`enforceMaxResolutionOf` split — probe-then-check separated so a caller who already probed doesn't spawn a second subprocess, exactly the `avSourceProbe`/`avProbeSource` discipline in `av.go:438-485`):
```go
func EnforceMaxResolution(ctx context.Context, path string, maxHeight int) error {
	streams, err := probeVideoStreams(ctx, path)
	if err != nil {
		return err
	}
	return enforceMaxResolutionOf(streams, maxHeight)
}
func enforceMaxResolutionOf(streams []avVideoStream, maxHeight int) error {
	if height := avMaxVideoHeight(streams); height > maxHeight {
		return fmt.Errorf("%w: declared height %d exceeds ceiling %d", ErrAVResolutionExceeded, height, maxHeight)
	}
	return nil
}
```
The disk guard's "probe" is `unix.Statfs` (no ffprobe subprocess needed), so it collapses to one function rather than a probe/enforce split — RESEARCH.md's recommended shape:
```go
package convert

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

var ErrAVInsufficientDiskSpace = errors.New("av: insufficient free disk space for transcode")

func EnforceMinFreeDisk(dir string, inputSizeBytes int64, safetyFactor float64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("av: statfs %q: %w", dir, err)
	}
	free := stat.Bavail * uint64(stat.Bsize)
	needed := uint64(float64(inputSizeBytes) * safetyFactor)
	if free < needed {
		return fmt.Errorf("%w: free=%d needed=%d (input=%d, factor=%.1f)",
			ErrAVInsufficientDiskSpace, free, needed, inputSizeBytes, safetyFactor)
	}
	return nil
}
```

**Call-site placement** — mirror `av.go`'s `Convert` guard-stage ordering (`av.go:414-426`, duration guard then resolution guard, BOTH before dispatch):
```go
if err := enforceMaxDurationOf(src.duration, avMaxSourceDuration); err != nil {
	return fmt.Errorf("av: %w", err)
}
if err := enforceMaxResolutionOf(src.videoStreams, avMaxSourceResolutionHeight); err != nil {
	return fmt.Errorf("av: %w", err)
}
// NEW: disk guard, same tier, before dispatch
if fi, statErr := os.Stat(inPath); statErr == nil {
	if err := EnforceMinFreeDisk(filepath.Dir(inPath), fi.Size(), avDiskSafetyFactor); err != nil {
		return fmt.Errorf("av: %w", err)
	}
}
```

**FLAGGED — no in-repo analog for the syscall itself:** `golang.org/x/sys/unix.Statfs`/`Statfs_t.Bavail`/`.Bsize` has zero precedent anywhere in `internal/` (confirmed by RESEARCH.md's grep). The dependency (`golang.org/x/sys v0.44.0`) is already resolved indirectly in `go.sum` via `pgx`/other deps — importing `unix` promotes it to direct without a new version resolution, but the syscall usage pattern itself must be written fresh, not copied.

**Test-file analog:** `internal/convert/avduration_test.go` (argv/behavior-pinning test style for `EnforceMaxResolution`) — mirror its table-driven fail-closed assertions for `EnforceMinFreeDisk` (e.g. exact-boundary free==needed, free<needed, statfs error path).

---

### `internal/convert/av.go` (MODIFIED — thread `AV_MAX_DURATION_SECONDS`/resolution ceiling into `AVConverter`)

**Problem (RESEARCH.md Pitfall 4):** `AVConverter` is a zero-field struct (`av.go:45`) registered via `converters.go`'s `init()`, with `avMaxSourceDuration`/`avMaxSourceResolutionHeight` as package-level `const`s (`av.go:231-246`) — there is no existing mechanism to inject env-derived config before `main()` runs, unlike `worker.Handler` which threads `audioMaxDuration` through an explicit constructor parameter.

**Two competing analogs in this codebase — pick the struct-field one, per RESEARCH.md's own recommendation:**

1. **Setter-function pattern** (`internal/convert/whisper.go:14-46`, `SetAudioModelPath`/`SetAudioThreads`) — a package-level `var` written exactly once at startup, read by every subsequent call, no mutex (single-write-before-concurrent-reads):
```go
var audioModelPath string
func SetAudioModelPath(path string) { audioModelPath = path }
var audioThreads int
func SetAudioThreads(n int) { audioThreads = n }
```
Called from `cmd/audio-worker/main.go:87,97`:
```go
convert.SetAudioModelPath(stripInlineComment(os.Getenv("AUDIO_MODEL_PATH")))
threads, threadSource := resolveAudioThreads()
convert.SetAudioThreads(threads)
```
This is the LOWEST-friction option (matches an existing, already-reviewed pattern exactly) but grows a second flavor of "global mutable-at-init state" alongside `convert.Default` (CLAUDE.md explicitly calls out `convert.Default` as the ONLY such state today).

2. **Struct-field + explicit constructor pattern** (`internal/api/api.go:123-194`, `Config` struct + `NewServer(..., cfg Config)`):
```go
type Config struct {
	MaxUploadBytes int64
	MaxEngineBytes map[string]int64
	// ...
}
func NewServer(repo Repo, storage Storage, queue Enqueuer, ..., cfg Config) *Server {
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 100 << 20 // zero-value defaulting
	}
	// ...
}
```
RESEARCH.md's explicit recommendation (Pitfall 4): change `AVConverter{}` to carry `MaxSourceDuration time.Duration` / `MaxSourceResolutionHeight int` fields (zero-value defaulting to today's `4h`/`4320` so every existing test/dev-flow caller constructing `AVConverter{}` bare keeps working), then have `cmd/av-worker/main.go` construct a configured instance and **re-register** it into `convert.Default` at startup:
```go
convert.Default.Register(convert.AVConverter{
	MaxSourceDuration:         envDurationSeconds("AV_MAX_DURATION_SECONDS", 4*time.Hour),
	MaxSourceResolutionHeight: 4320,
})
```
`Registry.Register` (`converters.go` doc comment, line 10-16) already documents that a later `Register` call silently last-write-wins on pair collision — re-registering is a SAFE, already-relied-upon mechanic, not a new risk.

**Recommended: use pattern 2 (struct field + re-register)** per RESEARCH.md's explicit call-out that this avoids growing a second global-mutable-at-init pattern. Reference `av.go:231-246` for exactly which two consts move to fields, and `av.go:421-426` for the call sites that currently read the package consts and must instead read `c.MaxSourceDuration`/`c.MaxSourceResolutionHeight` (receiver `c AVConverter` is already in scope in `Convert`).

---

### `internal/convert/converters.go` (MODIFIED — no structural change, just registration site)

**Analog:** itself (`converters.go:1-22`, full file read) — if the struct-field approach above is chosen, `init()`'s existing `Default.Register(AVConverter{})` line (line 21) stays as the zero-value-defaulted fallback registration; `cmd/av-worker/main.go` re-registers a configured instance at startup, using the SAME `Register` call already documented at `converters.go:10-20` as safe to call twice ("silent last-write-wins on pair collision").

---

### `cmd/av-worker/main.go` (MODIFIED)

**Analog:** `cmd/audio-worker/main.go` (full file read, 255 lines) — nearly a line-for-line template; `cmd/av-worker/main.go` (current state, 160 lines, also fully read) is already Phase-35-scaffolded and missing exactly the pieces audio has.

**Threads: `resolveAudioThreads` → `resolveAVThreads` (clone verbatim)** (`cmd/audio-worker/main.go:161-178`):
```go
func resolveAudioThreads() (int, string) {
	if n := envInt("AUDIO_THREADS", 0); n > 0 {
		return n, "env override"
	}
	if n, ok := convert.CgroupCPULimit(); ok {
		return n, "cgroup"
	}
	return runtime.NumCPU(), "NumCPU fallback"
}
```
Call site (`cmd/audio-worker/main.go:96-98`):
```go
threads, threadSource := resolveAudioThreads()
convert.SetAudioThreads(threads)
log.Printf("🧵 audio threads=%d (source=%s)", threads, threadSource)
```
Note `av.go` ALREADY has its own internal `avThreadCount()` (`av.go:351-356`) that reads `CgroupCPULimit()` directly with no env-override tier — decide whether to add an `AV_THREADS` override env (mirroring audio's escape hatch) or leave `avThreadCount()` as-is; either way this is a `main.go`-level wiring decision, not a new `internal/convert` function, since `avThreadCount` is already correct for the no-override case.

**`AV_MAX_DURATION_SECONDS` wiring** — clone `envDurationSeconds` VERBATIM from `cmd/audio-worker/main.go:198-228` (bare-integer-seconds tolerant, negative-rejecting, warn-and-fallback-on-unparseable — this function does NOT yet exist in `cmd/av-worker/main.go`, which only has `envInt`/`envDuration`/`firstField`):
```go
func envDurationSeconds(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f := firstField(v)
	if d, err := time.ParseDuration(f); err == nil && d >= 0 {
		return d
	}
	if sec, err := strconv.Atoi(f); err == nil && sec >= 0 {
		return time.Duration(sec) * time.Second
	}
	log.Printf("⚠️ %s=%q is neither a duration (\"4h\") nor bare integer seconds (\"14400\"); using default %v", key, f, def)
	return def
}
```
Wire it at the same point audio wires `AUDIO_MODEL_PATH`/threads — BEFORE `srv.Start(mux)` (happens-before boundary comment at `cmd/audio-worker/main.go:78-86` applies identically):
```go
convert.Default.Register(convert.AVConverter{
	MaxSourceDuration:         envDurationSeconds("AV_MAX_DURATION_SECONDS", 4*time.Hour),
	MaxSourceResolutionHeight: 4320,
})
```

**`AV_WORKER_CONCURRENCY`** — current `cmd/av-worker/main.go:78` already reads `envInt("AV_WORKER_CONCURRENCY", 2)`; no change needed beyond confirming the measured value from the RTF script feeds this (mirrors `cmd/audio-worker/main.go:104`, `envInt("AUDIO_WORKER_CONCURRENCY", 2)`).

**Disk-guard env** (new, no audio precedent — audio has no disk guard) — wire an `AV_DISK_SAFETY_FACTOR` float env similarly to how `AUDIO_MODEL_PATH` is env-only-in-main:
```go
// or thread a constant/env into EnforceMinFreeDisk's call site inside av.go,
// per the setter-vs-struct-field decision made for MaxSourceDuration above.
```

**ShutdownTimeout already correct in current `cmd/av-worker/main.go:87`** (`envDuration("AV_ENGINE_TIMEOUT", 600*time.Second) + 10*time.Second`) — matches D-08's `ShutdownTimeout = measured AV_ENGINE_TIMEOUT + 10s` requirement exactly; only the underlying `AV_ENGINE_TIMEOUT` VALUE changes (docker-compose.yml), not this code.

---

### `docker-compose.yml` (MODIFIED)

**Analog:** the existing `audio-worker` service block (`docker-compose.yml:357-421`, full block read) — clone its shape exactly for a new `av-worker` service.

**Full service-block template to mirror** (`docker-compose.yml:360-421`):
```yaml
av-worker:
  build:
    context: .
    dockerfile: Dockerfile.av-worker
  container_name: octoconv-av-worker
  restart: always
  # cmd/av-worker sets asynq ShutdownTimeout = AV_ENGINE_TIMEOUT + 10s --
  # stop_grace_period must exceed that, mirroring audio-worker's 762s
  # (752s ShutdownTimeout + 10s margin) pairing exactly.
  stop_grace_period: <AV_ENGINE_TIMEOUT + 20>s
  depends_on:
    postgres: { condition: service_healthy }
    redis: { condition: service_healthy }
    minio: { condition: service_healthy }
  environment:
    DATABASE_URL: postgres://octo:octo-pass@postgres:5432/octo_db
    REDIS_ADDR: redis:6379
    S3_ENDPOINT: minio:9000
    S3_ACCESS_KEY: minioadmin
    S3_SECRET_KEY: minioadmin
    S3_BUCKET: octoconv
    S3_USE_SSL: "false"
    AV_WORKER_CONCURRENCY: "<measured>"
    AV_ENGINE_TIMEOUT: "<measured>s"   # RTF-measured, replaces the 600s placeholder
    AV_MAX_RETRY: "2"                   # D-03 LOCKED
    AV_MAX_DURATION_SECONDS: "<chosen ceiling, possibly lowered via NO-GO lever>"
    # AV_THREADS: left unset if mirroring audio's cgroup-auto-detection pattern
    # every other engine's MAX_RETRY/ENGINE_TIMEOUT vars, unconditionally read
    # by queue.NewClient() even though this process only runs the av engine
    # (DEBT-05 precedent):
    IMAGE_MAX_RETRY: "4"
    ENGINE_TIMEOUT: "120s"
    DOCUMENT_MAX_RETRY: "3"
    DOCUMENT_ENGINE_TIMEOUT: "300s"
    HTML_MAX_RETRY: "3"
    HTML_ENGINE_TIMEOUT: "60s"
    AUDIO_MAX_RETRY: "3"
    AUDIO_ENGINE_TIMEOUT: "742s"
    METRICS_ADDR: "127.0.0.1:9090"
  deploy:
    resources:
      limits:
        cpus: "2.0"    # matches the RTF measurement container exactly
        memory: <measured>   # RESEARCH.md Open Question 4: audio=1g, chromium=2g; open pending measurement
```

**D-08/IN-02 env-parity requirement — CRITICAL, distinct from the service-block clone above:** `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` must be added to EVERY OTHER `queue.NewClient()`-constructing service too, not just `av-worker`. Mirror EXACTLY how `AUDIO_MAX_RETRY: "3"` / `AUDIO_ENGINE_TIMEOUT: "742s"` are already present, with the identical `IN-02` comment, in ALL of: `api` (`docker-compose.yml:110-111`), `worker` (`:156-157`), `webhook-worker-1` (`:209-210`), `webhook-worker-2` (`:252-253`), `document-worker` (`:298-299`), `chromium-worker` (`:343-344`). Add the parallel `AV_MAX_RETRY: "2"` / `AV_ENGINE_TIMEOUT: "<measured>s"` lines to each of those six blocks, using the exact same IN-02 comment template already there:
```yaml
      # IN-02: queue.NewClient() derives audioUniqueTTL from these unconditionally
      # in EVERY process -- must be identical across all 7 queue.NewClient()-
      # constructing services (RTF-measured, 32-03-SUMMARY.md).
      AUDIO_MAX_RETRY: "3"
      AUDIO_ENGINE_TIMEOUT: "742s"
      # IN-02 (Phase 36): same requirement, av engine.
      AV_MAX_RETRY: "2"
      AV_ENGINE_TIMEOUT: "<measured>s"
```
This is the compose-parity item explicitly deferred from Phase 35 (`internal/queue/client.go` already reads `AV_MAX_RETRY`/`AV_ENGINE_TIMEOUT` unconditionally in every process per D-08's context — confirmed live: `docker-compose.yml` currently has ZERO `AV_*` lines anywhere, this phase adds all of them).

**`RECONCILER_ACTIVE_STALE_AFTER` coupling** (`docker-compose.yml:194-199`, `webhook-worker-1`/`-2`) — currently `15m` (900s). D-04's NO-GO lever fires if the derived `AV_ENGINE_TIMEOUT` would breach this; do not raise this threshold to accommodate a high AV timeout — lower `AV_MAX_DURATION_SECONDS` instead (exact wording already in the existing comment block at `:195-198`).

---

### `.github/workflows/ci.yml` (MODIFIED)

**Analog:** the existing `audio-worker` bake entries (both jobs, full relevant sections read: `ci.yml:44-125`).

**docker-build job** (`ci.yml:58-76`) — add two lines matching the `audio-worker.cache-to`/`cache-from` pair (`ci.yml:75-76`):
```yaml
            av-worker.cache-to=type=gha,mode=max,scope=av-worker
            av-worker.cache-from=type=gha,scope=av-worker
```

**e2e job** (`ci.yml:99-110`) — add one `cache-from` line matching `audio-worker.cache-from` (`ci.yml:110`):
```yaml
            av-worker.cache-from=type=gha,scope=av-worker
```
No `platform: linux/amd64` pin needed (unlike `document-worker`'s veraPDF-forced pin) — RESEARCH.md Pattern 3 confirms ffmpeg's default runtime CPU dispatch needs no cross-arch platform constraint, mirroring `audio-worker`'s own "no platform pin" comment in `docker-compose.yml:357-359`.

No other `ci.yml` changes needed — `av-worker` is not currently referenced anywhere in the file (confirmed via grep), so this is a pure two-job, three-line addition, structurally identical to how `audio-worker` was added in Phase 32.

---

## Shared Patterns

### cgroup-derived CPU sizing
**Source:** `internal/convert/cgroup.go` (`CgroupCPULimit`, full file read, 65 lines) — reuse VERBATIM, no modification needed.
**Apply to:** `cmd/av-worker/main.go` (via a new `resolveAVThreads`, cloned from `resolveAudioThreads`) and `av.go`'s existing `avThreadCount()` (`av.go:351-356`), which already calls `CgroupCPULimit()` directly.
```go
func CgroupCPULimit() (int, bool) {
	b, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0, false
	}
	return parseCPUMax(string(b))
}
```

### Fail-closed guard-before-expensive-work
**Source:** `internal/convert/avduration.go` (`EnforceMaxDuration`/`EnforceMaxResolution` shape) + `internal/convert/av.go:414-426` (call-site ordering in `Convert`).
**Apply to:** `avdiskguard.go`'s `EnforceMinFreeDisk`, called at the same point (after duration/resolution guards, before format-dispatch).

### env-only-in-main + package-level setter/struct-field config
**Source:** `internal/convert/whisper.go:14-46` (setter style) and `internal/api/api.go:123-194` (struct-field style).
**Apply to:** `AVConverter.MaxSourceDuration`/`MaxSourceResolutionHeight` threading from `cmd/av-worker/main.go` — struct-field + re-register recommended (see `av.go` section above for full rationale).

### `envDurationSeconds` bare-integer-seconds tolerant parser
**Source:** `cmd/audio-worker/main.go:198-228` — copy verbatim into `cmd/av-worker/main.go` (does not currently exist there).
**Apply to:** `AV_MAX_DURATION_SECONDS` parsing.

### IN-02 env-parity across every `queue.NewClient()`-constructing service
**Source:** `docker-compose.yml`'s existing `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` lines, present identically in all 7 current queue-client services (`api`, `worker`, `webhook-worker-1`, `webhook-worker-2`, `document-worker`, `chromium-worker`, `audio-worker`).
**Apply to:** adding matching `AV_MAX_RETRY`/`AV_ENGINE_TIMEOUT` lines to all of those PLUS the new `av-worker` service itself (8 services total after this phase).

### RTF measurement script skeleton (OrbStack gate, cgroup thread derivation, p95 nearest-rank, peak-memory observation, measurement-integrity-only exit code)
**Source:** `scripts/audio-rtf-measure.sh` (full file, sections 0/3/6/7/8 copy near-verbatim; sections 4/5 adapted for lavfi synthesis + matrix sweep).
**Apply to:** `scripts/av-rtf-measure.sh`.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `golang.org/x/sys/unix.Statfs` usage inside `avdiskguard.go` | utility (syscall wrapper) | file-I/O (disk-space probe) | Confirmed via grep: no `Statfs`/`Bavail`/ephemeral-storage check exists anywhere in `internal/` today. The GUARD-FUNCTION SHAPE has a strong analog (`EnforceMaxResolution`); only the underlying syscall call itself is unprecedented in this codebase. Dependency already resolved indirectly in `go.sum` (`golang.org/x/sys v0.44.0`), so no new module-resolution risk — just no example code to copy the syscall usage from. RESEARCH.md's Code Examples section supplies a fully worked recommended implementation to use as the effective "analog" instead. |

## Metadata

**Analog search scope:** `Dockerfile.audio-worker`, `scripts/audio-rtf-measure.sh`, `internal/convert/{avduration,cgroup,av,converters,whisper}.go`, `cmd/{audio-worker,av-worker}/main.go`, `internal/api/api.go`, `docker-compose.yml`, `.github/workflows/ci.yml`, `internal/queue/client.go`, `.env.example`
**Files scanned:** 12 read in full (or targeted grep+read), 0 re-reads of already-loaded ranges
**Pattern extraction date:** 2026-07-22
