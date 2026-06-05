package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/audit"
)

func TestAuditExportAndVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")

	// Seed the log with a couple of records.
	store, err := audit.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Append(context.Background(), audit.Event{Type: audit.TypeTool, Actor: "bash", Summary: "ran ls"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.Append(context.Background(), audit.Event{Type: audit.TypeLLM, Actor: "anthropic", Summary: "completion"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// export
	exportCmd := newAuditCmd()
	var out bytes.Buffer
	exportCmd.SetOut(&out)
	exportCmd.SetErr(&out)
	exportCmd.SetArgs([]string{"export", "--path", path})
	if err := exportCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if got := strings.Count(out.String(), "\n"); got != 2 {
		t.Fatalf("export wrote %d lines, want 2:\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "ran ls") {
		t.Fatalf("export missing seeded summary:\n%s", out.String())
	}

	// verify
	verifyCmd := newAuditCmd()
	var vout bytes.Buffer
	verifyCmd.SetOut(&vout)
	verifyCmd.SetErr(&vout)
	verifyCmd.SetArgs([]string{"verify", "--path", path})
	if err := verifyCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(vout.String(), "2 records verified") {
		t.Fatalf("verify output = %q, want 2 records verified", vout.String())
	}
}
