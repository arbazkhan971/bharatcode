package tui

import (
	"context"
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/session"
)

// sessionForker is the optional Fork capability a session repository may
// expose. It is referenced only through a runtime type assertion so the TUI
// compiles whether or not the underlying *session.Repo defines Fork yet (a
// sibling change adds it in parallel). When the assertion fails the TUI falls
// back to forkSessionFallback, which branches a session using existing Repo
// primitives.
type sessionForker interface {
	Fork(ctx context.Context, sourceID string, title string) (*session.Session, error)
}

// forkSession branches sourceID into a new session and returns it. It prefers
// the repository's native Fork when available and otherwise reconstructs the
// fork from Get, Create, Messages, and AppendMessage. The returned session is a
// fresh row with its own ID whose transcript is a copy of the source's.
func forkSession(ctx context.Context, repo *session.Repo, sourceID string) (*session.Session, error) {
	if repo == nil {
		return nil, fmt.Errorf("forking session: repository is nil")
	}
	source, err := repo.Get(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("forking session: %w", err)
	}
	title := forkTitle(source.Title)

	// Prefer the native Fork once the sibling change lands. The assertion is
	// the only compile-safe way to reference a method that may not yet exist.
	var boxed interface{} = repo
	if f, ok := boxed.(sessionForker); ok {
		forked, err := f.Fork(ctx, sourceID, title)
		if err != nil {
			return nil, fmt.Errorf("forking session: %w", err)
		}
		return forked, nil
	}

	return forkSessionFallback(ctx, repo, source, title)
}

// forkSessionFallback branches source using only existing Repo primitives: it
// creates a new session copying the model and agent, then replays every source
// message into it. Message IDs are cleared so AppendMessage assigns fresh ones,
// avoiding a primary-key collision on the messages table.
func forkSessionFallback(ctx context.Context, repo *session.Repo, source *session.Session, title string) (*session.Session, error) {
	forked := &session.Session{
		ProjectPath: source.ProjectPath,
		Title:       title,
		Model:       source.Model,
		Agent:       source.Agent,
	}
	if err := repo.Create(ctx, forked); err != nil {
		return nil, fmt.Errorf("forking session: %w", err)
	}

	msgs, err := repo.Messages(ctx, source.ID)
	if err != nil {
		return nil, fmt.Errorf("forking session: %w", err)
	}
	for _, msg := range msgs {
		msg.ID = ""
		if err := repo.AppendMessage(ctx, forked.ID, msg); err != nil {
			return nil, fmt.Errorf("forking session: copying messages: %w", err)
		}
	}
	return forked, nil
}

// forkTitle derives a fork title from the source title, appending a marker so
// the branch is distinguishable in the session picker.
func forkTitle(source string) string {
	if source == "" {
		source = "New session"
	}
	return source + " (fork)"
}
