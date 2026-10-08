{
  description = "An operator for useful stuff in your CLUSTER";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    systems.url = "github:UnstoppableMango/nix-systems";

    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };

    kubepkgs = {
      url = "github:unmango/kubepkgs";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.systems.follows = "systems";
      inputs.flake-parts.follows = "flake-parts";
      inputs.treefmt-nix.follows = "treefmt-nix";
    };

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = import inputs.systems;

      imports = with inputs; [
        systems.flakeModule
        treefmt-nix.flakeModule
      ];

      perSystem =
        { pkgs, inputs', ... }:
        let
          # Tracks the minor of k8s.io/api in go.mod, so envtest serves the same
          # API the generated clients and CRDs were built against.
          k8s = inputs'.kubepkgs.legacyPackages.kubernetes."1.37";

          # The layout setup-envtest would download: kube-apiserver, etcd and
          # kubectl side by side in one bin/, which KUBEBUILDER_ASSETS points at.
          envtest = pkgs.symlinkJoin {
            name = "envtest-${k8s.kube-apiserver.version}";
            paths = [
              k8s.kube-apiserver
              k8s.kubectl
              k8s.deps.etcd
            ];
          };
        in
        {
          packages.envtest = envtest;

          devShells.default = pkgs.mkShellNoCC {
            packages = with pkgs; [
              direnv
              gnumake
              go
              kubernetes-helm
              nixfmt
              k8s.kubectl
              k8s.sigs.kind
              k8s.sigs.kustomize
            ];

            KUBEBUILDER_ASSETS = "${envtest}/bin";
            # mkShellNoCC carries no C compiler, and with cgo on every go build
            # (go tool controller-gen included) dies on runtime/cgo. The
            # manager is built with CGO_ENABLED=0 in the Dockerfile anyway.
            CGO_ENABLED = "0";
          };

          treefmt = {
            programs = {
              actionlint.enable = true;
              deadnix.enable = true;
              gofmt.enable = true;
              nixfmt.enable = true;
              statix.enable = true;
              zizmor.enable = true;
            };

            settings.global.excludes = [
              "dist/**"
              "**/zz_generated.*.go"
              ".claude/**"
            ];
          };
        };
    };
}
