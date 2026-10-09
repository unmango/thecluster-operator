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

package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	actionsv1alpha1 "github.com/unmango/thecluster-operator/api/actions/v1alpha1"
)

// Defaults is what a Repository gets for every field it leaves unset, plus
// the facts about the cluster's actions-runner-controller install that no
// Repository should have to restate. The manager fills it from flags, so image
// pins and cluster specifics stay with whoever deploys the operator.
type Defaults struct {
	// ControllerVersion is the actions-runner-controller version. The
	// controller deletes any AutoscalingRunnerSet whose version label differs
	// from its own in major or minor.
	ControllerVersion string

	// ControllerServiceAccount is the controller's ServiceAccount, which each
	// scale set's manager Role is bound to.
	ControllerServiceAccount types.NamespacedName

	// GitHubConfigSecret is the Secret copied into a Repository's namespace
	// when it names none.
	GitHubConfigSecret types.NamespacedName

	NamespacePrefix    string
	RunnerScaleSetName string
	MaxRunners         int32

	RunnerImage    string
	DindImage      string
	StorageClass   string
	NixStorageSize resource.Quantity
	WorkSize       resource.Quantity

	// RunnerNixConfig is set as NIX_CONFIG on the runner container, for
	// example to point nix at an in-cluster binary cache. Empty sets nothing.
	RunnerNixConfig string
}

// Validate reports the settings a Repository cannot be reconciled without.
func (d Defaults) Validate() error {
	var errs []error
	if d.ControllerVersion == "" {
		errs = append(errs, errors.New("the actions-runner-controller version is not set"))
	}
	if d.ControllerServiceAccount.Name == "" || d.ControllerServiceAccount.Namespace == "" {
		errs = append(errs, errors.New("the actions-runner-controller service account is not set"))
	}
	if d.RunnerImage == "" || d.DindImage == "" {
		errs = append(errs, errors.New("the runner and dind images are not set"))
	}
	return errors.Join(errs...)
}

// resolved is a Repository's spec with every default filled in.
type resolved struct {
	Namespace          string
	RunnerScaleSetName string
	MaxRunners         int32
	GitHubConfigSecret types.NamespacedName
	NixStorageSize     resource.Quantity
}

func (d Defaults) resolve(repo *actionsv1alpha1.Repository) resolved {
	r := resolved{
		Namespace:          repo.Spec.Namespace,
		RunnerScaleSetName: repo.Spec.RunnerScaleSetName,
		MaxRunners:         d.MaxRunners,
		GitHubConfigSecret: d.GitHubConfigSecret,
		NixStorageSize:     d.NixStorageSize,
	}
	if r.Namespace == "" {
		r.Namespace = DefaultNamespace(d.NamespacePrefix, repo.Name)
	}
	if r.RunnerScaleSetName == "" {
		r.RunnerScaleSetName = d.RunnerScaleSetName
	}
	if repo.Spec.MaxRunners != nil {
		r.MaxRunners = *repo.Spec.MaxRunners
	}
	if ref := repo.Spec.GitHubConfigSecret; ref != nil {
		r.GitHubConfigSecret = types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	}
	if repo.Spec.NixStorageSize != nil {
		r.NixStorageSize = *repo.Spec.NixStorageSize
	}
	return r
}

// DefaultNamespace is the namespace a Repository named name gets when it sets
// none: the prefix and the name, with the dots and underscores a namespace
// cannot hold replaced by dashes. It matches the arc-runner-scale-set chart
// in the-cluster, so a migrated scale set keeps its namespace.
//
// A result longer than a namespace can be is cut short and given a hash of
// the full name, so two long names that share a beginning stay apart.
func DefaultNamespace(prefix, name string) string {
	ns := prefix + strings.NewReplacer(".", "-", "_", "-").Replace(name)
	if len(ns) <= maxNamespaceLength {
		return ns
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])[:8]
	return strings.TrimRight(ns[:maxNamespaceLength-len(hash)-1], "-") + "-" + hash
}

const maxNamespaceLength = 63

