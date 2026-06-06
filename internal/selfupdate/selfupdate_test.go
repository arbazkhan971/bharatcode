package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareReportsUpdateWhenCommitsDiffer(t *testing.T) {
	s := Compare("abc1234", "def5678abcdef")
	require.True(t, s.UpdateAvailable)
	require.Equal(t, "abc1234", s.Current)
	require.Equal(t, "def5678", s.Latest) // upstream full sha shortened
}

func TestCompareNoUpdateWhenShortFormMatches(t *testing.T) {
	// A short built-in commit must match a full upstream SHA with the same prefix.
	s := Compare("abc1234", "abc1234def567890")
	require.False(t, s.UpdateAvailable)
}

func TestCompareDoesNotNagOnUnknownCommit(t *testing.T) {
	for _, cur := range []string{"", "0000000"} {
		s := Compare(cur, "def5678")
		require.False(t, s.UpdateAvailable, "must not report update for placeholder commit %q", cur)
	}
}

func TestCompareNoUpdateWhenUpstreamUnknown(t *testing.T) {
	s := Compare("abc1234", "")
	require.False(t, s.UpdateAvailable)
}

func TestCheckFetchesAndCompares(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		require.NotEmpty(t, r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte(`{"sha":"feedface00000000000000000000000000000000","commit":{"message":"x"}}`))
	}))
	defer srv.Close()

	s, err := Check(context.Background(), srv.URL, "abc1234")
	require.NoError(t, err)
	require.True(t, s.UpdateAvailable)
	require.Equal(t, "feedfac", s.Latest)
}

func TestCheckUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sha":"abc1234def"}`))
	}))
	defer srv.Close()

	s, err := Check(context.Background(), srv.URL, "abc1234")
	require.NoError(t, err)
	require.False(t, s.UpdateAvailable)
}

func TestCheckErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := Check(context.Background(), srv.URL, "abc1234")
	require.Error(t, err)
}

func TestCheckErrorsOnEmptySHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sha":""}`))
	}))
	defer srv.Close()

	_, err := Check(context.Background(), srv.URL, "abc1234")
	require.Error(t, err)
}

func TestAdviceMessage(t *testing.T) {
	require.Empty(t, Compare("abc1234", "abc1234").Advice())

	msg := Compare("abc1234", "def5678").Advice()
	require.Contains(t, msg, "abc1234")
	require.Contains(t, msg, "def5678")
	require.Contains(t, msg, "git pull")
}
