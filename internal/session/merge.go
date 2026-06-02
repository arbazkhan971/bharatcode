package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/db/sqlc"
)

// SourceDisposition selects what Merge does with the source session after its
// messages have been appended onto the target.
type SourceDisposition int

const (
	// LeaveSource keeps the source session intact after the merge. It is the
	// zero value, so the zero MergeOptions leaves the source untouched.
	LeaveSource SourceDisposition = iota
	// ArchiveSource soft-hides the source after the merge via Archive: it stays
	// retrievable by id but drops out of the default List.
	ArchiveSource
	// DeleteSource hard-deletes the source and its messages after the merge via
	// Delete.
	DeleteSource
)

// MergeOptions configures a Merge call. The zero value appends the source's
// messages onto the target and leaves the source intact.
type MergeOptions struct {
	// Disposition selects what happens to the source session after its messages
	// are appended onto the target. The default (LeaveSource) leaves it intact.
	Disposition SourceDisposition
}

// Merge appends every message from fromID onto intoID, producing a combined
// transcript on intoID, and returns the updated target session. The source's
// messages are copied in their existing order and stamped after the target's
// last message so the merged transcript reads target-messages-then-source-
// messages when fetched with Messages (which orders by created_at then
// insertion order). Copied messages receive fresh IDs; ParentID references
// between copied messages are remapped to those fresh IDs, and any parent
// outside the copied set is dropped (left nil), so the merged session never
// references a message it does not contain. The target's own messages, title,
// model, and agent are left unchanged.
//
// After the append, the source is handled per opts.Disposition: left intact
// (default), archived, or deleted. Returns ErrNotFound if either session is
// absent, and an error if intoID == fromID (a session cannot be merged into
// itself).
func (r *Repo) Merge(ctx context.Context, intoID, fromID string, opts MergeOptions) (*Session, error) {
	if intoID == fromID {
		return nil, fmt.Errorf("merging session: cannot merge a session into itself")
	}

	into, err := r.Get(ctx, intoID)
	if err != nil {
		return nil, fmt.Errorf("merging session: target: %w", err)
	}
	if _, err := r.Get(ctx, fromID); err != nil {
		return nil, fmt.Errorf("merging session: source: %w", err)
	}

	intoMsgs, err := r.Messages(ctx, intoID)
	if err != nil {
		return nil, fmt.Errorf("merging session: reading target messages: %w", err)
	}
	fromMsgs, err := r.Messages(ctx, fromID)
	if err != nil {
		return nil, fmt.Errorf("merging session: reading source messages: %w", err)
	}

	// Re-stamp the copied messages so they sort after every existing target
	// message. Messages read back ordered by (created_at ASC, rowid ASC); since
	// created_at has only second granularity, a source message with an earlier
	// second would otherwise sort ahead of a later target message. Anchoring the
	// copies at >= the target's latest created_at, while preserving their
	// relative spacing, keeps the source block strictly after the target block.
	var baseStamp time.Time
	for _, m := range intoMsgs {
		if m.CreatedAt.After(baseStamp) {
			baseStamp = m.CreatedAt
		}
	}
	// When the target has no messages there is nothing to sort after, so anchor
	// on the source's own first timestamp; this preserves the copies' original
	// times rather than collapsing them to the zero time (negative Unix).
	if baseStamp.IsZero() && len(fromMsgs) > 0 {
		baseStamp = fromMsgs[0].CreatedAt
	}

	// idRemap maps each source message ID to its freshly generated copy ID so
	// that ParentID references between copied messages point within the merged
	// set rather than back at the original source rows.
	idRemap := make(map[string]string, len(fromMsgs))
	for _, m := range fromMsgs {
		newID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("merging session: %w", err)
		}
		idRemap[m.ID] = newID
	}

	for i, m := range fromMsgs {
		contentBytes, err := json.Marshal(m.Content)
		if err != nil {
			return nil, fmt.Errorf("merging session: marshalling message content: %w", err)
		}

		var parentID *string
		if m.ParentID != nil {
			if remapped, ok := idRemap[*m.ParentID]; ok {
				parentID = &remapped
			}
			// A parent outside the copied range is dropped (left nil) so the
			// merged session never references a message it does not contain.
		}

		// Shift each copy's timestamp to baseStamp + its original offset from the
		// source's first message, preserving the source's internal spacing while
		// guaranteeing the whole block sorts at/after the target's last message.
		// The (created_at, rowid) ordering then keeps the source after the target
		// even when the seconds collide, because the copies are inserted last.
		stamp := baseStamp
		if i > 0 {
			delta := m.CreatedAt.Sub(fromMsgs[0].CreatedAt)
			if delta < 0 {
				delta = 0
			}
			stamp = baseStamp.Add(delta)
		}

		msgParams := sqlc.CreateMessageParams{
			ID:          idRemap[m.ID],
			SessionID:   into.ID,
			Role:        string(m.Role),
			ContentJson: string(contentBytes),
			ParentID:    parentID,
			CreatedAt:   stamp.UTC().Unix(),
		}
		if _, err := r.database.Queries.CreateMessage(ctx, msgParams); err != nil {
			return nil, fmt.Errorf("merging session: copying message: %w", err)
		}
	}

	// Update the target's denormalized count and UpdatedAt. The count is derived
	// from the rows we actually loaded and inserted rather than the stored
	// MessageCount, which is not always trustworthy. We build the params directly
	// rather than calling Update, which ignores MessageCount and would reset it to
	// the stale stored value.
	into.MessageCount = len(intoMsgs) + len(fromMsgs)
	into.UpdatedAt = time.Now().UTC()
	updateParams := sqlc.UpdateSessionParams{
		ID:           into.ID,
		ProjectPath:  into.ProjectPath,
		Title:        into.Title,
		Model:        into.Model,
		Agent:        into.Agent,
		UpdatedAt:    into.UpdatedAt.Unix(),
		MessageCount: int64(into.MessageCount),
	}
	if _, err := r.database.Queries.UpdateSession(ctx, updateParams); err != nil {
		return nil, fmt.Errorf("merging session: updating target: %w", err)
	}

	switch opts.Disposition {
	case ArchiveSource:
		if err := r.Archive(ctx, fromID); err != nil {
			return nil, fmt.Errorf("merging session: archiving source: %w", err)
		}
	case DeleteSource:
		if err := r.Delete(ctx, fromID); err != nil {
			return nil, fmt.Errorf("merging session: deleting source: %w", err)
		}
	case LeaveSource:
		// Source left intact.
	}

	return into, nil
}
