# NixOS service for agentifi. Import it from the flake as
# agentifi.nixosModules.default.
{ self }:
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.agentifi;
  inherit (lib) mkOption mkEnableOption mkIf types;
  system = pkgs.stdenv.hostPlatform.system;
  stateDir = "/var/lib/agentifi";

  # Values the service sets itself; cfg.settings overrides them.
  baseEnv = {
    HTTP_ADDR = "${cfg.listenAddress}:${toString cfg.port}";
    STORAGE_PATH = "${stateDir}/attachments";
    BACKUP_DIR = "${stateDir}/backups";
    AGENT_PROFILES_DIR = "${stateDir}/profiles";
    # Chrome writes a crash database and a lock file under HOME.
    HOME = stateDir;
  }
  // lib.optionalAttrs (cfg.database.url != null) { DATABASE_URL = cfg.database.url; }
  // lib.optionalAttrs cfg.browser.enable {
    PLAYWRIGHT_DRIVER_PATH = "${cfg.browser.driver}";
    PLAYWRIGHT_NODEJS_PATH = "${cfg.browser.driver}/node";
    AGENTIFI_CHROME_PATH = lib.getExe cfg.browser.package;
  };

  toEnv = v: if lib.isBool v then lib.boolToString v else toString v;
  environment = lib.mapAttrs (_: toEnv) (baseEnv // cfg.settings);

  credentials = { SECRET_KEY = cfg.secretKeyFile; } // cfg.credentialFiles;
in
{
  options.services.agentifi = {
    enable = mkEnableOption "agentifi, self-hosted personal finance";

    package = mkOption {
      type = types.package;
      default = self.packages.${system}.agentifi;
      defaultText = lib.literalExpression "agentifi.packages.\${system}.agentifi";
      description = "The agentifi package to run.";
    };

    listenAddress = mkOption {
      type = types.str;
      default = "127.0.0.1";
      description = "Address to listen on. Use a reverse proxy for TLS.";
    };

    port = mkOption {
      type = types.port;
      default = 8000;
      description = "Port to listen on.";
    };

    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = "Open the port in the firewall.";
    };

    secretKeyFile = mkOption {
      type = types.path;
      example = "/run/secrets/agentifi-secret-key";
      description = ''
        File holding SECRET_KEY, at least 32 characters
        (`openssl rand -hex 32`). It signs sign-in tokens and, unless
        CREDENTIAL_ENCRYPTION_KEY is set, seals stored bank credentials.
        Read through systemd credentials, so it never enters the
        environment or the Nix store.
      '';
    };

    credentialFiles = mkOption {
      type = types.attrsOf types.path;
      default = { };
      example = lib.literalExpression ''
        {
          CREDENTIAL_ENCRYPTION_KEY = "/run/secrets/agentifi-credential-key";
          OIDC_CLIENT_SECRET = "/run/secrets/agentifi-oidc";
        }
      '';
      description = ''
        Other secrets, by the name of the setting they fill, read through
        systemd credentials. Any setting agentifi reads as a secret works
        here: CREDENTIAL_ENCRYPTION_KEY, DATABASE_URL, OIDC_CLIENT_SECRET,
        SMTP_PASSWORD, VAPID_PRIVATE_KEY, EBAY_CERT_ID,
        OPENEXCHANGERATES_APP_ID.
      '';
    };

    settings = mkOption {
      type = types.attrsOf (types.oneOf [ types.str types.int types.bool ]);
      default = { };
      example = {
        FRONTEND_URL = "https://money.example.com";
        WEBAUTHN_RP_ID = "money.example.com";
        WEBAUTHN_ORIGIN = "https://money.example.com";
        PRIMARY_CURRENCY = "EUR";
      };
      description = ''
        Environment settings for agentifi (see the repository's
        .env.example). Never put secrets here: they end up in the Nix
        store. Use credentialFiles instead.
      '';
    };

    database = {
      createLocally = mkOption {
        type = types.bool;
        default = true;
        description = ''
          Run Postgres on this machine with a database and role named
          agentifi, reached over the unix socket with peer authentication.
        '';
      };
      url = mkOption {
        type = types.nullOr types.str;
        default =
          if cfg.database.createLocally then "postgres://agentifi@/agentifi?host=/run/postgresql" else null;
        defaultText = lib.literalExpression ''
          if database.createLocally then "postgres://agentifi@/agentifi?host=/run/postgresql" else null
        '';
        description = ''
          libpq URL of the database. Leave null and set DATABASE_URL in
          credentialFiles when the URL carries a password.
        '';
      };
    };

    backupTools = mkOption {
      type = types.package;
      default = config.services.postgresql.package;
      defaultText = lib.literalExpression "config.services.postgresql.package";
      description = ''
        Package providing pg_dump and pg_restore for backups. Its major
        version must be at least the server's.
      '';
    };

    browser = {
      enable = mkEnableOption ''
        the Chrome-based bill and merchant connectors. Connectors that
        need Firefox (Camoufox) are not packaged'';
      package = mkOption {
        type = types.package;
        default = pkgs.google-chrome;
        defaultText = lib.literalExpression "pkgs.google-chrome";
        description = ''
          Browser the connectors drive. The project runs Google Chrome stable
          and the connectors present themselves as Chrome, so another browser
          is a fingerprint mismatch. pkgs.google-chrome is unfree (allow it
          in nixpkgs.config) and exists only on x86_64-linux; elsewhere the
          module refuses to evaluate with the browser enabled.
        '';
      };
      driver = mkOption {
        type = types.package;
        default = self.packages.${system}.playwright-driver;
        defaultText = lib.literalExpression "agentifi.packages.\${system}.playwright-driver";
        description = "Playwright driver matching the playwright-go agentifi was built with.";
      };
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.database.url != null || cfg.credentialFiles ? DATABASE_URL;
        message = "services.agentifi: set database.url, or DATABASE_URL in credentialFiles.";
      }
    ]
    ++ lib.optional cfg.browser.enable {
      assertion = lib.meta.availableOn pkgs.stdenv.hostPlatform cfg.browser.package;
      message = "services.agentifi.browser: ${cfg.browser.package.name} is not available on ${system}; Google Chrome stable ships for x86_64-linux only.";
    };

    services.postgresql = mkIf cfg.database.createLocally {
      enable = true;
      ensureDatabases = [ "agentifi" ];
      ensureUsers = [
        {
          name = "agentifi";
          ensureDBOwnership = true;
        }
      ];
    };

    users.users.agentifi = {
      isSystemUser = true;
      group = "agentifi";
      home = stateDir;
    };
    users.groups.agentifi = { };

    networking.firewall.allowedTCPPorts = mkIf cfg.openFirewall [ cfg.port ];

    systemd.services.agentifi = {
      description = "Agentifi";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ] ++ lib.optional cfg.database.createLocally "postgresql.service";
      requires = lib.optional cfg.database.createLocally "postgresql.service";
      inherit environment;
      path = [ cfg.backupTools ];

      serviceConfig = {
        User = "agentifi";
        Group = "agentifi";
        # The binary refuses to serve a database behind its migrations, so
        # each start applies them first.
        ExecStartPre = "${lib.getExe cfg.package} migrate";
        ExecStart = "${lib.getExe cfg.package} serve";
        Restart = "on-failure";
        RestartSec = 5;

        LoadCredential = lib.mapAttrsToList (name: path: "${name}:${path}") credentials;
        StateDirectory = "agentifi agentifi/attachments agentifi/backups agentifi/profiles";
        StateDirectoryMode = "0700";
        WorkingDirectory = stateDir;
        UMask = "0077";

        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectProc = "invisible";
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" "AF_NETLINK" ];
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        CapabilityBoundingSet = "";
        SystemCallArchitectures = "native";
        # Chromium and node JIT-compile, so MemoryDenyWriteExecute stays off;
        # Chromium also needs namespaces unless it runs without its sandbox.
        RestrictNamespaces = !cfg.browser.enable;
      };
    };
  };
}
