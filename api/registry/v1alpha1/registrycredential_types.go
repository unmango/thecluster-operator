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

// Access is what a RegistryCredential may do in its project. Neither level
// can delete.
// +kubebuilder:validation:Enum=pull;push
type Access string

const (
	AccessPull Access = "pull"
	AccessPush Access = "push"
)

// RegistryCredentialSpec defines the desired state of RegistryCredential.
// It is immutable: a different login is a different RegistryCredential.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type RegistryCredentialSpec struct {
	// ProjectRef names the Project, in the same namespace, the credential
	// reaches. A credential cannot reach a project in another namespace.
	ProjectRef corev1.LocalObjectReference `json:"projectRef"`

	// Access is pull, or push, which can also pull.
	Access Access `json:"access"`

	// SecretName names the secret the operator writes in its secret store,
	// holding the registry host, username and password as JSON. The stored
	// name is prefixed with the namespace, as <namespace>--<secretName>,
	// so one namespace cannot overwrite another's. A secretName holds no
	// "--" and neither starts nor ends with "-", which keeps that split
	// unambiguous. It is lowercase because Key Vault compares names without
	// case, and at 62 characters the whole name fits Key Vault's 127.
	// +kubebuilder:validation:Pattern=`^[0-9a-z]+(-[0-9a-z]+)*$`
	// +kubebuilder:validation:MaxLength=62
	SecretName string `json:"secretName"`
}

// Robot is a backend login issued for a RegistryCredential.
type Robot struct {
	// ID is the backend's id for the robot.
	ID int64 `json:"id"`

	// Name is the username the robot logs in with.
	Name string `json:"name"`

	// ExpiresAt is when the backend stops accepting the robot.
	ExpiresAt metav1.Time `json:"expiresAt"`

	// Written is when the robot's secret last reached the secret store.
	// +optional
	Written *metav1.Time `json:"written,omitempty"`
}

// RegistryCredentialStatus defines the observed state of RegistryCredential.
type RegistryCredentialStatus struct {
	// Robot is the login the secret store currently holds.
	// +optional
	Robot *Robot `json:"robot,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Access",type=string,JSONPath=`.spec.access`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.robot.expiresAt`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RegistryCredential is a request for a login to one Project. The operator
// issues a robot scoped to that project and writes its secret into the
// secret store, where programs read it; nothing else hands it out.
type RegistryCredential struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   RegistryCredentialSpec   `json:"spec"`
	Status RegistryCredentialStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RegistryCredentialList contains a list of RegistryCredential.
type RegistryCredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RegistryCredential `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RegistryCredential{}, &RegistryCredentialList{})
		return nil
	})
}
