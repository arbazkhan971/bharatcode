package filetracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RevertAction names what RevertSession did (or, under DryRun, would do)
// for a single file path.
type RevertAction string

const (
	// RevertRestored means the file's content was rewritten to the state
	// it had before the session first touched it.
	RevertRestored RevertAction = "restored"
	// RevertDeleted means the file was removed because the session had
	// created it (it did not exist beforehand).
	RevertDeleted RevertAction = "deleted"
	// RevertSkipped means the file was left untouched; see Outcome.Reason.
	RevertSkipped RevertAction = "skipped"
)

// RevertOutcome is the per-path result of a RevertSession call.
type RevertOutcome struct {
	Path   string       `json:"path"`
	Action RevertAction `json:"action"`
	Reason string       `json:"reason,omitempty"` // populated for RevertSkipped
}

// RevertOptions tunes RevertSession.
type RevertOptions struct {
	// Force reverts a file even when its current on-disk content differs
	// from what the session last wrote (i.e. it was modified out of band
	// since). Without Force such files are skipped so later edits are not
	// clobbered.
	Force bool
	// DryRun computes and returns the outcomes without touching any file.
	DryRun bool
}

// RevertSession restores every file that sessionID mutated back to the
// state it had before the session's first change to that file. A file
// that the session created (so it had no prior content) is deleted.
// Outcomes are returned in path order.
//
// Safety: a file whose current on-disk content no longer matches what
// the session last wrote was changed out of band and is skipped unless
// opts.Force is set. A file whose original content was never snapshotted
// (snapshots disabled when it was written) is also skipped. The revert
// itself is intentionally not recorded as a new Change — it is a
// recovery operation that walks the session's edits back, not a fresh
// agent edit.
func (t *Tracker) RevertSession(ctx context.Context, sessionID string, opts RevertOptions) ([]RevertOutcome, error) {
	changes, err := t.ChangesForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// ChangesForSession is oldest-first, so the first change seen for a
	// path holds its pre-session state and the last holds the state the
	// session left on disk.
	first := make(map[string]Change)
	last := make(map[string]Change)
	var paths []string
	for _, c := range changes {
		if _, ok := first[c.Path]; !ok {
			first[c.Path] = c
			paths = append(paths, c.Path)
		}
		last[c.Path] = c
	}
	sort.Strings(paths)

	outcomes := make([]RevertOutcome, 0, len(paths))
	for _, path := range paths {
		outcome, err := t.revertPath(path, first[path], last[path], opts)
		if err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// revertPath reverts a single file given its first and last recorded
// changes within the session. Only IO errors that should abort the
// whole revert are returned; recoverable conditions become a skipped
// outcome.
func (t *Tracker) revertPath(path string, first, last Change, opts RevertOptions) (RevertOutcome, error) {
	curHash, exists, err := hashFile(path)
	if err != nil {
		return RevertOutcome{}, fmt.Errorf("hashing %s for revert: %w", path, err)
	}

	// Safety: refuse to clobber a file the session no longer fully owns.
	if !opts.Force {
		var matches bool
		if last.Op == OpDelete {
			matches = !exists // the session left it deleted
		} else {
			matches = exists && curHash == last.AfterHash
		}
		if !matches {
			return RevertOutcome{
				Path:   path,
				Action: RevertSkipped,
				Reason: "modified since the session wrote it (use --force to override)",
			}, nil
		}
	}

	// The session created this path if its first change had no prior
	// content; reverting therefore means deleting it.
	if first.Op == OpCreate {
		if !opts.DryRun && exists {
			if err := os.Remove(path); err != nil {
				return RevertOutcome{}, fmt.Errorf("removing %s during revert: %w", path, err)
			}
		}
		return RevertOutcome{Path: path, Action: RevertDeleted}, nil
	}

	// Otherwise restore the original content from the snapshot store.
	content, ok, err := t.getBlob(first.BeforeHash)
	if err != nil {
		return RevertOutcome{}, err
	}
	if !ok {
		return RevertOutcome{
			Path:   path,
			Action: RevertSkipped,
			Reason: "no snapshot of the original content is available",
		}, nil
	}
	if !opts.DryRun {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return RevertOutcome{}, fmt.Errorf("creating parent dir for %s during revert: %w", path, err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return RevertOutcome{}, fmt.Errorf("restoring %s during revert: %w", path, err)
		}
	}
	return RevertOutcome{Path: path, Action: RevertRestored}, nil
}
