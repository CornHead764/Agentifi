package store

import (
	"context"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/google/uuid"
)

// connectorState is where a signed-in connector (a bill connection, a merchant
// account) keeps what every such connector keeps the same way: a sealed
// session and password, whether a person must sign in again, a pause on
// unattended sign-ins, and how the last scheduled run ended. The columns that
// are named differently per table are named here; the queries are built once.
type connectorState struct {
	table string
	what  string
	// session and credential are the sealing purposes of the two secrets.
	session, credential func(SpaceID, uuid.UUID) string
	enabled             string
	lastRun             string
	runStatus           string
	runError            string
	// runShot is the page the last run failed on, and runTrail what the
	// provider showed during the last run or unfinished sign-in, written and
	// cleared with runError so neither outlives the run it tells of. runTrail
	// is "" for a table that keeps no trail.
	runShot, runTrail string
	// wayIn is true of a row that has something a run can use.
	wayIn string
}

// A run's status, the same strings at every connector.
const (
	runOK          = "ok"
	runNeedsSignIn = "needs_sign_in"
	// runSignInFailed is a sign-in a person started that never landed.
	runSignInFailed = "sign_in_failed"
)

var billConnectorState = connectorState{
	table: "bill_connections", what: "bill connection",
	session: billConnectionSessionContext, credential: billConnectionCredentialContext,
	enabled: "pull_enabled", lastRun: "last_pulled_at", runStatus: "last_pull_status", runError: "last_pull_error",
	runShot: "last_pull_screenshot", runTrail: "last_pull_trail",
	// An API provider signing in with a password it is given each time, or one
	// read from the mailbox, needs neither secret kept.
	wayIn: `(session_state IS NOT NULL OR credential_sealed IS NOT NULL
	         OR credential_source NOT IN ('` + BillCredentialSession + `', '` + BillCredentialStored + `'))`,
}

var merchantConnectorState = connectorState{
	table: "merchant_accounts", what: "merchant account",
	session: merchantSessionContext, credential: merchantCredentialContext,
	enabled: "sync_enabled", lastRun: "last_synced_at", runStatus: "last_sync_status", runError: "last_sync_error",
	runShot: "last_sync_screenshot",
	wayIn:   `session_state IS NOT NULL`,
}

// forgetTrail is the assignment that clears the kept trail, "" for a table
// that keeps none.
func (c connectorState) forgetTrail(when string) string {
	if c.runTrail == "" {
		return ""
	}
	if when == "" {
		return c.runTrail + ` = NULL,`
	}
	return c.runTrail + ` = CASE WHEN ` + when + ` THEN NULL ELSE ` + c.runTrail + ` END,`
}

func connectorArgs(spaceID SpaceID, id uuid.UUID, more ...sqlitedb.NamedArgs) sqlitedb.NamedArgs {
	args := sqlitedb.NamedArgs{"space": spaceID.UUID(), "id": id}
	for _, set := range more {
		for name, value := range set {
			args[name] = value
		}
	}
	return args
}

// saveSession seals a session onto the row. fresh marks a sign-in a person just
// did: it clears the last run's stamp so the next tick runs, says no sign-in is
// needed, and lifts any pause. also is further assignments, with their named
// arguments in args.
func (s *Store) saveSession(
	ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, state string, fresh bool,
	also string, args sqlitedb.NamedArgs,
) error {
	cipher, err := s.requireCipher()
	if err != nil {
		return err
	}
	sealed, err := cipher.Seal(c.session(spaceID, id), state)
	if err != nil {
		return err
	}
	return s.execOne(ctx, "store: save "+c.what+" session",
		`UPDATE `+c.table+`
		    SET session_state = @sealed,
		        signed_in_at = CASE WHEN @fresh THEN now() ELSE signed_in_at END,
		        needs_sign_in = CASE WHEN @fresh THEN false ELSE needs_sign_in END,
		        sign_in_paused_at = CASE WHEN @fresh THEN NULL ELSE sign_in_paused_at END,
		        sign_in_paused_for = CASE WHEN @fresh THEN '' ELSE sign_in_paused_for END,
		        `+c.lastRun+` = CASE WHEN @fresh THEN NULL ELSE `+c.lastRun+` END,
		        `+c.runStatus+` = CASE WHEN @fresh THEN '' ELSE `+c.runStatus+` END,
		        `+c.runError+` = CASE WHEN @fresh THEN '' ELSE `+c.runError+` END,
		        `+c.runShot+` = CASE WHEN @fresh THEN NULL ELSE `+c.runShot+` END,
		        `+c.forgetTrail("@fresh")+`
		        `+also+`
		        updated_at = now()
		  WHERE space_id = @space AND id = @id`,
		connectorArgs(spaceID, id, sqlitedb.NamedArgs{"sealed": sealed, "fresh": fresh}, args))
}

