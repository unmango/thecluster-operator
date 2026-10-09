/*
Copyright 2025.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// HarborSpec connects a Registry to a Harbor instance.
type HarborSpec struct {
	// URL is where the operator reaches Harbor's API, without the /api/v2.0 suffix.
	// An in-cluster Service address avoids depending on the Gateway and DNS.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// Username of a Harbor administrator. Creating registry endpoints needs a
	// system administrator, not a project role.
	// +kubebuilder:default=admin
	// +optional
	Username string `json:"username,omitempty"`

	// PasswordSecretRef selects the key holding that user's password, in a
	// Secret in the Registry's namespace.
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`
}

// RegistrySpec defines the desired state of Registry.
// Exactly one backend is set; Harbor is the only one so far.
type RegistrySpec struct {
	// Harbor backs this registry with a Harbor instance.
	Harbor *HarborSpec `json:"harbor"`
}

// RegistryStatus defines the observed state of Registry.
type RegistryStatus struct {
	// Host is the registry host clients pull from, as the backend reports it.
	// +optional
	Host string `json:"host,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.status.host`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Registry is a container registry the operator can configure, such as a
// Harbor instance. Other resources, like ProxyCache, name it to say where
// they are fulfilled.
type Registry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RegistrySpec   `json:"spec,omitempty"`
	Status RegistryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RegistryList contains a list of Registry.
type RegistryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Registry `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Registry{}, &RegistryList{})
		return nil
	})
}
