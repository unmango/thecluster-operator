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

// ProjectSpec defines the desired state of Project.
type ProjectSpec struct {
	// RegistryRef names the Registry, in the same namespace, that holds
	// this project. It is immutable, since the recorded project and robot
	// ids mean nothing on another backend.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="registryRef is immutable"
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="name is required"
	RegistryRef corev1.LocalObjectReference `json:"registryRef"`
}

// ProjectStatus defines the observed state of Project.
type ProjectStatus struct {
	// ProjectID is the backend's id for the project this Project created.
	// A project with the same name and another id was made by someone else,
	// and is left alone.
	// +optional
	ProjectID int64 `json:"projectID,omitempty"`

	// Endpoint is the prefix clients push to and pull from, for example
	// harbor.example.com/mve.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Project is a private project in a Registry, which needs credentials to
// pull from or push to. Its name is the project's name in the backend, which
// is global to the Registry, so the first namespace to claim a name keeps it.
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   ProjectSpec   `json:"spec"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList contains a list of Project.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Project{}, &ProjectList{})
		return nil
	})
}