// clearSession forgets the session. The row then asks a person to sign in
// again; a kept password stays, and so does the switch on scheduled runs.
func (s *Store) clearSession(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) error {
	return s.execOne(ctx, "store: clear "+c.what+" session",
		`UPDATE `+c.table+`
		    SET session_state = NULL, signed_in_at = NULL, needs_sign_in = true, updated_at = now()
		  WHERE space_id = @space AND id = @id`, connectorArgs(spaceID, id))
}

// saveCredential seals a password that worked and lifts any pause.
func (s *Store) saveCredential(
	ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, credential BillCredential,
	also string, args sqlitedb.NamedArgs,
) error {
	sealed, err := s.sealJSON("store: save "+c.what+" credential", c.credential(spaceID, id), credential)
	if err != nil {
		return err
	}
	return s.execOne(ctx, "store: save "+c.what+" credential",
		`UPDATE `+c.table+`
		    SET credential_sealed = @sealed, credential_has_totp = @totp,
		        sign_in_paused_at = NULL, sign_in_paused_for = '',
		        `+also+`
		        updated_at = now()
		  WHERE space_id = @space AND id = @id`,
		connectorArgs(spaceID, id, sqlitedb.NamedArgs{"sealed": sealed, "totp": credential.TOTPSecret != ""}, args))
}

func (s *Store) credentialOf(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) (BillCredential, error) {
	return openJSON[BillCredential](ctx, s, "store: "+c.what+" credential", c.table, "credential_sealed",
		c.credential(spaceID, id), spaceID, id)
}

// clearCredential forgets the password, its authenticator key and any pause.
func (s *Store) clearCredential(
	ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, also string, args sqlitedb.NamedArgs,
) error {
	return s.execOne(ctx, "store: clear "+c.what+" credential",
		`UPDATE `+c.table+`
		    SET credential_sealed = NULL, credential_has_totp = false,
		        sign_in_paused_at = NULL, sign_in_paused_for = '',
		        `+also+`
		        updated_at = now()
		  WHERE space_id = @space AND id = @id`, connectorArgs(spaceID, id, args))
}

// MaxFailureScreenshotBytes bounds a kept failure screenshot, as the column's
// check does; a larger one is not kept.
const MaxFailureScreenshotBytes = 1 << 20

// maxTrailBytes bounds a kept trail; a larger one is not kept. The engine's
// own cap on lines keeps a trail well under it.
const maxTrailBytes = 1 << 20

// keptTrail is a trail as it is stored: nil for none, or for one too large.
func keptTrail(trail []byte) []byte {
	if len(trail) == 0 || len(trail) > maxTrailBytes {
		return nil
	}
	return trail
}

// markRun records how a run ended, with the page it failed on or nil and what
// the provider showed during it (JSON) or nil. A run that met a sign-in says a
// person must sign in; one that got in lifts any pause.
func (s *Store) markRun(
	ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, status, detail string, shot, trail []byte,
) error {
	if len(shot) == 0 || len(shot) > MaxFailureScreenshotBytes {
		shot = nil
	}
	keepTrail := ""
	if c.runTrail != "" {
		keepTrail = c.runTrail + ` = @trail,`
	}
	_, err := s.db.Exec(ctx,
		`UPDATE `+c.table+`
		    SET `+c.lastRun+` = now(), `+c.runStatus+` = @status, `+c.runError+` = @detail,
		        `+c.runShot+` = @shot,
		        `+keepTrail+`
		        needs_sign_in = @needs,
		        sign_in_paused_at = CASE WHEN @ok THEN NULL ELSE sign_in_paused_at END,
		        sign_in_paused_for = CASE WHEN @ok THEN '' ELSE sign_in_paused_for END,
		        updated_at = now()
		  WHERE space_id = @space AND id = @id`,
		connectorArgs(spaceID, id, sqlitedb.NamedArgs{
			"status": status, "detail": detail, "shot": shot, "trail": keptTrail(trail),
			"needs": status == runNeedsSignIn, "ok": status == runOK,
		}))
	return wrap("store: mark "+c.what+" run", err)
}

