# THECLUSTER Operator

An operator for useful stuff in your CLUSTER. WIP.

## APIs

| Group                | Kind              | What it does                                              |
| -------------------- | ----------------- | --------------------------------------------------------- |
| `core.thecluster.io` | `WireguardClient` | Runs a linuxserver/wireguard client                       |
| `pia.thecluster.io`  | `WireguardConfig` | Generates a WireGuard config from PIA credentials         |

Samples live in `config/samples/`.

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
