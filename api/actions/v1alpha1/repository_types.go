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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// SecretReference names a Secret in any namespace.
type SecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the Secret.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

// RepositorySpec defines the desired state of Repository. Everything but the
// URL is optional: an unset field takes the operator's default, and the
// defaults describe a scale set of dind runners with a nix store volume.
// +kubebuilder:validation:XValidation:rule="!has(self.minRunners) || !has(self.maxRunners) || self.minRunners <= self.maxRunners",message="minRunners must not exceed maxRunners"
type RepositorySpec struct {
	// URL of the repository the runners register to, for example
	// https://github.com/UnstoppableMango/dotfiles.
	// +kubebuilder:validation:Pattern=`^https://[^/]+/[^/]+/[^/]+/?$`
	URL string `json:"url"`

	// Namespace the scale set and its runners run in. The operator creates it
	// and deletes it with the Repository. Defaults to the operator's
	// namespace prefix followed by the Repository's name.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="namespace is immutable"
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^$|^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// RunnerScaleSetName is the name the scale set registers under, the label
	// a workflow's runs-on selects. Defaults to the operator's
	// --runner-scale-set-name.
	// +kubebuilder:validation:MaxLength=45
	// +optional
	RunnerScaleSetName string `json:"runnerScaleSetName,omitempty"`

	// RunnerGroup the scale set joins. Omitted, it joins the default group.
	// +optional
	RunnerGroup string `json:"runnerGroup,omitempty"`

	// MinRunners is how many idle runners to keep. Defaults to none.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinRunners *int32 `json:"minRunners,omitempty"`

	// MaxRunners caps how many runners run at once. Defaults to the
	// operator's --max-runners.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxRunners *int32 `json:"maxRunners,omitempty"`

	// GitHubConfigSecret is the Secret holding the GitHub App or token the
	// scale set authenticates with. The operator copies it into Namespace,
	// since the scale set can only read a Secret in its own namespace.
	// Defaults to the operator's --github-config-secret.
	// +optional
	GitHubConfigSecret *SecretReference `json:"githubConfigSecret,omitempty"`

	// NixStorageSize sizes the volume mounted at /nix in the default runner
	// template. Defaults to the operator's --nix-storage-size. Ignored when
	// Template is set.
	// +optional
	NixStorageSize *resource.Quantity `json:"nixStorageSize,omitempty"`

	// Template replaces the default runner pod template outright.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +optional
	Template *corev1.PodTemplateSpec `json:"template,omitempty"`

	// ListenerTemplate is passed to the AutoscalingRunnerSet as is.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +optional
	ListenerTemplate *corev1.PodTemplateSpec `json:"listenerTemplate,omitempty"`
}

// RepositoryStatus defines the observed state of Repository.
type RepositoryStatus struct {
	// Namespace the scale set runs in.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Phase of the AutoscalingRunnerSet: Pending until its listener starts,
	// Running once it does, and Outdated when the controller wants it
	// recreated.
	// +optional
	Phase string `json:"phase,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Repository is a GitHub Actions runner scale set for one repository. It
// renders an actions-runner-controller AutoscalingRunnerSet in a namespace of
// its own, along with the RBAC and credentials the scale set needs there.
type Repository struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Repository
	// +required
	Spec RepositorySpec `json:"spec"`

	// status defines the observed state of Repository
	// +optional
	Status RepositoryStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RepositoryList contains a list of Repository
type RepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Repository `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Repository{}, &RepositoryList{})
		return nil
	})
}
