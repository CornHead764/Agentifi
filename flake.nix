{
  description = "Agentifi: package, NixOS module, and development shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs, ... }:
    let
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (s: f nixpkgs.legacyPackages.${s});
      linux = [ "aarch64-linux" "x86_64-linux" ];
    in {
      packages = forAll (pkgs: rec {
        agentifi = pkgs.callPackage ./nix/package.nix {
          buildGoModule = pkgs.buildGoModule.override { go = pkgs.go_1_26; };
          src = self;
          commit = self.rev or self.dirtyRev or "";
        };
        playwright-driver = pkgs.callPackage ./nix/playwright-driver.nix { };
        default = agentifi;
      });

      nixosModules.default = import ./nix/module.nix { inherit self; };

      # playwright-go runs only the exact driver version its run.go pins, so
      # the packaged driver is compared with the vendored module's pin.
      checks = forAll (pkgs:
        let
          packages = self.packages.${pkgs.stdenv.hostPlatform.system};
          have = packages.playwright-driver.version;
        in {
          playwright-driver-version = pkgs.runCommand "playwright-driver-version" { } ''
            want=$(find ${packages.agentifi.goModules} -path '*/playwright-go/run.go' \
              -exec sed -n 's/^[[:space:]]*\(const \)\{0,1\}playwrightCliVersion = "\(.*\)"$/\2/p' {} +)
            if [ "$want" != "${have}" ]; then
              echo "nix/playwright-driver.nix is ${have}, but backend/go.mod's playwright-go runs ''${want:-an unreadable version}" >&2
              exit 1
            fi
            touch $out
          '';
        } // nixpkgs.lib.optionalAttrs (builtins.elem pkgs.stdenv.hostPlatform.system linux) {
          nixos = import ./nix/test.nix { inherit self pkgs; };
        });

      devShells = forAll (pkgs: {
        # Nix provides the tools; mise still runs the tasks in mise.toml.
        # Keep these versions in step with go.mod and clients/apps/web/mise.toml.
        default = pkgs.mkShell {
          packages = with pkgs; [ go_1_26 nodejs_26 mise postgresql_17 ];
          # Stop mise from installing its own copies of tools Nix supplies.
          MISE_DISABLE_TOOLS = "go,node";
          # mise reads only trusted config files. Trust this repo's own three,
          # and nothing else.
          shellHook = ''
            root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
            for f in mise.toml backend/mise.toml clients/apps/web/mise.toml; do
              [ -f "$root/$f" ] && mise trust --quiet "$root/$f"
            done
            unset root f
          '';
        };
      });
    };
}
