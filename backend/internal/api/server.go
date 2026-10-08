package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/CornHead764/agentifi/backend/internal/buildinfo"
	"github.com/CornHead764/agentifi/backend/internal/config"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The server itself, from the administration screen: what build is running,
// and the environment settings an administrator may save (config.Settings).
//
// A saved setting sits beneath the environment: a variable set in the
// environment or .env wins and is shown read-only. Secrets are not in the
// list, so no response here could carry one.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAdminServerServiceHandler(adminServerService{env}, opts...)
	})
}

type adminServerService struct{ env *Env }

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

func (s adminServerService) GetServerInfo(ctx context.Context, _ *agentifiv1.GetServerInfoRequest) (*agentifiv1.GetServerInfoResponse, error) {
	version, err := s.env.DB.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	build := buildinfo.Read()
	return &agentifiv1.GetServerInfoResponse{
		Commit:        build.Commit,
		BuiltAt:       build.BuiltAt,
		Modified:      build.Modified,
		GoVersion:     build.GoVersion,
		TimeZone:      s.env.now().Format("MST (UTC-07:00)"),
		SchemaVersion: version,
	}, nil
}

func (s adminServerService) GetServerSettings(ctx context.Context, _ *agentifiv1.GetServerSettingsRequest) (*agentifiv1.GetServerSettingsResponse, error) {
	stored, err := storedSettings(ctx, s.env.Cfg, s.env.DB)
	if err != nil {
		return nil, err
	}
	_, resolved, err := config.Resolve(stored)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetServerSettingsResponse{Settings: settingsProto(s.env, resolved)}, nil
}

// UpdateServerSettings saves every value sent. A setting left out is cleared,
// so the default answers for it; one set by the environment may not be sent.
func (s adminServerService) UpdateServerSettings(ctx context.Context, req *agentifiv1.UpdateServerSettingsRequest) (*agentifiv1.UpdateServerSettingsResponse, error) {
	_, current, err := config.Resolve(nil)
	if err != nil {
		return nil, err
	}

	values := map[string]string{}
	for key, value := range req.GetValues() {
		if _, ok := config.SettingByKey(key); !ok {
			return nil, errInvalid("unknown", []string{"body", "values", key},
				"%s is not a setting this screen can save", key)
		}
		if current[key].Source == config.FromEnvironment {
			return nil, errInvalid("environment", []string{"body", "values", key},
				"%s is set by the environment, which wins over anything saved here", key)
		}
		values[key] = strings.TrimSpace(value)
	}
	// Checked as the next start would read it, so a value that would refuse
	// to start is refused here instead.
	live, resolved, err := config.Resolve(values)
	if err != nil {
		return nil, errInvalid("invalid", []string{"body", "values"},
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
	sealed, err := settingsStore(s.env.Cfg, s.env.DB)
	if err != nil {
		return nil, err
	}
	if err := sealed.ReplaceServerSettings(ctx, replace, values); err != nil {
		return nil, err
	}
	live.SimpleFINAllowPrivate = s.env.Cfg.SimpleFINAllowPrivate
	s.env.live.Store(live)
	return &agentifiv1.UpdateServerSettingsResponse{Settings: settingsProto(s.env, resolved)}, nil
}

func settingsProto(env *Env, resolved map[string]config.Resolved) []*agentifiv1.ServerSetting {
	out := make([]*agentifiv1.ServerSetting, 0, len(config.Settings))
	for _, setting := range config.Settings {
		now := resolved[setting.Key]
		started, known := env.started[setting.Key]
		out = append(out, &agentifiv1.ServerSetting{
			Key:            setting.Key,
			Group:          setting.Group,
			Label:          setting.Label,
			Help:           setting.Help,
			Kind:           string(setting.Kind),
			Value:          now.Value,
			Default:        now.Default,
			Source:         string(now.Source),
			Live:           setting.Live,
			PendingRestart: !setting.Live && known && started.Value != now.Value,
		})
	}
	return out
}
