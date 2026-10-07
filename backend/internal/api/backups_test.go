package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The backups section of the administration screen. The sets themselves, and
// restoring them, are internal/backup's tests; these are the settings, what
// the screen is told, and who hears about a failed night.

// A public key from the age library's own pair, not a real one.
const testRecipient = "age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m"

func clearBackupSettings(t *testing.T, env *Env) {
	t.Helper()
	_, err := env.DB.Pool().Exec(t.Context(),
		`DELETE FROM server_settings WHERE key = ANY($1)`, store.BackupSettingKeys)
	require.NoError(t, err)
	_, err = env.DB.Pool().Exec(t.Context(), `DELETE FROM backup_runs`)
	require.NoError(t, err)
}

func TestBackupsAreOffWithoutADirectoryAndSaySo(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)

	body := c.get("/admin/backups").requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["enabled"])
	require.Equal(t, false, body["encrypted"])
	require.Empty(t, body["sets"])
	require.Nil(t, body["next_run"])

	c.post("/admin/backups/run", map[string]any{}).requireStatus(http.StatusConflict)
	c.post("/admin/backups/sets/any/rehearse", map[string]any{"identity": ""}).
		requireStatus(http.StatusConflict)
}

func TestTheBackupSettingsRoundTrip(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)

	before := c.get("/admin/backups").requireStatus(http.StatusOK).json()
	require.Equal(t, "environment", before["sources"].(map[string]any)["recipients"])

	saved := c.put("/admin/backups/settings", map[string]any{
		"at": "02:15", "keep_days": 30,
		"recipients": []map[string]any{
			{"recipient": testRecipient, "label": "Offsite key"},
			{"recipient": " " + testRecipient + " ", "label": "the same key twice"},
		},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "02:15", saved["at"])
	require.EqualValues(t, 30, saved["keep_days"])
	require.Equal(t, true, saved["encrypted"])
	recipients := saved["recipients"].([]any)
	require.Len(t, recipients, 1, "a key given twice is kept once")
	first := recipients[0].(map[string]any)
	require.Equal(t, testRecipient, first["recipient"])
	require.Equal(t, "Offsite key", first["label"])
	addedAt := first["added_at"]
	require.NotEmpty(t, addedAt)
	sources := saved["sources"].(map[string]any)
	require.Equal(t, "database", sources["at"])
	require.Equal(t, "database", sources["recipients"])

	again := c.put("/admin/backups/settings", map[string]any{
		"at": "02:15", "keep_days": 30,
		"recipients": []map[string]any{{"recipient": testRecipient, "label": "Renamed"}},
	}).requireStatus(http.StatusOK).json()
	kept := again["recipients"].([]any)[0].(map[string]any)
	require.Equal(t, addedAt, kept["added_at"], "a key saved again keeps the day it was added")

	cleared := c.put("/admin/backups/settings", map[string]any{
		"at": "02:15", "keep_days": 30, "recipients": []any{},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, cleared["encrypted"])
	require.Empty(t, cleared["recipients"])
}

// An invented ssh-keygen key, and the fingerprint `ssh-keygen -lf` printed for it.
const (
	testSSHKey         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHtfBk58AasAD2LR2xeQ/qM6+2BhtSPUDwJP25s93+Bw"
	testSSHFingerprint = "SHA256:zeupREKRB/xQzRoKxYO2h+MbTWn/XoCwc0PDgie88Zk"
)

func TestAnSSHKeyIsSavedAsARecipient(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)

	saved := c.put("/admin/backups/settings", map[string]any{
		"at": "03:30", "keep_days": 14,
		"recipients": []map[string]any{
			{"recipient": testRecipient, "label": "Offsite key"},
			{"recipient": testSSHKey + " backup-test@example.invalid", "label": ""},
			{"recipient": testSSHKey + " another-comment", "label": "the same key twice"},
		},
	}).requireStatus(http.StatusOK).json()
	recipients := saved["recipients"].([]any)
	require.Len(t, recipients, 2, "the comment does not make a key a different one")
	ageRow := recipients[0].(map[string]any)
	require.Equal(t, "age", ageRow["kind"])
	require.Equal(t, "", ageRow["fingerprint"])
	sshRow := recipients[1].(map[string]any)
	require.Equal(t, testSSHKey, sshRow["recipient"], "stored without its comment")
	require.Equal(t, "backup-test@example.invalid", sshRow["label"], "an unlabelled key is labelled with its comment")
	require.Equal(t, "ssh-ed25519", sshRow["kind"])
	require.Equal(t, testSSHFingerprint, sshRow["fingerprint"])
}

