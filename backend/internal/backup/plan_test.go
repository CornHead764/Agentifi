package backup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func day(text string) time.Time {
	at, err := time.Parse("2006-01-02 15:04", text)
	if err != nil {
		panic(err)
	}
	return at
}

func set(name string, at string, intact bool) Set {
	one := Set{Intact: intact}
	one.Name = name
	one.CreatedAt = day(at)
	return one
}

func TestResolveFindsTheSetAFromNames(t *testing.T) {
	sets := []Set{
		set("2026-03-04_033000_nightly", "2026-03-04 03:30", false),
		set("2026-03-03_120000_manual", "2026-03-03 12:00", true),
		set("2026-03-03_033000_nightly", "2026-03-03 03:30", true),
	}

	latest, err := Resolve(sets, "latest")
	require.NoError(t, err)
	require.Equal(t, "2026-03-03_120000_manual", latest.Name, "latest skips a damaged set")

	empty, err := Resolve(sets, "")
	require.NoError(t, err)
	require.Equal(t, latest.Name, empty.Name)

	byName, err := Resolve(sets, "2026-03-03_033000_nightly")
	require.NoError(t, err)
	require.Equal(t, "2026-03-03_033000_nightly", byName.Name)

	byDay, err := Resolve(sets, "2026-03-03")
	require.NoError(t, err)
	require.Equal(t, "2026-03-03_120000_manual", byDay.Name, "a day is its newest set")

	_, err = Resolve(sets, "2026-03-04_033000_nightly")
	require.ErrorContains(t, err, "cannot be restored")

	_, err = Resolve(sets, "2026-03-04")
	require.ErrorContains(t, err, "no intact set from 2026-03-04")

	_, err = Resolve(sets, "yesterday")
	require.ErrorContains(t, err, "no set is named")

	_, err = Resolve(nil, "latest")
	require.Error(t, err)
}

func encryptedSet(keyID string) Set {
	one := set("2026-03-03_033000_nightly", "2026-03-03 03:30", true)
	one.Encrypted = true
	one.CredentialKeyID = keyID
	one.Files = map[Part]File{PartDatabase: {}, PartAttachments: {}, PartSecrets: {}}
	return one
}

func TestARestoreOverTheLiveDatabaseNeedsTheNameTypedBack(t *testing.T) {
	req := RestoreRequest{Set: encryptedSet("k1"), HasIdentity: true, CurrentKeyID: "k1", HasStorage: true}
	_, err := PlanRestore(req)
	require.ErrorContains(t, err, "--confirm 2026-03-03_033000_nightly")

	req.Confirm = "2026-03-03"
	_, err = PlanRestore(req)
	require.Error(t, err, "a day is not the set's name")

	req.Confirm = req.Set.Name
	plan, err := PlanRestore(req)
	require.NoError(t, err)
	require.Contains(t, plan.Steps, "take a backup of the current database, attachments and secrets")
	require.Contains(t, plan.Steps, "set the current attachments aside and move the restored ones in")
	require.Contains(t, plan.Steps, "apply this binary's migrations to the restored database")
}

func TestARehearsalNeedsNoConfirmationAndTouchesNothingLive(t *testing.T) {
	plan, err := PlanRestore(RestoreRequest{Set: encryptedSet("k1"), HasIdentity: true, CurrentKeyID: "k1", Rehearse: true})
	require.NoError(t, err)
	require.Contains(t, plan.Steps, "drop the scratch database; the live one is not touched")
	require.NotContains(t, plan.Steps, "take a backup of the current database, attachments and secrets")
}

func TestAnEncryptedSetNeedsAnIdentity(t *testing.T) {
	_, err := PlanRestore(RestoreRequest{Set: encryptedSet("k1"), CurrentKeyID: "k1", Rehearse: true})
	require.ErrorIs(t, err, ErrIdentityRequired)
}

func TestADamagedSetIsRefused(t *testing.T) {
	broken := encryptedSet("k1")
	broken.Intact = false
	broken.Problem = "the database file is missing"
	_, err := PlanRestore(RestoreRequest{Set: broken, HasIdentity: true, Rehearse: true})
	require.ErrorContains(t, err, "the database file is missing")
}

