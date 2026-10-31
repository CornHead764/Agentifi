# The agentifi binary with the web app embedded, built the way the Dockerfile
# builds it: the frontend first, then Go with the bundle copied into
# internal/web/dist.
{
  lib,
  buildGoModule,
  buildNpmPackage,
  importNpmLock,
  nodejs_26,
  src,
  commit ? "",
  builtAt ? "",
}:

let
  version = (lib.importJSON (src + "/frontend/package.json")).version;

  # The frontend imports the exporters in ../tools, so the whole tree is
  # unpacked and the build runs in frontend/.
  frontend = buildNpmPackage {
    pname = "agentifi-frontend";
    inherit version src;
    sourceRoot = "source/frontend";
    nodejs = nodejs_26;
    # Dependencies come straight from package-lock.json, so there is no
    # npmDepsHash to update when the lockfile changes.
    npmDeps = importNpmLock { npmRoot = src + "/frontend"; };
    npmConfigHook = importNpmLock.npmConfigHook;
    installPhase = ''
      runHook preInstall
      cp -r dist $out
      runHook postInstall
    '';
  };

  buildinfo = "github.com/CornHead764/agentifi/backend/internal/buildinfo";
in
buildGoModule {
  pname = "agentifi";
  inherit version src;
  modRoot = "backend";
  # Update when backend/go.sum changes: scripts/nix-hashes.sh rewrites it, and
  # the nix-hashes workflow commits it on Renovate's pull requests.
  vendorHash = "sha256-yUzXC2TQtGNGlmPZIvstEPidMPBkGE+7izDqYUBlnXk=";

  subPackages = [ "cmd/agentifi" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X ${buildinfo}.Version=${version}"
    "-X ${buildinfo}.Commit=${commit}"
    "-X ${buildinfo}.BuiltAt=${builtAt}"
  ];

  preBuild = ''
    rm -rf internal/web/dist
    cp -r ${frontend} internal/web/dist
  '';

  # CI runs the tests.
  doCheck = false;

  passthru = { inherit frontend; };

  meta = {
    description = "Self-hosted personal finance";
    homepage = "https://github.com/CornHead764/Agentifi";
    license = lib.licenses.gpl3Only;
    mainProgram = "agentifi";
  };
}