func TestBadBackupSettingsAreRefused(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)
	for _, body := range []map[string]any{
		{"at": "25:00", "keep_days": 14, "recipients": []any{}},
		{"at": "3:30pm", "keep_days": 14, "recipients": []any{}},
		{"at": "03:30", "keep_days": 0, "recipients": []any{}},
		{"at": "03:30", "keep_days": 14, "recipients": []map[string]any{{"recipient": "ssh-ed25519 AAAA"}}},
		{"at": "03:30", "keep_days": 14, "recipients": []map[string]any{
			{"recipient": "AGE-SECRET-KEY-15WVA2EEQZFV75G9LJENM2PVQS9M3WURETAGJXX4PEMUVLWW40V7SXLFC3W"}}},
	} {
		c.put("/admin/backups/settings", body).requireStatus(http.StatusUnprocessableEntity)
	}
}

func TestAFailedNightIsToldToTheAdministrators(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)
	admin := soloAdmin(t)
	space := makeSpace(t, admin, "Household", store.RoleOwner, true)
	bystander := makeUser(t, testPassword)
	accepted := time.Now().UTC()
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID,
		&store.Membership{UserID: bystander.ID, Role: store.RoleMember, InvitedAt: &accepted, AcceptedAt: &accepted}))

	backups := NewBackups(c.env.Cfg, c.env.DB)
	backups.Alerts = newAlerts(c.env)
	backups.Dir = filepath.Join(t.TempDir(), "not-mounted")

	_, err := backups.Take(t.Context(), backup.TriggerNightly)
	require.ErrorContains(t, err, "not-mounted")

	runs, err := c.env.DB.ListBackupRuns(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, store.BackupRunSkipped, runs[0].Status)

	feed, err := c.env.DB.ListNotifications(t.Context(), space.ID, admin.ID, store.NotificationQuery{})
	require.NoError(t, err)
	require.Len(t, feed, 1)
	require.Equal(t, domain.AlertBackupFailed, feed[0].AlertType)
	require.Contains(t, feed[0].Body, "not-mounted")

	other, err := c.env.DB.ListNotifications(t.Context(), space.ID, bystander.ID, store.NotificationQuery{})
	require.NoError(t, err)
	require.Empty(t, other, "only administrators hear about the server's backups")

	_, err = backups.Take(t.Context(), backup.TriggerNightly)
	require.Error(t, err)
	feed, err = c.env.DB.ListNotifications(t.Context(), space.ID, admin.ID, store.NotificationQuery{})
	require.NoError(t, err)
	require.Len(t, feed, 1, "a second failed night is the same standing condition")
}

// backupSource is a database of its own for the screen to back up: the
// shared test database holds every package's schema at once.
func backupSource(t *testing.T) backup.Postgres {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is unset; see CONTRIBUTING.md for the development Postgres")
	}
	// A missing or mismatched client skips on a workstation but is fatal in
	// CI, where a skipped round trip would read exactly like a passing one.
	fatal := t.Skip
	if os.Getenv("CI") != "" {
		fatal = t.Fatal
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := osexec.LookPath(tool); err != nil {
			fatal(fmt.Sprintf("needs %s on PATH at the server's major version", tool))
		}
	}
	server := backup.Postgres{URL: url}
	if tools := backup.CheckTools(t.Context(), server); tools.Problem != "" {
		fatal(tools.Problem)
	}
	name := fmt.Sprintf("agentifi_api_backup_%d", os.Getpid())
	maintenance, err := pgx.Connect(t.Context(), server.On("postgres").URL)
	require.NoError(t, err)
	drop := func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	}
	drop()
	_, err = maintenance.Exec(t.Context(), `CREATE DATABASE `+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		drop()
		_ = maintenance.Close(context.Background())
	})
	source := server.On(name)
	conn, err := pgx.Connect(t.Context(), source.URL)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	_, err = conn.Exec(t.Context(), `CREATE TABLE invented (id int); INSERT INTO invented VALUES (1), (2), (3)`)
	require.NoError(t, err)
	return source
}

