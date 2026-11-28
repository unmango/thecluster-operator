{
  bash,
  buildGoApplication,
  envtest,
  go,
  gnumake,
  lib,
  makeWrapper,
  symlinkJoin,
}:
let
  # The sandbox has no default route for kube-apiserver to advertise.
  sandboxEnvtest = symlinkJoin {
    name = "${envtest.name}-sandbox";
    paths = [ envtest ];
    nativeBuildInputs = [ makeWrapper ];
    postBuild = ''
      wrapProgram $out/bin/kube-apiserver --add-flags '--advertise-address=127.0.0.1 --endpoint-reconciler-type=none'
    '';
  };

  check =
    name: script:
    buildGoApplication {
      pname = "thecluster-operator-${name}";
      version = "0";
      inherit go;

      src = lib.cleanSource ../.;
      modules = ./gomod2nix.toml;

      nativeBuildInputs = [ gnumake ];

      CGO_ENABLED = 0;
      env.KUBEBUILDER_ASSETS = "${sandboxEnvtest}/bin";

      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR XDG_CACHE_HOME=$TMPDIR/cache
        ${script}
        runHook postBuild
      '';

      doCheck = false;
      installPhase = "touch $out";
    };

  # The Makefile's SHELL is /usr/bin/env, which the sandbox lacks.
  make = "make SHELL='${lib.getExe bash} -o pipefail'";

  controllerTools = (lib.importTOML ./gomod2nix.toml).mod."sigs.k8s.io/controller-tools".version;
in
{
  test = check "test" ''
    # controller-gen loads each vendored module's go.mod as a root.
    chmod -R u+w vendor
    find vendor \( -name go.mod -o -name go.work \) -delete
    # controller-gen stamps its version into every CRD, and a vendored build has none.
    go build -o "$TMPDIR/controller-gen" \
      -ldflags "-X sigs.k8s.io/controller-tools/pkg/version.version=${controllerTools}" \
      sigs.k8s.io/controller-tools/cmd/controller-gen
    ${make} test CONTROLLER_GEN="$TMPDIR/controller-gen"
    # Generated files must match the committed source.
    diff -r --exclude=vendor --exclude=cover.out "$src" .
  '';

  lint = check "lint" "${make} lint";
}
