# The agentifi binary with the web app embedded, built the way the Dockerfile
# builds it: the frontend first, then Go with the bundle copied into
# internal/web/dist.
{
  lib,
  buildGoModule,
  buildNpmPackage,
  importNpmLock,
  nodejs_26,
  buf,
  src,
  commit ? "",
  builtAt ? "",
}:

assert buf.version == "1.73.0";

let
  version = (lib.importJSON (src + "/frontend/package.json")).version;

  nodeModules = buildNpmPackage {
    pname = "agentifi-node-modules";
    inherit version src;
    sourceRoot = "source/frontend";
    nodejs = nodejs_26;
    npmDeps = importNpmLock { npmRoot = src + "/frontend"; };
    npmConfigHook = importNpmLock.npmConfigHook;
    dontNpmBuild = true;
    installPhase = "mkdir -p $out && cp -r node_modules $out/node_modules";
  };

  generated = buildGoModule {
    pname = "agentifi-protobuf";
    inherit version src;
    modRoot = "backend";
    # `go mod vendor` needs the generated packages the backend imports, which
    # this derivation produces, so the plugins come from the module cache.
    proxyVendor = true;
    vendorHash = "sha256-A7eVaczyTPoBCmnOuEmGxldDUl5EW+yi8Gz/ri8g0bE=";
    nativeBuildInputs = [ nodejs_26 buf ];
    buildPhase = ''
      runHook preBuild
      export HOME=$TMPDIR
      ln -s ${nodeModules}/node_modules ../frontend/node_modules
      cd ..
      (cd tools/buf && buf generate ../.. --template ../../buf.gen.yaml)
      runHook postBuild
    '';
    installPhase = ''
      mkdir -p $out/backend $out/frontend
      cp -r backend/internal/gen $out/backend/gen
      cp -r frontend/src/gen $out/frontend/gen
    '';
  };

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
    preBuild = ''
      cp -r ${generated}/frontend/gen src/gen
    '';
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
  # Update when backend/go.sum changes: set to lib.fakeHash, build, and copy
  # the hash from the error.
  vendorHash = "sha256-dSnaXvzDKe/qzYa8K6jaksij5c8jARBBa/tH54wpzAA=";
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

  passthru = { inherit frontend; };

  meta = {
    description = "Self-hosted personal finance";
    homepage = "https://github.com/CornHead764/Agentifi";
    license = lib.licenses.gpl3Only;
    mainProgram = "agentifi";
  };
}
