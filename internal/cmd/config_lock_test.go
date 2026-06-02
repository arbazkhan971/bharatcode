package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/stretchr/testify/require"
)

// TestAcquireConfigLockExcludesSecondHolder proves the lock is truly
// exclusive: while one holder owns it, a second acquisition fails once
// its short window elapses, and only succeeds after the first releases.
func TestAcquireConfigLockExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	release, err := acquireConfigLock(context.Background(), path)
	require.NoError(t, err)

	// The lockfile sits beside the config file while held.
	require.FileExists(t, path+".lock")

	// A second acquire with a short deadline must not succeed while the
	// first holder is active; it blocks then times out with an error.
	shortCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	release2, err := acquireConfigLock(shortCtx, path)
	require.Error(t, err)
	require.Nil(t, release2)
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond,
		"second acquire returned before its deadline, so it was not blocked")

	// After releasing, the lock becomes available again and the
	// lockfile is gone.
	require.NoError(t, release())
	require.NoFileExists(t, path+".lock")

	release3, err := acquireConfigLock(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, release3())
}

// TestConfigLockDoubleReleaseErrors proves a double release is reported
// rather than silently swallowed, so a logic bug that releases twice is
// caught.
func TestConfigLockDoubleReleaseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	release, err := acquireConfigLock(context.Background(), path)
	require.NoError(t, err)

	require.NoError(t, release())
	require.Error(t, release(), "second release must report the lock was already released")
}

// TestConfigLockPreventsLostUpdates is the core behavior test. Many
// goroutines race to perform a lock-guarded read-modify-write of the
// same config file: each reads the accumulated marker, sleeps to widen
// the interleave window, appends its own index, and writes back. The
// sleep guarantees a lost update if the writes ran unserialized. Because
// the lock serializes them, every writer's index must survive and the
// final file must still parse as valid config.
//
// This test discriminates: with locking removed it fails (markers are
// lost and the count comes up short), so a passing run is real evidence
// the lock serializes the read-modify-write.
func TestConfigLockPreventsLostUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(defaultTestConfig()), 0o600))

	const writers = 8
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func(n int) {
			defer wg.Done()
			release, err := acquireConfigLock(context.Background(), path)
			if err != nil {
				t.Errorf("writer %d failed to acquire lock: %v", n, err)
				return
			}
			defer func() { _ = release() }()

			// Read-modify-write: load the current config, append this
			// writer's marker to the accumulated list, and save it back.
			// The sleep between read and write would let an unserialized
			// sibling read the same stale value and clobber our append.
			cfg, err := config.LoadFrom(context.Background(), path, "")
			if err != nil {
				t.Errorf("writer %d failed to load config: %v", n, err)
				return
			}
			updated := appendMarker(cfg.Options.DataDir, n)
			time.Sleep(time.Millisecond)
			cfg.Options.DataDir = updated
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				t.Errorf("writer %d failed to marshal: %v", n, err)
				return
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				t.Errorf("writer %d failed to write: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	// The final file parses cleanly: no torn/interleaved JSON survived.
	final, err := config.LoadFrom(context.Background(), path, "")
	require.NoError(t, err, "final config must be valid after concurrent writes")

	// Every writer's marker survived: no update was lost to a race,
	// proving the read-modify-write was serialized.
	markers := strings.FieldsFunc(final.Options.DataDir, func(r rune) bool { return r == ',' })
	markers = nonEmpty(markers)
	require.Len(t, markers, writers,
		"every writer's marker must survive serialized updates; got %v", markers)
	seen := map[string]bool{}
	for _, m := range markers {
		require.False(t, seen[m], "duplicate marker %q indicates a corrupted update", m)
		seen[m] = true
	}
}

// appendMarker appends "wN" to a comma-separated accumulator.
func appendMarker(acc string, n int) string {
	marker := "w" + strconv.Itoa(n)
	if acc == "" {
		return marker
	}
	return acc + "," + marker
}

func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
