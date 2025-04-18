/*
Copyright 2025 UnstoppableMango.

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
)

type WireguardClientConfigValue struct {
	Value           string                       `json:"value,omitempty"`
	ConfigMapKeyRef *corev1.ConfigMapKeySelector `json:"configMapKeyRef,omitempty"`
	SecretKeyRef    *corev1.SecretKeySelector    `json:"secretKeyRef,omitempty"`
}

// WireguardConfigSpec defines the desired state of WireguardConfig.
type WireguardConfigSpec struct {
	Username WireguardClientConfigValue `json:"username"`
	Password WireguardClientConfigValue `json:"password"`
}

// WireguardConfigStatus defines the observed state of WireguardConfig.
type WireguardConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// WireguardConfig is the Schema for the wireguardconfigs API.
type WireguardConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WireguardConfigSpec   `json:"spec,omitempty"`
	Status WireguardConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WireguardConfigList contains a list of WireguardConfig.
type WireguardConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WireguardConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WireguardConfig{}, &WireguardConfigList{})
}
