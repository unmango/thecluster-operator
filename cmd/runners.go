/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	actionscontroller "github.com/unmango/thecluster-operator/internal/controller/actions"
)

// runnerFlags configure the Repository controller. Images and the
// actions-runner-controller version have no defaults on purpose: they are
// pinned where the operator is deployed, so Renovate bumps them there.
type runnerFlags struct {
	controllerVersion        string
	controllerServiceAccount string
	githubConfigSecret       string
	namespacePrefix          string
	runnerScaleSetName       string
	maxRunners               int
	runnerImage              string
	dindImage                string
	storageClass             string
	nixStorageSize           string
	workSize                 string
	runnerNixConfig          string
}

func (f *runnerFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&f.controllerVersion, "arc-version", "",
		"Version of the actions-runner-controller in the cluster. Scale sets are labelled with it, "+
			"and the controller deletes any whose major or minor version differs.")
	fs.StringVar(&f.controllerServiceAccount, "arc-service-account", "",
		"The actions-runner-controller's ServiceAccount as namespace/name, bound to each scale set's manager Role.")
	fs.StringVar(&f.githubConfigSecret, "github-config-secret", "",
		"GitHub credentials Secret as namespace/name, copied into a Repository's namespace when it names none.")
	fs.StringVar(&f.namespacePrefix, "runner-namespace-prefix", "arc-",
		"Prefix of the namespace a Repository gets when it names none.")
	fs.StringVar(&f.runnerScaleSetName, "runner-scale-set-name", "thecluster",
		"Name scale sets register under, the label a workflow's runs-on selects.")
	fs.IntVar(&f.maxRunners, "max-runners", 6, "Most runners a scale set runs at once.")
	fs.StringVar(&f.runnerImage, "runner-image", "", "Image of the runner container and its init containers.")
	fs.StringVar(&f.dindImage, "dind-image", "", "Image of the docker-in-docker sidecar.")
	fs.StringVar(&f.storageClass, "runner-storage-class", "fast-rwo",
		"StorageClass of the runners' work and nix volumes.")
	fs.StringVar(&f.nixStorageSize, "nix-storage-size", "20Gi", "Size of a runner's /nix volume.")
	fs.StringVar(&f.workSize, "work-storage-size", "5Gi", "Size of a runner's work volume.")
	fs.StringVar(&f.runnerNixConfig, "runner-nix-config", "",
		"NIX_CONFIG for the runner container, for example an in-cluster binary cache. Empty sets nothing.")
}

func (f *runnerFlags) defaults() (actionscontroller.Defaults, error) {
	d := actionscontroller.Defaults{
		ControllerVersion:  f.controllerVersion,
		NamespacePrefix:    f.namespacePrefix,
		RunnerScaleSetName: f.runnerScaleSetName,
		MaxRunners:         int32(f.maxRunners),
		RunnerImage:        f.runnerImage,
		DindImage:          f.dindImage,
		StorageClass:       f.storageClass,
		RunnerNixConfig:    f.runnerNixConfig,
	}
	var err error
	if d.ControllerServiceAccount, err = namespacedName("arc-service-account", f.controllerServiceAccount); err != nil {
		return d, err
	}
	if d.GitHubConfigSecret, err = namespacedName("github-config-secret", f.githubConfigSecret); err != nil {
		return d, err
	}
	if d.NixStorageSize, err = resource.ParseQuantity(f.nixStorageSize); err != nil {
		return d, fmt.Errorf("--nix-storage-size: %w", err)
	}
	if d.WorkSize, err = resource.ParseQuantity(f.workSize); err != nil {
		return d, fmt.Errorf("--work-storage-size: %w", err)
	}
	if f.maxRunners < 0 {
		return d, fmt.Errorf("--max-runners must not be negative")
	}
	return d, nil
}

// namespacedName parses namespace/name, leaving both empty for an empty flag.
func namespacedName(flag, value string) (types.NamespacedName, error) {
	if value == "" {
		return types.NamespacedName{}, nil
	}
	ns, name, ok := strings.Cut(value, "/")
	if !ok || ns == "" || name == "" {
		return types.NamespacedName{}, fmt.Errorf("--%s must be namespace/name, got %q", flag, value)
	}
	return types.NamespacedName{Namespace: ns, Name: name}, nil
}