// pauseSignIn keeps scheduled runs off the row until a person signs in,
// forgets the password, or runs it by hand. The password stays.
func (s *Store) pauseSignIn(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, why string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE `+c.table+` SET sign_in_paused_at = now(), sign_in_paused_for = @why, updated_at = now()
		  WHERE space_id = @space AND id = @id`, connectorArgs(spaceID, id, sqlitedb.NamedArgs{"why": why}))
	return wrap("store: pause "+c.what+" sign-in", err)
}

// noteRunSkipped records why a scheduled pass skipped the row, writing only
// when the text changed so "last error" does not move nightly. The last run's
// stamp is untouched: nothing ran, and no page was seen.
func (s *Store) noteRunSkipped(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, detail string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE `+c.table+` SET `+c.runError+` = @detail, `+c.runShot+` = NULL, `+c.forgetTrail("")+` updated_at = now()
		  WHERE space_id = @space AND id = @id AND `+c.runError+` <> @detail`,
		connectorArgs(spaceID, id, sqlitedb.NamedArgs{"detail": detail}))
	return wrap("store: note a skipped "+c.what+" run", err)
}

// markSignInEnded records a sign-in a person started that never landed, in
// the last run's place: its status, its sentence, the page it ended on or
// nil, and its trail. When the row last ran, whether it needs a sign-in and
// any pause are the last run's and stay as they are.
func (s *Store) markSignInEnded(
	ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID, detail string, shot, trail []byte,
) error {
	if len(shot) == 0 || len(shot) > MaxFailureScreenshotBytes {
		shot = nil
	}
	return s.execOne(ctx, "store: mark an unfinished "+c.what+" sign-in",
		`UPDATE `+c.table+`
		    SET `+c.runStatus+` = @status, `+c.runError+` = @detail, `+c.runShot+` = @shot,
		        `+c.runTrail+` = @trail, updated_at = now()
		  WHERE space_id = @space AND id = @id`,
		connectorArgs(spaceID, id, sqlitedb.NamedArgs{
			"status": runSignInFailed, "detail": detail, "shot": shot, "trail": keptTrail(trail),
		}))
}

// runTrailOf is the trail the row's last run or unfinished sign-in kept, as
// JSON; ErrNotFound when it kept none.
func (s *Store) runTrailOf(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	var trail []byte
	err := s.db.QueryRow(ctx,
		`SELECT `+c.runTrail+` FROM `+c.table+` WHERE space_id = @space AND id = @id`,
		connectorArgs(spaceID, id)).Scan(&trail)
	if err != nil {
		return nil, wrap("store: "+c.what+" trail", err)
	}
	if len(trail) == 0 {
		return nil, ErrNotFound
	}
	return trail, nil
}

// runScreenshot is the page the row's last run failed on, ErrNotFound when it
// kept none.
func (s *Store) runScreenshot(ctx context.Context, c connectorState, spaceID SpaceID, id uuid.UUID) ([]byte, error) {
	var shot []byte
	err := s.db.QueryRow(ctx,
		`SELECT `+c.runShot+` FROM `+c.table+` WHERE space_id = @space AND id = @id`,
		connectorArgs(spaceID, id)).Scan(&shot)
	if err != nil {
		return nil, wrap("store: "+c.what+" failure screenshot", err)
	}
	if len(shot) == 0 {
		return nil, ErrNotFound
	}
	return shot, nil
}

// listDue is every row, in every space, with scheduled runs on, something to
// run with, and no run since the given moment. One needing sign-in is still
// due when it holds a password; a paused one never is, or a refused password
// would be retried nightly until the site locked the account.
func listDue[T any](
	ctx context.Context, s *Store, c connectorState, columns string, scan func(scanner) (T, error), since time.Time,
) ([]T, error) {
	return queryAll(ctx, s.db, "store: "+c.what+"s due", scan,
		`SELECT `+columns+` FROM `+c.table+`
		  WHERE `+c.enabled+`
		    AND sign_in_paused_at IS NULL
		    AND (NOT needs_sign_in OR credential_sealed IS NOT NULL)
		    AND `+c.wayIn+`
		    AND (`+c.lastRun+` IS NULL OR `+c.lastRun+` < @since)
		  ORDER BY created_at`, sqlitedb.NamedArgs{"since": since})
}
