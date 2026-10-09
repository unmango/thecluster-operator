# THECLUSTER Operator

[![Hercules CI](https://hercules-ci.com/api/v1/site/github/account/unmango/project/thecluster-operator/badge)](https://hercules-ci.com/github/unmango/thecluster-operator)

An operator for useful stuff in your CLUSTER. WIP.

## APIs

| Group                    | Kind              | What it does                                      |
| ------------------------ | ----------------- | ------------------------------------------------- |
| `actions.thecluster.io`  | `Repository`      | A GitHub Actions runner scale set for one repo    |
| `core.thecluster.io`     | `WireguardClient` | Runs a linuxserver/wireguard client               |
| `pia.thecluster.io`      | `WireguardConfig` | Generates a WireGuard config from PIA credentials |
| `registry.thecluster.io` | `Registry`        | Connects to a Harbor instance                     |
| `registry.thecluster.io` | `ProxyCache`      | A pull-through cache of one upstream registry     |

Samples live in `config/samples/`.

### Runners

The `actions.thecluster.io` group is a thin layer over [actions-runner-controller](https://github.com/actions/actions-runner-controller)'s scale sets, which must already be installed.

- `Repository` is cluster scoped and needs only `spec.url`.
  It renders what the `gha-runner-scale-set` chart would: a namespace (`arc-<name>` by default), a copy of the GitHub credentials Secret, the runner ServiceAccount, the controller's Role and RoleBinding, and the `AutoscalingRunnerSet`.
  Every other field defaults from the operator's flags: a dind runner with a `/nix` volume, `--max-runners`, `--runner-scale-set-name`, and `--github-config-secret`.
  `spec.template` replaces the default runner pod outright.
  The manager needs `--arc-version`, `--arc-service-account`, `--runner-image`, and `--dind-image`; without them every Repository reports `Misconfigured`.
  Deleting a Repository deletes the scale set, waits for the controller to clean it up, then deletes the namespace.

### Registries

The `registry.thecluster.io` group describes registries by what they do rather than how a product configures it.
Harbor is the only backend so far.

- `Registry` points at a Harbor instance with an administrator's password from a Secret, and reports `Ready` plus the host clients pull from.
- `ProxyCache` is a pull-through cache of one upstream (Docker Hub, ghcr.io, quay.io, or any distribution registry).
  On Harbor it becomes a registry endpoint and a proxy-cache project, both named after the ProxyCache, and `status.endpoint` is the prefix to pull through.
  Settings changed in Harbor are put back on the next resync, every ten minutes.
  Deleting the ProxyCache deletes the project, its cached images, and the endpoint.

## Development

Everything comes from the Nix devshell: copy `hack/example.envrc` to `.envrc` for direnv, or prefix commands with `nix develop -c`.

```sh
make test       # envtest suites; KUBEBUILDER_ASSETS is set by the devshell
make lint       # golangci-lint
make fmt        # treefmt
make check      # nix flake check
make manifests generate  # regenerate CRDs, RBAC and deepcopy code after editing api/
make kind-cluster test-e2e
```

## Install

The Helm chart is in `dist/chart`, regenerated from `config/` by `make helm`.
Images are published to `ghcr.io/unmango/thecluster-operator`.

```sh
helm install thecluster-operator ./dist/chart \
  --namespace thecluster-operator-system --create-namespace
```
