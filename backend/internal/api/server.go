package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/buildinfo"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The server itself, from the administration screen: what build is running,
// and the environment settings an administrator may save (config.Settings).
//
// A saved setting sits beneath the environment: a variable set in the
// environment or .env wins and is shown read-only. Secrets are not in the
// list, so no response here could carry one.

func init() {
	RegisterAdmin(Resource{Prefix: "/admin/server", Routes: func(rt *Routes) {
		rt.Superuser(http.MethodGet, "/", readServerInfo)
		rt.Superuser(http.MethodGet, "/settings", readServerSettings)
		rt.Superuser(http.MethodPut, "/settings", saveServerSettings)
	}})
}

type AdminServerResponse struct {
	// Commit is empty for a build that was not told one.
	Commit        string `json:"commit"`
	BuiltAt       string `json:"built_at"`
	Modified      bool   `json:"modified"`
	GoVersion     string `json:"go_version"`
	TimeZone      string `json:"time_zone"`
	SchemaVersion int64  `json:"schema_version"`
}

type AdminSettingResponse struct {
	Key   string             `json:"key"`
	Group string             `json:"group"`
	Label string             `json:"label"`
	Help  string             `json:"help"`
	Kind  config.SettingKind `json:"kind"`
	// Value is what applies, written as the environment variable would be.
	Value   string `json:"value"`
	Default string `json:"default"`
	// Source is "environment", "database" or "default".
	Source config.Source `json:"source"`
	// Live settings apply on save; the rest when the server next starts.
	Live bool `json:"live"`
	// PendingRestart is true when Value differs from what this process
	// started with and the setting is read only at start.
	PendingRestart bool `json:"pending_restart"`
}

type AdminSettingsResponse struct {
	Settings []AdminSettingResponse `json:"settings"`
}

// AdminSettingsWrite is every value saved here. A setting left out is
// cleared, so the default answers for it; one set by the environment may not
// be sent.
type AdminSettingsWrite struct {
	Values map[string]string `json:"values"`
}

// Live is the configuration for the settings marked Live: the last save's,
// or the one the process started with.
func (e *Env) Live() *config.Config {
	if live := e.live.Load(); live != nil {
		return live
	}
	return e.Cfg
}

// WithStartedSettings records how config.Settings resolved at start, which a
// setting read only at start is still running on.
func WithStartedSettings(resolved map[string]config.Resolved) Option {
	return func(env *Env) { env.started = resolved }
}

// ServerConfig layers the saved settings beneath the environment for `serve`.
// Not fatal: a saved value that cannot be read or would be refused leaves the server
// on the environment alone rather than refusing to start.
func ServerConfig(ctx context.Context, cfg *config.Config, db *store.Store) (*config.Config, map[string]config.Resolved) {
	stored, err := storedSettings(ctx, cfg, db)
	if err == nil {
		var resolved *config.Config
		var started map[string]config.Resolved
		if resolved, started, err = config.Resolve(stored); err == nil {
			resolved.SimpleFINAllowPrivate = cfg.SimpleFINAllowPrivate
			return resolved, started
		}
	}
	slog.Warn("the settings saved in Server admin were not applied; the environment stands", "error", err)
	_, started, resolveErr := config.Resolve(nil)
	if resolveErr != nil {
		return cfg, nil
	}
	return cfg, started
}

func settingKeys() []string {
	keys := make([]string, len(config.Settings))
	for i, setting := range config.Settings {
		keys[i] = setting.Key
	}
	return keys
}

func storedSettings(ctx context.Context, cfg *config.Config, db *store.Store) (map[string]string, error) {
	sealed, err := settingsStore(cfg, db)
	if err != nil {
		return nil, err
	}
	return sealed.GetServerSettings(ctx, settingKeys())
}

func readServerInfo(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	version, err := env.DB.SchemaVersion(r.Context())
	if err != nil {
		return err
	}
	build := buildinfo.Read()
	return writeJSON(w, http.StatusOK, AdminServerResponse{
		Commit:        build.Commit,
		BuiltAt:       build.BuiltAt,
		Modified:      build.Modified,
		GoVersion:     build.GoVersion,
		TimeZone:      env.now().Format("MST (UTC-07:00)"),
		SchemaVersion: version,
	})
}

func readServerSettings(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	stored, err := storedSettings(r.Context(), env.Cfg, env.DB)
	if err != nil {
		return err
	}
	_, resolved, err := config.Resolve(stored)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, settingsResponse(env, resolved))
}

func saveServerSettings(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	var body AdminSettingsWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	_, current, err := config.Resolve(nil)
	if err != nil {
		return err
	}

	values := map[string]string{}
	for key, value := range body.Values {
		if _, ok := config.SettingByKey(key); !ok {
			return errInvalid("unknown", []string{"body", "values", key},
				"%s is not a setting this screen can save", key)
		}
		if current[key].Source == config.FromEnvironment {
			return errInvalid("environment", []string{"body", "values", key},
				"%s is set by the environment, which wins over anything saved here", key)
		}
		values[key] = strings.TrimSpace(value)
	}
	// Checked as the next start would read it, so a value that would refuse
	// to start is refused here instead.
	live, resolved, err := config.Resolve(values)
	if err != nil {
		return errInvalid("invalid", []string{"body", "values"},
			"%s", strings.TrimPrefix(err.Error(), "config: "))
	}

	// A setting the environment answers for keeps whatever row it has, so
	// taking the variable away later brings the saved value back.
	var replace []string
	for _, key := range settingKeys() {
		if current[key].Source != config.FromEnvironment {
			replace = append(replace, key)
		}
	}
	sealed, err := settingsStore(env.Cfg, env.DB)
	if err != nil {
		return err
	}
	if err := sealed.ReplaceServerSettings(r.Context(), replace, values); err != nil {
		return err
	}
	live.SimpleFINAllowPrivate = env.Cfg.SimpleFINAllowPrivate
	env.live.Store(live)
	return writeJSON(w, http.StatusOK, settingsResponse(env, resolved))
}

func settingsResponse(env *Env, resolved map[string]config.Resolved) AdminSettingsResponse {
	out := AdminSettingsResponse{Settings: make([]AdminSettingResponse, 0, len(config.Settings))}
	for _, setting := range config.Settings {
		now := resolved[setting.Key]
		started, known := env.started[setting.Key]
		out.Settings = append(out.Settings, AdminSettingResponse{
			Key:            setting.Key,
			Group:          setting.Group,
			Label:          setting.Label,
			Help:           setting.Help,
			Kind:           setting.Kind,
			Value:          now.Value,
			Default:        now.Default,
			Source:         now.Source,
			Live:           setting.Live,
			PendingRestart: !setting.Live && known && started.Value != now.Value,
		})
	}
	return out
}
