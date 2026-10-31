package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// TagLinks is a join table listing, in order, the tags a row names.
type TagLinks struct{ Table, Owner string }

var (
	RuleTags   = TagLinks{"rule_tags", "rule_id"}
	SeriesTags = TagLinks{"series_tags", "series_id"}
)

// Execer is what a tag write needs: a store handle or the importer's
// transaction.
type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// IDsSQL reads the links back as a JSON array, for a statement over the owning
// table named, not aliased, as owner.
func (l TagLinks) IDsSQL(owner string) string {
	return `(SELECT json_group_array(l.tag_id ORDER BY l."position") FROM ` + l.Table + ` l WHERE l.` + l.Owner + ` = ` + owner + `.id)`
}

// Replace swaps the tags owner names. A repeated id keeps its first place.
func (l TagLinks) Replace(ctx context.Context, db Execer, owner uuid.UUID, tags []uuid.UUID) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM `+l.Table+` WHERE `+l.Owner+` = ?`, owner); err != nil {
		return wrap("store: replace "+l.Table, err)
	}
	for position, tag := range tags {
		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO `+l.Table+` (`+l.Owner+`, tag_id, "position") VALUES (?, ?, ?)`,
			owner, tag, position); err != nil {
			return wrap("store: replace "+l.Table, err)
		}
	}
	return nil
}
