package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/app"
	"github.com/arbazkhan971/bharatcode/internal/db"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/session"
	"github.com/stretchr/testify/require"
)

// installImportApp wires newApp to an App backed by a real, empty session repo
// and returns an independent repo the test can query after the command runs,
// plus a restore func. The command's deferred closeApp closes the App's own DB
// handle; because db.Open returns refcounted handles sharing one underlying
// pool per path, the returned repo (a second handle on the same file) stays
// usable for post-run assertions. No network or external process is used.
func installImportApp(t *testing.T) (*session.Repo, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "import.db")

	// Handle owned by the command (closed by closeApp during the run).
	appDB, err := db.Open(context.Background(), dbPath)
	require.NoError(t, err)

	// Independent handle for assertions, kept open past the command's close.
	assertDB, err := db.Open(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = assertDB.Close() })

	fake := &app.App{DB: appDB, Sessions: session.NewRepo(appDB)}
	old := newApp
	newApp = func(ctx context.Context, opts app.Options) (*app.App, error) {
		_ = ctx
		_ = opts
		return fake, nil
	}
	return session.NewRepo(assertDB), func() { newApp = old }
}

// writeFixture writes body to a temp file and returns its path. The temp file
// is the intended seam for the file-reading path under test.
func writeFixture(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// onlySession returns the single session in the project, failing if there is
// not exactly one. Import must create exactly one new session per run.
func onlySession(t *testing.T, repo *session.Repo, projectPath string) session.Session {
	t.Helper()
	sessions, err := repo.List(context.Background(), session.ListFilter{ProjectPath: projectPath})
	require.NoError(t, err)
	require.Len(t, sessions, 1, "import should create exactly one session")
	return sessions[0]
}

// textOf concatenates the text blocks of a message for assertion convenience.
func textOf(t *testing.T, msg message.Message) string {
	t.Helper()
	return messageText(msg)
}

func TestImportHistoryMarkdownCreatesSessionWithTurns(t *testing.T) {
	repo, restore := installImportApp(t)
	defer restore()

	// A transcript in the exact shape session.ExportMarkdown produces, including
	// the header block, italic timestamp lines, and a fenced tool call.
	transcript := "# Refactor the parser\n\n" +
		"- Model: deepseek-chat\n- Agent: coder\n\n" +
		"## User\n\n*2026-06-02 10:30:00 UTC*\n\n" +
		"Please refactor the tokenizer.\n\n" +
		"## Assistant\n\n*2026-06-02 10:30:01 UTC*\n\n" +
		"Refactoring the tokenizer now.\n\n" +
		"**Tool call: edit_file**\n\n```json\n{\"path\":\"lex.go\"}\n```\n\n"

	path := writeFixture(t, "transcript.md", transcript)

	stdout, stderr, err := executeRoot(t, "--project-dir", "/tmp/imp", "import-history", path)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Imported 2 messages into session ")

	sess := onlySession(t, repo, "/tmp/imp")
	// Header metadata is carried onto the new session.
	require.Equal(t, "Refactor the parser", sess.Title)
	require.Equal(t, "deepseek-chat", sess.Model)
	require.Equal(t, "coder", sess.Agent)

	msgs, err := repo.Messages(context.Background(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	require.Equal(t, message.RoleUser, msgs[0].Role)
	require.Equal(t, "Please refactor the tokenizer.", textOf(t, msgs[0]))

	require.Equal(t, message.RoleAssistant, msgs[1].Role)
	// Assistant prose plus the fenced tool block are folded into the turn text;
	// the timestamp line is dropped.
	assistant := textOf(t, msgs[1])
	require.Contains(t, assistant, "Refactoring the tokenizer now.")
	require.Contains(t, assistant, "**Tool call: edit_file**")
	require.Contains(t, assistant, `{"path":"lex.go"}`)
	require.NotContains(t, assistant, "2026-06-02 10:30:01")
}

func TestImportHistoryPromptsCreatesUserMessages(t *testing.T) {
	repo, restore := installImportApp(t)
	defer restore()

	// One prompt per line; blank lines and "#" comments are skipped.
	prompts := "Summarize the repository\n\n# this is a comment\nNow write tests\n   \nAdd a README\n"
	path := writeFixture(t, "prompts.txt", prompts)

	stdout, stderr, err := executeRoot(t, "--project-dir", "/tmp/imp", "import-history", path, "--format", "prompts")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Imported 3 messages into session ")

	sess := onlySession(t, repo, "/tmp/imp")
	// With no explicit title, the repo auto-titles from the first user message.
	require.Equal(t, "Summarize the repository", sess.Title)

	msgs, err := repo.Messages(context.Background(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	for _, m := range msgs {
		require.Equal(t, message.RoleUser, m.Role)
	}
	require.Equal(t, "Summarize the repository", textOf(t, msgs[0]))
	require.Equal(t, "Now write tests", textOf(t, msgs[1]))
	require.Equal(t, "Add a README", textOf(t, msgs[2]))
}

func TestImportHistoryRejectsEmptyFile(t *testing.T) {
	_, restore := installImportApp(t)
	defer restore()

	path := writeFixture(t, "empty.md", "\n\n   \n")
	stdout, stderr, err := executeRoot(t, "import-history", path)
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "no messages to import")
}

func TestImportHistoryMissingFile(t *testing.T) {
	_, restore := installImportApp(t)
	defer restore()

	stdout, stderr, err := executeRoot(t, "import-history", filepath.Join(t.TempDir(), "nope.md"))
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "reading import file")
}

func TestImportHistoryInvalidFormat(t *testing.T) {
	_, restore := installImportApp(t)
	defer restore()

	path := writeFixture(t, "x.md", "## User\n\nhi\n")
	stdout, stderr, err := executeRoot(t, "import-history", path, "--format", "bogus")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, `invalid --format "bogus"`)
}

// TestImportHistoryRoundTripsExport pins the markdown importer to the real
// exporter: a session exported by session.ExportMarkdown re-imports into a new
// session with the same role-attributed text turns.
func TestImportHistoryRoundTripsExport(t *testing.T) {
	repo, restore := installImportApp(t)
	defer restore()

	src := &session.Session{Title: "Roundtrip demo", Model: "kimi-k2", Agent: "coder"}
	original := []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "First question?"}}},
		{Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: "First answer."}}},
	}
	md, err := session.ExportMarkdown(src, original)
	require.NoError(t, err)

	path := writeFixture(t, "roundtrip.md", md)
	_, stderr, err := executeRoot(t, "--project-dir", "/tmp/rt", "import-history", path)
	require.NoError(t, err)
	require.Empty(t, stderr)

	sess := onlySession(t, repo, "/tmp/rt")
	require.Equal(t, "Roundtrip demo", sess.Title)
	require.Equal(t, "kimi-k2", sess.Model)

	msgs, err := repo.Messages(context.Background(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, message.RoleUser, msgs[0].Role)
	require.Equal(t, "First question?", textOf(t, msgs[0]))
	require.Equal(t, message.RoleAssistant, msgs[1].Role)
	require.Equal(t, "First answer.", textOf(t, msgs[1]))
}
