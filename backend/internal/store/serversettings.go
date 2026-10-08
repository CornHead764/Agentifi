package store

import (
	"context"
	"fmt"
	"slices"
)

// The settings one deployment shares, keyed rather than columned: the OIDC
// provider, the backups, and the environment settings Server admin may save
// (config.Settings, stored under their variable names). Every value is sealed, and a caller asks for a credential by name,
// so no struct carries the plaintext into a listing or log line.
//
// These rows configure the server and are not scoped by space. A household's
// preferences belong on spaces or memberships.

// --- OIDC ---------------------------------------------------------------------
//
// One row per setting, with the environment standing behind any never saved.
// Per-row rather than one JSON document so the fallback is per value: a
// deployment can keep OIDC_CLIENT_SECRET in its environment and change only the
// discovery URL here. Every value is sealed, not only the secret — a path that
// sometimes seals eventually does not.

const (
	OIDCEnabledSetting             = "oidc_enabled"
	OIDCProviderNameSetting        = "oidc_provider_name"
	OIDCDiscoveryURLSetting        = "oidc_discovery_url"
	OIDCClientIDSetting            = "oidc_client_id"
	OIDCClientSecretSetting        = "oidc_client_secret"
	OIDCScopesSetting              = "oidc_scopes"
	OIDCAutoRegisterSetting        = "oidc_auto_register"
	OIDCRequireVerifiedMailSetting = "oidc_require_verified_email"
	OIDCLinkExistingEmailSetting   = "oidc_link_existing_email"
)

// OIDCSettingKeys is the closed list of what may be stored; a key outside it
// is refused, so a typo is an error.
var OIDCSettingKeys = []string{
	OIDCEnabledSetting,
	OIDCProviderNameSetting,
	OIDCDiscoveryURLSetting,
	OIDCClientIDSetting,
	OIDCClientSecretSetting,
	OIDCScopesSetting,
	OIDCAutoRegisterSetting,
	OIDCRequireVerifiedMailSetting,
	OIDCLinkExistingEmailSetting,
}

// --- Backups ------------------------------------------------------------------
//
// The schedule, the retention and the age recipients. The recipients are one
// value, a JSON list, since they are saved together.

const (
	BackupAtSetting         = "backup_at"
	BackupKeepDaysSetting   = "backup_keep_days"
	BackupRecipientsSetting = "backup_recipients"
)

var BackupSettingKeys = []string{
	BackupAtSetting,
	BackupKeepDaysSetting,
	BackupRecipientsSetting,
}

// GetServerSettings reads the settings among keys saved through the app. A key
// never saved is absent, not "": absent means the environment still answers
// for it.
func (s *Store) GetServerSettings(ctx context.Context, keys []string) (map[string]string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return nil, err
	}
	sealed, err := queryAll(ctx, s.db, "server settings", scanPair[string, string],
		`SELECT key, value_encrypted FROM server_settings WHERE key IN (SELECT value FROM json_each($1))`,
		keys)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, one := range sealed {
		value, err := cipher.Open(serverSettingContext(one.first), one.second)
		if err != nil {
			return nil, err
		}
		out[one.first] = value
	}
	return out, nil
}

// SetServerSettings seals and upserts the given settings in one transaction,
// since a half-changed provider cannot authenticate. keys is the closed list
// the values must come from. Keys absent from the map are left alone, which
// is how an unchanged client secret survives a save.
func (s *Store) SetServerSettings(ctx context.Context, keys []string, values map[string]string) error {
	return s.saveServerSettings(ctx, keys, values, false)
}

// ReplaceServerSettings is SetServerSettings for a form that is the whole
// list: a key in keys but absent from values is deleted, so the environment or
// the default answers for it again.
func (s *Store) ReplaceServerSettings(ctx context.Context, keys []string, values map[string]string) error {
	return s.saveServerSettings(ctx, keys, values, true)
}

func (s *Store) saveServerSettings(
	ctx context.Context, keys []string, values map[string]string, deleteAbsent bool,
) error {
	cipher, err := s.requireCipher()
	if err != nil {
		return err
	}
	for key := range values {
		if !slices.Contains(keys, key) {
			return fmt.Errorf("store: %q is not one of the settings being saved", key)
		}
	}
	return s.InTx(ctx, func(tx *Store) error {
		for _, key := range keys {
			value, given := values[key]
			if !given {
				if deleteAbsent {
					if _, err := tx.db.Exec(ctx, `DELETE FROM server_settings WHERE key = $1`, key); err != nil {
						return wrap("server settings", err)
					}
				}
				continue
			}
			sealed, err := cipher.Seal(serverSettingContext(key), value)
			if err != nil {
				return err
			}
			_, err = tx.db.Exec(ctx, `
				INSERT INTO server_settings (key, value_encrypted, updated_at)
				VALUES ($1, $2, now())
				ON CONFLICT (key) DO UPDATE
				    SET value_encrypted = EXCLUDED.value_encrypted, updated_at = now()`,
				key, sealed)
			if err != nil {
				return wrap("server settings", err)
			}
		}
		return nil
	})
}
