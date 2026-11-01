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

  # Generated code is not committed. It is generated once, here, and copied
  # into both the module derivation and the build.
  generated = buildGoModule {
    pname = "agentifi-generated";
    inherit version src;
    modRoot = "backend";
    # `go mod vendor` needs the generated packages the backend imports, which
    # this derivation produces, so the generators come from the module cache.
    proxyVendor = true;
    # Update when backend/go.sum changes, as for the build's vendorHash below.
    vendorHash = "sha256-R4+Mff7H08uwSesFNDGN3ny3IjGuu1S92X68S8vKeS0=";
    env.CGO_ENABLED = 0;
    buildPhase = ''
      runHook preBuild
      cd ..
      go -C backend run ./cmd/jetgen internal/gen/jet
      runHook postBuild
    '';
    doCheck = false;
    installPhase = ''
      mkdir -p $out/backend
      cp -r backend/internal/gen $out/backend/gen
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
  # Vendoring resolves every package the module imports, generated ones included.
  overrideModAttrs = _: {
    preBuild = "cp -r ${generated}/backend/gen internal/gen";
  };

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
    cp -r ${generated}/backend/gen internal/gen
    rm -rf internal/web/dist
    cp -r ${frontend} internal/web/dist
  '';

  # CI runs the tests.
  doCheck = false;

  passthru = { inherit frontend generated; };

  meta = {
    description = "Self-hosted personal finance";
    homepage = "https://github.com/CornHead764/Agentifi";
    license = lib.licenses.gpl3Only;
    mainProgram = "agentifi";
  };
}
