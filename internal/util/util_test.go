package util

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExpandPath(t *testing.T) {
	// Setup custom home and env vars.
	t.Setenv("HOME", "/h/u")
	t.Setenv("USERPROFILE", "/h/u") // For Windows tests.
	t.Setenv("USER", "alice")
	t.Setenv("FOO", "bar")

	// Empty input.
	require.Equal(t, "", ExpandPath(""))

	// Basic tilde expansion.
	require.Equal(t, filepath.Clean("/h/u/foo"), ExpandPath("~/foo"))

	// Tilde alone.
	require.Equal(t, filepath.Clean("/h/u"), ExpandPath("~"))

	// Env var substitution.
	require.Equal(t, filepath.Clean("/h/u/alice_x"), ExpandPath("$HOME/${USER}_x"))

	// Non-existent env var.
	require.Equal(t, filepath.Clean("xyz"), ExpandPath("xyz$NONEXISTENT"))

	// Absolute path untouched.
	require.Equal(t, filepath.Clean("/abc/def"), ExpandPath("/abc/def"))
}

func TestShortPath(t *testing.T) {
	t.Setenv("HOME", "/h/u")
	t.Setenv("USERPROFILE", "/h/u")

	// Empty input.
	require.Equal(t, "", ShortPath(""))

	// Under home.
	require.Equal(t, "~"+string(filepath.Separator)+"foo", ShortPath(filepath.Clean("/h/u/foo")))

	// Exactly home.
	require.Equal(t, "~", ShortPath(filepath.Clean("/h/u")))

	// Outside home.
	require.Equal(t, filepath.Clean("/other/path"), ShortPath(filepath.Clean("/other/path")))

	// prefix matches but not subdirectory.
	require.Equal(t, filepath.Clean("/h/u-prefix/foo"), ShortPath(filepath.Clean("/h/u-prefix/foo")))

	// Test when home cannot be determined.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	require.Equal(t, filepath.Clean("/h/u/foo"), ShortPath(filepath.Clean("/h/u/foo")))
}

func TestShortPathWindowsMock(t *testing.T) {
	// Mock Windows environment
	oldIsWindows := isWindows
	isWindows = true
	defer func() { isWindows = oldIsWindows }()

	t.Setenv("HOME", "/h/u")
	t.Setenv("USERPROFILE", "/h/u")

	// Test exact match (case insensitive)
	require.Equal(t, "~", ShortPath(filepath.Clean("/H/U")))

	// Test prefix match (case insensitive)
	require.Equal(t, "~"+string(filepath.Separator)+"foo", ShortPath(filepath.Clean("/H/U/foo")))
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{-1024, "-1.0 KB"},
		{-2048, "-2.0 KB"},
		{5242880, "5.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{1024 * 1024 * 1024 * 1024 * 1024, "1.0 PB"},
		{math.MinInt64, "-8192.0 PB"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			require.Equal(t, tc.expected, HumanBytes(tc.input))
		})
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{0, "0µs"},
		{500 * time.Microsecond, "500µs"},
		{750 * time.Millisecond, "750ms"},
		{12 * time.Second, "12s"},
		{2*time.Minute + 34*time.Second, "2m 34s"},
		{1*time.Hour + 2*time.Minute + 5*time.Second, "1h 2m 5s"},
		{-12 * time.Second, "12s"},
		{1*time.Hour + 5*time.Second, "1h 5s"},
		{time.Second + 500*time.Millisecond, "1s 500ms"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			require.Equal(t, tc.expected, HumanDuration(tc.input))
		})
	}
}

func TestTruncate(t *testing.T) {
	t.Logf("Truncate(héllo, 4) = %q", Truncate("héllo", 4))

	require.Equal(t, "", Truncate("hello", 0))
	require.Equal(t, "", Truncate("hello", -5))
	require.Equal(t, "hello", Truncate("hello", 5))
	require.Equal(t, "hello", Truncate("hello", 10))
	require.Equal(t, "hel…", Truncate("hello", 4))
}

func TestIndent(t *testing.T) {
	require.Equal(t, "  a\n  b\n", Indent("a\nb\n", "  "))
	require.Equal(t, "  a\n  b", Indent("a\nb", "  "))
	require.Equal(t, "", Indent("", "  "))
	require.Equal(t, "  \n", Indent("\n", "  "))
	require.Equal(t, "  a\n  \n  b\n", Indent("a\n\nb\n", "  "))
}

func BenchmarkHumanBytes(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = HumanBytes(5242880)
	}
}

func BenchmarkHumanDuration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = HumanDuration(2*time.Minute + 34*time.Second)
	}
}

func BenchmarkTruncate(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Truncate("hello", 4)
	}
}

func BenchmarkIndent(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Indent("a\nb\n", "  ")
	}
}