// podTemplate is the runner pod for a scale set that brings no template of
// its own: a runner with docker from a dind sidecar, and a volume at /nix so
// nix builds stay off the container's overlay layer.
//
// It is the template the-cluster's arc-runners scale sets run, and the
// comments there explain each piece. The short version: dind runs as a native
// sidecar, the work volume is shared between it and the runner, init-nix hands
// the nix volume to the runner because a CSI volume comes up setgid and root
// owned, and fsGroup 1001 makes the runner the group owner of both volumes.
func (d Defaults) podTemplate(r resolved, serviceAccount string) corev1.PodTemplateSpec {
	ephemeral := func(size resource.Quantity) *corev1.EphemeralVolumeSource {
		return &corev1.EphemeralVolumeSource{
			VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: ptr.To(d.StorageClass),
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: size},
					},
				},
			},
		}
	}
	resources := func(cpu, memory, limit string) corev1.ResourceRequirements {
		return corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(limit)},
		}
	}

	runnerEnv := []corev1.EnvVar{
		{Name: "DOCKER_HOST", Value: "unix:///var/run/docker.sock"},
		{Name: "RUNNER_WAIT_FOR_DOCKER_IN_SECONDS", Value: "120"},
		// actions/setup-dotnet installs to /usr/share/dotnet unless told
		// otherwise, which the runner user cannot write.
		{Name: "DOTNET_INSTALL_DIR", Value: "/home/runner/.dotnet"},
	}
	if d.RunnerNixConfig != "" {
		runnerEnv = append([]corev1.EnvVar{{Name: "NIX_CONFIG", Value: d.RunnerNixConfig}}, runnerEnv...)
	}
	runnerResources := resources("1", "2Gi", "8Gi")
	runnerResources.Limits[corev1.ResourceCPU] = resource.MustParse("8")

	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			ServiceAccountName: serviceAccount,
			RestartPolicy:      corev1.RestartPolicyNever,
			SecurityContext:    &corev1.PodSecurityContext{FSGroup: ptr.To[int64](1001)},
			InitContainers: []corev1.Container{
				{
					Name:         "init-dind-externals",
					Image:        d.RunnerImage,
					Command:      []string{"cp", "-r", "/home/runner/externals/.", "/home/runner/tmpDir/"},
					Resources:    resources("10m", "32Mi", "1Gi"),
					VolumeMounts: []corev1.VolumeMount{{Name: "dind-externals", MountPath: "/home/runner/tmpDir"}},
				},
				{
					Name:  "init-nix",
					Image: d.RunnerImage,
					// Five digits, because a numeric chmod of four or fewer
					// keeps a directory's setgid bit.
					Command:         []string{"sh", "-c", "chown runner:runner /nix && chmod 00755 /nix"},
					SecurityContext: &corev1.SecurityContext{RunAsUser: ptr.To[int64](0)},
					Resources:       resources("10m", "32Mi", "64Mi"),
					VolumeMounts:    []corev1.VolumeMount{{Name: "nix", MountPath: "/nix"}},
				},
				{
					Name:          "dind",
					Image:         d.DindImage,
					Args:          []string{"dockerd", "--host=unix:///var/run/docker.sock", "--group=$(DOCKER_GROUP_GID)"},
					Env:           []corev1.EnvVar{{Name: "DOCKER_GROUP_GID", Value: "123"}},
					RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
					SecurityContext: &corev1.SecurityContext{
						Privileged: ptr.To(true),
					},
					StartupProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{Command: []string{"docker", "info"}},
						},
						FailureThreshold: 24,
						PeriodSeconds:    5,
						TimeoutSeconds:   5,
					},
					Resources: resources("100m", "256Mi", "4Gi"),
					VolumeMounts: []corev1.VolumeMount{
						{Name: "work", MountPath: "/home/runner/_work"},
						{Name: "dind-sock", MountPath: "/var/run"},
						{Name: "dind-externals", MountPath: "/home/runner/externals"},
					},
				},
			},
			Containers: []corev1.Container{{
				Name:      "runner",
				Image:     d.RunnerImage,
				Command:   []string{"/home/runner/run.sh"},
				Env:       runnerEnv,
				Resources: runnerResources,
				VolumeMounts: []corev1.VolumeMount{
					{Name: "work", MountPath: "/home/runner/_work"},
					{Name: "dind-sock", MountPath: "/var/run"},
					{Name: "nix", MountPath: "/nix"},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "dind-sock", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "dind-externals", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "work", VolumeSource: corev1.VolumeSource{Ephemeral: ephemeral(d.WorkSize)}},
				{Name: "nix", VolumeSource: corev1.VolumeSource{Ephemeral: ephemeral(r.NixStorageSize)}},
			},
		},
	}
}
