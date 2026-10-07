package backup

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The decisions, kept apart from the disk and the database so each one is a
// unit test: which set a --from names, what a restore will do, which sets
// retention removes, and whether the nightly run is due.

// Resolve finds the set a restore's --from names: "latest" (or nothing), a
// set's exact name, or a day (YYYY-MM-DD), which is that day's newest intact
// set. sets is newest first, as List returns it.
func Resolve(sets []Set, from string) (Set, error) {
	from = strings.TrimSpace(from)
	if from == "" || from == "latest" {
		for _, set := range sets {
			if set.Intact {
				return set, nil
			}
		}
		return Set{}, errors.New("backup: there is no intact backup set")
	}
	for _, set := range sets {
		if set.Name == from {
			if !set.Intact {
				return Set{}, fmt.Errorf("backup: set %s cannot be restored: %s", from, set.Problem)
			}
			return set, nil
		}
	}
	if _, err := time.Parse(dateLayout, from); err != nil {
		return Set{}, fmt.Errorf("backup: no set is named %q (give a set's name, a day as YYYY-MM-DD, or latest)", from)
	}
	for _, set := range sets {
		if set.Date() == from && set.Intact {
			return set, nil
		}
	}
	return Set{}, fmt.Errorf("backup: there is no intact set from %s", from)
}

// RestoreRequest is what a restore was asked to do, and what it found about
// the install it is restoring into.
type RestoreRequest struct {
	Set         Set
	HasIdentity bool
	Rehearse    bool
	// CurrentKeyID is KeyID of the running install's credential key.
	CurrentKeyID string
	// IgnoreKeyMismatch restores a set whose stored connections will not open
	// under the current key, accepting that each must be set up again.
	IgnoreKeyMismatch bool
	// Confirm is the set name typed back; a restore over the live
	// database refuses without it.
	Confirm string
	// HasStorage is whether the install keeps attachments somewhere this
	// process can write.
	HasStorage bool
}

// RestorePlan is the steps a restore will take, in order, for the operator to
// read before anything changes.
type RestorePlan struct {
	Steps []string
	// Warnings are true and worth reading, but do not stop the restore.
	Warnings []string
	// KeyMismatch is set when the set's connections were sealed with another
	// key.
	KeyMismatch bool
}

// PlanRestore checks a restore can go ahead and lists what it will do. It
// refuses rather than warns for anything that would leave the install worse
// than it found it.
func PlanRestore(req RestoreRequest) (RestorePlan, error) {
	set := req.Set
	var plan RestorePlan
	if !set.Intact {
		return plan, fmt.Errorf("backup: set %s cannot be restored: %s", set.Name, set.Problem)
	}
	if set.Encrypted && !req.HasIdentity {
		return plan, ErrIdentityRequired
	}
	if !req.Rehearse && req.Confirm != set.Name {
		return plan, fmt.Errorf("backup: restoring overwrites the live database; "+
			"pass --confirm %s to go ahead, or --rehearse to try it in a scratch database", set.Name)
	}

	switch {
	case set.CredentialKeyID == "":
		plan.Warnings = append(plan.Warnings,
			"this set does not record which key sealed its stored connections; "+
				"if they will not open after the restore, put the set's secrets back (docs/operations.md)")
	case req.CurrentKeyID != set.CredentialKeyID:
		plan.KeyMismatch = true
		if !req.Rehearse && !req.IgnoreKeyMismatch {
			return plan, errors.New("backup: this set's stored connections were sealed with a different " +
				"SECRET_KEY or CREDENTIAL_ENCRYPTION_KEY than this install's; put the set's secrets back first " +
				"(docs/operations.md, Restoring on a new host), or pass --ignore-key-mismatch to restore " +
				"anyway and set every connection up again")
		}
		plan.Warnings = append(plan.Warnings,
			"the set's stored connections were sealed with a different key and will not open under this install's")
	}
	if set.Encrypted {
		plan.Steps = append(plan.Steps, "decrypt the set with the identity given, in memory")
	}
	_, hasAttachments := set.Files[PartAttachments]
	if req.Rehearse {
		plan.Steps = append(plan.Steps,
			"restore the database into a scratch database, all or nothing",
			"count the rows in every table")
		if hasAttachments {
			plan.Steps = append(plan.Steps, "read the attachments archive end to end")
		}
		if _, ok := set.Files[PartSecrets]; ok {
			plan.Steps = append(plan.Steps, "list the secrets archive")
		}
		plan.Steps = append(plan.Steps, "drop the scratch database; the live one is not touched")
		return plan, nil
	}

	if hasAttachments && req.HasStorage {
		plan.Steps = append(plan.Steps, "unpack the attachments beside the current ones")
	}
	plan.Steps = append(plan.Steps,
		"take a backup of the current database, attachments and secrets",
		"restore the database into a new database, all or nothing",
		"swap the new database in under the live name")
	if hasAttachments && req.HasStorage {
		plan.Steps = append(plan.Steps, "set the current attachments aside and move the restored ones in")
	} else if hasAttachments {
		plan.Warnings = append(plan.Warnings, "STORAGE_PATH is not writable here, so the attachments are not restored")
	}
	plan.Steps = append(plan.Steps,
		"apply this binary's migrations to the restored database",
		"drop the replaced database and the set-aside attachments")
	plan.Warnings = append(plan.Warnings,
		"secrets are not restored in place: this install keeps its own")
	return plan, nil
}

// PlanRetention picks the sets to delete: everything taken more than keepDays
// before now, except the newest intact set, which is kept whatever its age so
// a long outage cannot leave no backup at all. sets is newest first.
func PlanRetention(sets []Set, now time.Time, keepDays int) []Set {
	cutoff := now.AddDate(0, 0, -keepDays)
	keptNewest := false
	var remove []Set
	for _, set := range sets {
		if set.Intact && !keptNewest {
			keptNewest = true
			continue
		}
		if set.CreatedAt.Before(cutoff) {
			remove = append(remove, set)
		}
	}
	return remove
}

// NightlyDue reports whether the nightly run should start: it is past today's
// time and no nightly run has started since. A server that was down at the
// time catches up when it comes back, once.
func NightlyDue(now time.Time, hour, minute int, lastStarted *time.Time) bool {
	slot := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if now.Before(slot) {
		return false
	}
	return lastStarted == nil || lastStarted.Before(slot)
}

// NextNightly is when the nightly run will next start.
func NextNightly(now time.Time, hour, minute int, lastStarted *time.Time) time.Time {
	if NightlyDue(now, hour, minute, lastStarted) {
		return now
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if now.Before(slot) {
		return slot
	}
	return slot.AddDate(0, 0, 1)
}
