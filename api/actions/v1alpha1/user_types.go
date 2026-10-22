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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// UserSpec defines the desired state of User: which of a GitHub user's
// repositories get a Repository, and what those Repositories look like.
type UserSpec struct {
	// Login of the GitHub user, for example UnstoppableMango.
	// +kubebuilder:validation:MaxLength=39
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?$`
	Login string `json:"login"`

	// Repositories are names of the user's repositories that get runners
	// whether or not discovery finds them. Include, Exclude, and the archived
	// and fork switches do not apply to them.
	// +listType=set
	// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9._-]+$`
	// +optional
	Repositories []string `json:"repositories,omitempty"`

	// Discover adds every repository the user owns that the credentials can
	// see: the repositories a GitHub App installation was granted, or those a
	// token's owner owns.
	// +optional
	Discover bool `json:"discover,omitempty"`

	// Include limits discovered repositories to those whose name matches one
	// of these globs, compared without regard to case. Empty includes all.
	// +listType=set
	// +optional
	Include []string `json:"include,omitempty"`

	// Exclude drops discovered repositories whose name matches one of these
	// globs, compared without regard to case. It wins over Include.
	// +listType=set
	// +optional
	Exclude []string `json:"exclude,omitempty"`

	// IncludeArchived keeps archived repositories that discovery finds.
	// +optional
	IncludeArchived bool `json:"includeArchived,omitempty"`

	// IncludeForks keeps forks that discovery finds.
	// +optional
	IncludeForks bool `json:"includeForks,omitempty"`

	// Interval is how often discovery runs again. Defaults to 10m.
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// Template is applied to every Repository the User generates. Its
	// GitHubConfigSecret is also what discovery authenticates with.
	// +kubebuilder:validation:XValidation:rule="!has(self.minRunners) || !has(self.maxRunners) || self.minRunners <= self.maxRunners",message="minRunners must not exceed maxRunners"
	// +optional
	Template RepositoryTemplate `json:"template,omitzero"`
}

// UserStatus defines the observed state of User.
type UserStatus struct {
	// Repositories are the names of the Repositories the User generated.
	// +listType=set
	// +optional
	Repositories []string `json:"repositories,omitempty"`

	// Conflicts are names of Repositories the User wanted but did not take
	// over, because something else created them first.
	// +listType=set
	// +optional
	Conflicts []string `json:"conflicts,omitempty"`

	// LastDiscoveryTime is when discovery last succeeded.
	// +optional
	LastDiscoveryTime *metav1.Time `json:"lastDiscoveryTime,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Login",type=string,JSONPath=`.spec.login`
// +kubebuilder:printcolumn:name="Discover",type=boolean,JSONPath=`.spec.discover`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// User is a GitHub user whose repositories get runner scale sets. It
// generates a Repository for each repository it selects and deletes the
// Repositories it generated once their repository is no longer selected.
type User struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of User
	// +required
	Spec UserSpec `json:"spec"`

	// status defines the observed state of User
	// +optional
	Status UserStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// UserList contains a list of User
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []User `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &User{}, &UserList{})
		return nil
	})
}
