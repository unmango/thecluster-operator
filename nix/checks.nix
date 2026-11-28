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
  # kube-apiserver derives its advertise address from the default route, and
  # the sandbox has only loopback.
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
    # controller-gen's paths=./... loads every nested go.mod as a root, and
    # gomod2nix's vendor/ keeps the modules' go.mod and go.work files.
    chmod -R u+w vendor
    find vendor \( -name go.mod -o -name go.work \) -delete
    # A vendored build carries no module version, and controller-gen stamps
    # its version into every CRD.
    go build -o "$TMPDIR/controller-gen" \
      -ldflags "-X sigs.k8s.io/controller-tools/pkg/version.version=${controllerTools}" \
      sigs.k8s.io/controller-tools/cmd/controller-gen
    ${make} test CONTROLLER_GEN="$TMPDIR/controller-gen"
    # CI's git status check: manifests and generate must not change or add a file.
    diff -r --exclude=vendor --exclude=cover.out "$src" .
  '';

  lint = check "lint" "${make} lint";
}
