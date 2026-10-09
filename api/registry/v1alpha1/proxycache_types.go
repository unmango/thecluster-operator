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

// UpstreamType names the kind of registry a ProxyCache pulls from. It decides
// how the backend authenticates and pages through the upstream.
// +kubebuilder:validation:Enum=docker-hub;github-ghcr;quay;docker-registry
type UpstreamType string

const (
	UpstreamDockerHub      UpstreamType = "docker-hub"
	UpstreamGitHubGHCR     UpstreamType = "github-ghcr"
	UpstreamQuay           UpstreamType = "quay"
	UpstreamDockerRegistry UpstreamType = "docker-registry"
)

// Upstream is the registry a ProxyCache pulls through to.
type Upstream struct {
	// Type of the upstream registry. Harbor cannot change it in place, so
	// changing it means deleting and recreating the ProxyCache.
	Type UpstreamType `json:"type"`

	// URL of the upstream registry, for example https://hub.docker.com for
	// Docker Hub or https://registry.k8s.io.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// CredentialsSecretRef names a Secret in the ProxyCache's namespace with
	// username and password keys, for upstreams that rate limit anonymous
	// pulls. Omitted, the cache pulls anonymously.
	// +optional
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`
}

// ProxyCacheSpec defines the desired state of ProxyCache.
type ProxyCacheSpec struct {
	// RegistryRef names the Registry, in the same namespace, that serves
	// this cache.
	RegistryRef corev1.LocalObjectReference `json:"registryRef"`

	// Upstream is the registry being cached.
	Upstream Upstream `json:"upstream"`

	// Public lets clients pull without credentials, which is what node
	// container runtimes need.
	// +kubebuilder:default=true
	// +optional
	Public *bool `json:"public,omitempty"`
}

// ProxyCacheStatus defines the observed state of ProxyCache.
type ProxyCacheStatus struct {
	// Endpoint is the prefix clients pull through, for example
	// harbor.example.com/dockerhub for docker.io images.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Upstream",type=string,JSONPath=`.spec.upstream.url`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProxyCache is a pull-through cache of an upstream registry. Its name is
// the path prefix it is served under, so for Harbor it becomes both the
// registry endpoint and the proxy-cache project. Those names are global to
// the Registry, so two ProxyCaches with one name in different namespaces
// would fight over them.
type ProxyCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxyCacheSpec   `json:"spec,omitempty"`
	Status ProxyCacheStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProxyCacheList contains a list of ProxyCache.
type ProxyCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProxyCache `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ProxyCache{}, &ProxyCacheList{})
		return nil
	})
}