func TestABackupFromTheScreenIsEncryptedListedAndRehearsed(t *testing.T) {
	source := backupSource(t)
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)

	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	sshPublic, sshPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshKey, err := ssh.NewPublicKey(sshPublic)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(sshPrivate, "", []byte("correct horse"))
	require.NoError(t, err)
	sshIdentity := string(pem.EncodeToMemory(block))
	c.env.Backups = NewBackups(c.env.Cfg, c.env.DB)
	c.env.Backups.Dir = t.TempDir()
	c.env.Backups.Source.Database = source
	c.env.Backups.Source.StoragePath = t.TempDir()
	c.env.Backups.Source.SecretsDir = ""
	c.put("/admin/backups/settings", map[string]any{
		"at": "03:30", "keep_days": 14,
		"recipients": []map[string]any{
			{"recipient": identity.Recipient().String(), "label": "test"},
			{"recipient": string(ssh.MarshalAuthorizedKey(sshKey)), "label": "ssh"},
		},
	}).requireStatus(http.StatusOK)

	c.post("/admin/backups/run", map[string]any{}).requireStatus(http.StatusAccepted)
	require.Eventually(t, func() bool { return !c.env.Backups.Running() }, 60*time.Second, 50*time.Millisecond)

	body := c.get("/admin/backups").requireStatus(http.StatusOK).json()
	runs := body["runs"].([]any)
	require.NotEmpty(t, runs)
	last := runs[0].(map[string]any)
	require.Equal(t, "succeeded", last["status"], last["error"])
	require.Equal(t, "manual", last["trigger"])
	sets := body["sets"].([]any)
	require.Len(t, sets, 1)
	one := sets[0].(map[string]any)
	require.Equal(t, true, one["encrypted"])
	require.Equal(t, true, one["verified"])
	require.Equal(t, true, one["intact"])
	require.Equal(t, true, one["key_matches"])
	name := one["name"].(string)

	stranger, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	c.post("/admin/backups/sets/"+name+"/rehearse", map[string]any{"identity": stranger.String()}).
		requireStatus(http.StatusUnprocessableEntity)
	c.post("/admin/backups/sets/"+name+"/rehearse", map[string]any{"identity": ""}).
		requireStatus(http.StatusUnprocessableEntity)
	c.post("/admin/backups/sets/no-such-set/rehearse", map[string]any{"identity": identity.String()}).
		requireStatus(http.StatusNotFound)

	rehearsed := c.post("/admin/backups/sets/"+name+"/rehearse", map[string]any{"identity": identity.String()})
	rehearsed.requireStatus(http.StatusOK)
	require.NotContains(t, rehearsed.Body.String(), "AGE-SECRET-KEY", "the identity is never echoed")
	report := rehearsed.json()
	require.EqualValues(t, 3, report["rows"])
	require.Equal(t, []any{map[string]any{"table": "invented", "rows": float64(3)}}, report["tables"])

	rehearse := func(passphrase string) *response {
		return c.post("/admin/backups/sets/"+name+"/rehearse",
			map[string]any{"identity": sshIdentity, "passphrase": passphrase})
	}
	asked := rehearse("")
	asked.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, asked.Body.String(), "passphrase")
	rehearse("wrong horse").requireStatus(http.StatusUnprocessableEntity)
	opened := rehearse("correct horse")
	opened.requireStatus(http.StatusOK)
	require.NotContains(t, opened.Body.String(), "correct horse", "the passphrase is never echoed")
	require.EqualValues(t, 3, opened.json()["rows"])
}
