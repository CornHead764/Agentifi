# The Playwright driver playwright-go runs: the playwright-core package plus a
# node binary, laid out as scripts/playwright-driver.sh lays it out.
#
# playwright-go only runs the exact version it pins, so this is not nixpkgs'
# playwright-driver. The flake's playwright-driver-version check fails when
# backend/go.mod moves to a playwright-go that pins another version.
{
  lib,
  stdenvNoCC,
  fetchurl,
  nodejs_26,
}:

stdenvNoCC.mkDerivation (finalAttrs: {
  pname = "agentifi-playwright-driver";
  version = "1.62.0";

  src = fetchurl {
    url = "https://registry.npmjs.org/playwright-core/-/playwright-core-${finalAttrs.version}.tgz";
    hash = "sha256-JXsOKfuAcDnT1n/nyRiKK5aWQDxXghsQy2yNY145eJo=";
  };

  dontBuild = true;
  installPhase = ''
    runHook preInstall
    mkdir -p $out/package
    tar xzf $src --strip-components=1 -C $out/package
    ln -s ${lib.getExe nodejs_26} $out/node
    runHook postInstall
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    $out/node $out/package/cli.js --version | grep -F ${finalAttrs.version}
  '';

  meta.license = lib.licenses.asl20;
})
