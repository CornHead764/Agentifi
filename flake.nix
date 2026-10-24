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

      # playwright-go v0.MMpp.x runs driver 1.MM.x, and the driver refuses a
      # client whose major.minor differs, so the packaged driver must follow
      # backend/go.mod.
      checks = forAll (pkgs:
        let
          goMod = builtins.readFile ./backend/go.mod;
          m = builtins.match ".*playwright-go v0\\.([0-9]+)[0-9]{2}\\.[0-9]+.*" goMod;
          want = "1.${builtins.elemAt m 0}";
          have = nixpkgs.lib.versions.majorMinor self.packages.${pkgs.stdenv.hostPlatform.system}.playwright-driver.version;
        in {
          playwright-driver-version = assert nixpkgs.lib.assertMsg (m != null && want == have)
            "nix/playwright-driver.nix is ${have}, but backend/go.mod's playwright-go runs ${want}";
            pkgs.runCommand "playwright-driver-version" { } "touch $out";
        } // nixpkgs.lib.optionalAttrs (builtins.elem pkgs.stdenv.hostPlatform.system linux) {
          nixos = import ./nix/test.nix { inherit self pkgs; };
        });

      devShells = forAll (pkgs: {
        # Nix provides the tools; mise still runs the tasks in mise.toml.
        # Keep these versions in step with go.mod and frontend/mise.toml.
        default = pkgs.mkShell {
          packages = with pkgs; [ go_1_26 nodejs_26 mise postgresql_17 ];
          # Stop mise from installing its own copies of tools Nix supplies.
          MISE_DISABLE_TOOLS = "go,node";
          # mise reads only trusted config files. Trust this repo's own three,
          # and nothing else.
          shellHook = ''
            root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
            for f in mise.toml backend/mise.toml frontend/mise.toml; do
              [ -f "$root/$f" ] && mise trust --quiet "$root/$f"
            done
            unset root f
          '';
        };
      });
    };
}
