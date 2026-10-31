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
  version = (lib.importJSON (src + "/clients/apps/web/package.json")).version;

  # The frontend imports the exporters in tools/, so the whole tree is
  # unpacked and the build runs in the clients/ workspace.
  frontend = buildNpmPackage {
    pname = "agentifi-frontend";
    inherit version src;
    sourceRoot = "source/clients";
    npmWorkspace = "apps/web";
    nodejs = nodejs_26;
    # Dependencies come straight from package-lock.json, so there is no
    # npmDepsHash to update when the lockfile changes.
    npmDeps = importNpmLock { npmRoot = src + "/clients"; };
    npmConfigHook = importNpmLock.npmConfigHook;
    installPhase = ''
      runHook preInstall
      cp -r apps/web/dist $out
      runHook postInstall
    '';
  };

  buildinfo = "github.com/CornHead764/agentifi/backend/internal/buildinfo";
in
buildGoModule {
  pname = "agentifi";
  inherit version src;
  modRoot = "backend";
  # Update when backend/go.sum changes: set to lib.fakeHash, build, and copy
  # the hash from the error.
  vendorHash = "sha256-huJvvWwZrCJyZaw9hGhZn+cifIn1ZzUiOWbkf0vNyL8=";

  subPackages = [ "cmd/agentifi" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X ${buildinfo}.Version=${version}"
    "-X ${buildinfo}.Commit=${commit}"
    "-X ${buildinfo}.BuiltAt=${builtAt}"
  ];

  # go-jet's packages are generated here from the migrations, with the
  # vendored generator, rather than committed.
  preBuild = ''
    go run ./cmd/jetgen internal/gen/jet
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
