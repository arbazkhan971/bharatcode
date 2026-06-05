package audit

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/permission"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Deterministic, monotonically increasing clock for stable hashes.
	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	var n int64
	s.now = func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Second)
	}
	return s
}

func TestAppendAndRecords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1, err := s.Append(ctx, Event{Type: TypeTool, Actor: "bash", Summary: "ran ls"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if r1.Seq != 1 {
		t.Fatalf("first seq = %d, want 1", r1.Seq)
	}
	if r1.PrevHash != genesisHash {
		t.Fatalf("first prev_hash = %q, want genesis", r1.PrevHash)
	}

	r2, err := s.Append(ctx, Event{Type: TypeLLM, Actor: "anthropic", Summary: "completion", Detail: map[string]any{"tokens": 42}})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if r2.PrevHash != r1.Hash {
		t.Fatalf("second record does not chain onto first: prev=%q want=%q", r2.PrevHash, r1.Hash)
	}

	recs, err := s.Records(ctx)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if string(recs[1].Detail) != `{"tokens":42}` {
		t.Fatalf("detail = %s, want {\"tokens\":42}", recs[1].Detail)
	}
}

func TestVerifyCleanChain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.Append(ctx, Event{Type: TypeFileWrite, Actor: "edit", Summary: "wrote file"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	n, err := s.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n != 5 {
		t.Fatalf("verified %d records, want 5", n)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.Append(ctx, Event{Type: TypePermission, Actor: "sess", Summary: "allow bash"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Tamper directly on the underlying handle, bypassing the append-only API,
	// to simulate an attacker editing the SQLite file. UPDATE is blocked by a
	// trigger, so re-insert after a raw delete that also bypasses the trigger is
	// not possible either — instead corrupt by inserting a fabricated row is not
	// it; we forcibly drop the trigger then mutate a summary.
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER events_no_update`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE events SET summary = 'deny bash' WHERE seq = 2`); err != nil {
		t.Fatalf("tamper update: %v", err)
	}

	idx, err := s.Verify(ctx)
	if err == nil {
		t.Fatalf("Verify accepted a tampered chain")
	}
	if idx != 1 {
		t.Fatalf("Verify reported failure at index %d, want 1", idx)
	}
}

func TestAppendOnlyTriggers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, Event{Type: TypeTool, Summary: "x"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE events SET summary = 'y' WHERE seq = 1`); err == nil {
		t.Fatalf("UPDATE was allowed; append-only trigger missing")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE seq = 1`); err == nil {
		t.Fatalf("DELETE was allowed; append-only trigger missing")
	}
}

func TestExportJSONL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, Event{Type: TypeTool, Actor: "bash", Summary: "a"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.Append(ctx, Event{Type: TypeLLM, Actor: "x", Summary: "b"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var sb strings.Builder
	if err := s.Export(ctx, &sb); err != nil {
		t.Fatalf("Export: %v", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(sb.String()))
	var lines int
	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("export line not valid JSON: %v", err)
		}
		lines++
	}
	if lines != 2 {
		t.Fatalf("exported %d lines, want 2", lines)
	}
}

func TestHeadPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.db")

	s1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r1, err := s1.Append(ctx, Event{Type: TypeTool, Summary: "first"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	r2, err := s2.Append(ctx, Event{Type: TypeTool, Summary: "second"})
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if r2.PrevHash != r1.Hash {
		t.Fatalf("chain not continued across reopen: prev=%q want=%q", r2.PrevHash, r1.Hash)
	}
	if n, err := s2.Verify(ctx); err != nil || n != 2 {
		t.Fatalf("Verify after reopen: n=%d err=%v", n, err)
	}
}

func TestPermissionLogger(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	logger := s.PermissionLogger()
	logger.Log(ctx, permission.AuditRecord{
		Timestamp:   time.Now(),
		Tool:        "bash",
		SessionID:   "sess-1",
		ArgsSummary: "ls -la",
		Decision:    permission.DecisionAllow,
		Scope:       permission.ScopeOnce,
	})

	recs, err := s.Records(ctx)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Type != TypePermission {
		t.Fatalf("type = %q, want %q", recs[0].Type, TypePermission)
	}
	if !strings.Contains(recs[0].Summary, "bash") {
		t.Fatalf("summary %q missing tool name", recs[0].Summary)
	}
}

// sanity check that an unopened store fails loudly rather than panicking.
func TestAppendClosedStore(t *testing.T) {
	var s *Store
	if _, err := s.Append(context.Background(), Event{}); err == nil {
		t.Fatalf("Append on nil store should error")
	}
	s2 := &Store{db: (*sql.DB)(nil)}
	if _, err := s2.Append(context.Background(), Event{}); err == nil {
		t.Fatalf("Append on store with nil db should error")
	}
}
