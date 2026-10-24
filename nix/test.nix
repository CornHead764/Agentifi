# Boots a VM with the service and local Postgres, and checks that it migrates,
# serves the web app, and answers the API. Linux only:
#   nix build .#checks.x86_64-linux.nixos   (or aarch64-linux)
{ self, pkgs }:

let
  browser = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
in
pkgs.testers.runNixOSTest {
  name = "agentifi";
  # Lets the node allow unfree google-chrome.
  node.pkgsReadOnly = false;

  nodes.machine = { lib, ... }: {
    imports = [ self.nixosModules.default ];
    environment.etc."agentifi-secret-key".text = "0123456789abcdef0123456789abcdef";
    services.agentifi = {
      enable = true;
      secretKeyFile = "/etc/agentifi-secret-key";
      # Google Chrome exists only on x86_64-linux.
      browser.enable = browser;
    };
    nixpkgs.config.allowUnfreePredicate = p: lib.getName p == "google-chrome";
    environment.systemPackages = [ pkgs.curl ];
    virtualisation.memorySize = 2048;
  };

  testScript = ''
    machine.wait_for_unit("agentifi.service")
    machine.wait_for_open_port(8000)
    machine.succeed("curl -sf http://127.0.0.1:8000/ | grep -i '<div id=\"root\"'")
    # A JSON answer from the API, not the web app's index page.
    machine.succeed("curl -s http://127.0.0.1:8000/api/v1/auth/me -o /dev/null -w '%{content_type}' | grep -F application/json")
  '' + pkgs.lib.optionalString browser ''
    # The service was handed a driver that runs. That its version is the one
    # playwright-go accepts is the flake's playwright-driver-version check.
    env = machine.succeed("systemctl show agentifi -p Environment")
    driver = next(w.split("=", 1)[1] for w in env.split() if w.startswith("PLAYWRIGHT_DRIVER_PATH="))
    machine.succeed(f"{driver}/node {driver}/package/cli.js --version")
  '' + ''
    # The secret came in as a credential, not the environment.
    machine.fail("systemctl show agentifi -p Environment | grep -F SECRET_KEY")
  '';
}
