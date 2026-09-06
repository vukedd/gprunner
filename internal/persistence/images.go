package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) GetContentKeyByBuildKey(ctx context.Context, buildKey string) (contentID string, ok bool, err error) {
	const q = `SELECT content_key FROM build_keys WHERE build_key = ?`

	err = s.db.QueryRowContext(ctx, q, buildKey).Scan(&contentID)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return "", false, nil
		default:
			return "", false, fmt.Errorf("looking up build key %s: %w", buildKey, err)
		}
	}
	return contentID, true, nil
}

func (s *Store) SaveImageMetadata(ctx context.Context, spec, buildKey, contentKey string, size int64) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	now := time.Now().Unix()

	// distinct build keys may dedupe onto one content key, so a repeat publish
	// only refreshes last_used_at
	const imgQ = `
		INSERT INTO images (content_key, size_bytes, created_at, last_used_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(content_key) DO UPDATE SET last_used_at = excluded.last_used_at`

	if _, err = tx.ExecContext(ctx, imgQ, contentKey, size, now, now); err != nil {
		return fmt.Errorf("saving image %s: %w", contentKey, err)
	}

	// a build key is deterministic, so the update is normally a no-op; if the
	// two ever disagree, the row written here points at the image just
	// published while the old one may already have been evicted
	const bkQ = `
		INSERT INTO build_keys (build_key, content_key, spec, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(build_key) DO UPDATE SET content_key = excluded.content_key`

	if _, err = tx.ExecContext(ctx, bkQ, buildKey, contentKey, spec, now); err != nil {
		return fmt.Errorf("saving build key %s: %w", buildKey, err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing image %s: %w", contentKey, err)
	}

	return nil
}

func (s *Store) TouchImage(ctx context.Context, contentKey string) (err error) {
	const q = `UPDATE images SET last_used_at = ? WHERE content_key = ?`
	now := time.Now().Unix()

	if _, err = s.db.ExecContext(ctx, q, now, contentKey); err != nil {
		return fmt.Errorf("touching image %s: %w", contentKey, err)
	}

	return nil
}