func TestAnotherKeysConnectionsAreRefusedUnlessAccepted(t *testing.T) {
	req := RestoreRequest{
		Set: encryptedSet("old-key"), HasIdentity: true, CurrentKeyID: "new-key",
		Confirm: "2026-03-03_033000_nightly", HasStorage: true,
	}
	_, err := PlanRestore(req)
	require.ErrorContains(t, err, "--ignore-key-mismatch")

	req.IgnoreKeyMismatch = true
	plan, err := PlanRestore(req)
	require.NoError(t, err)
	require.True(t, plan.KeyMismatch)

	rehearsal, err := PlanRestore(RestoreRequest{Set: encryptedSet("old-key"), HasIdentity: true,
		CurrentKeyID: "new-key", Rehearse: true})
	require.NoError(t, err, "a rehearsal only warns")
	require.True(t, rehearsal.KeyMismatch)
	require.NotEmpty(t, rehearsal.Warnings)
}

func TestAttachmentsWithNowhereToGoAreSaidSo(t *testing.T) {
	plan, err := PlanRestore(RestoreRequest{Set: encryptedSet("k1"), HasIdentity: true, CurrentKeyID: "k1",
		Confirm: "2026-03-03_033000_nightly"})
	require.NoError(t, err)
	require.NotContains(t, plan.Steps, "unpack the attachments beside the current ones")
	require.Contains(t, plan.Warnings, "STORAGE_PATH is not writable here, so the attachments are not restored")
}

func names(sets []Set) []string {
	out := []string{}
	for _, one := range sets {
		out = append(out, one.Name)
	}
	return out
}

func TestRetentionRemovesWhatIsOlderThanTheWindow(t *testing.T) {
	now := day("2026-03-20 03:30")
	sets := []Set{
		set("a", "2026-03-20 03:30", true),
		set("b", "2026-03-07 03:30", true),
		set("c", "2026-03-06 03:29", true),
		set("d", "2026-03-01 03:30", false),
	}
	require.Equal(t, []string{"c", "d"}, names(PlanRetention(sets, now, 14)))
}

func TestRetentionAlwaysKeepsTheNewestIntactSet(t *testing.T) {
	now := day("2026-06-01 03:30")
	sets := []Set{
		set("broken", "2026-05-31 03:30", false),
		set("good", "2026-01-01 03:30", true),
		set("older", "2025-12-31 03:30", true),
	}
	require.Equal(t, []string{"broken", "older"}, names(PlanRetention(sets, day("2026-06-30 03:30"), 14)))
	require.Equal(t, []string{"older"}, names(PlanRetention(sets, now, 14)))
}

func TestTheNightlyRunIsDueOncePastItsTime(t *testing.T) {
	before := day("2026-03-03 03:29")
	at := day("2026-03-03 03:30")
	after := day("2026-03-03 09:00")
	yesterday := day("2026-03-02 03:30")
	thisMorning := day("2026-03-03 03:31")

	require.False(t, NightlyDue(before, 3, 30, &yesterday))
	require.True(t, NightlyDue(at, 3, 30, &yesterday))
	require.True(t, NightlyDue(after, 3, 30, &yesterday), "a server down at the time catches up")
	require.False(t, NightlyDue(after, 3, 30, &thisMorning), "once a day")
	require.True(t, NightlyDue(after, 3, 30, nil), "a first start runs one")
	require.False(t, NightlyDue(before, 3, 30, nil))
}

func TestTheNextNightlyRun(t *testing.T) {
	thisMorning := day("2026-03-03 03:31")
	require.Equal(t, day("2026-03-04 03:30"), NextNightly(day("2026-03-03 09:00"), 3, 30, &thisMorning))
	require.Equal(t, day("2026-03-03 03:30"), NextNightly(day("2026-03-03 01:00"), 3, 30, &thisMorning))
	now := day("2026-03-03 09:00")
	require.Equal(t, now, NextNightly(now, 3, 30, nil))
}
