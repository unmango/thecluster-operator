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

// Package arc holds the slice of actions-runner-controller's
// actions.github.com/v1alpha1 API that this operator writes and reads.
//
// The upstream module is not imported: its only semver tags belong to the
// legacy controller, and its go.mod moves ahead of this one's Go version.
// Every field here keeps upstream's JSON name, and the operator only ever
// server-side applies the fields it sets, so whatever else the CRD carries is
// left to the controller.
// +kubebuilder:object:generate=true
// +kubebuilder:skip
package arc

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// SchemeGroupVersion is the actions-runner-controller API this package
	// mirrors.
	SchemeGroupVersion = schema.GroupVersion{Group: "actions.github.com", Version: "v1alpha1"}

	// SchemeBuilder registers the mirrored types.
	SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &AutoscalingRunnerSet{}, &AutoscalingRunnerSetList{})
		metav1.AddToGroupVersion(s, SchemeGroupVersion)
		return nil
	})

	// AddToScheme adds the mirrored types to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Labels and annotations the controller reads off an AutoscalingRunnerSet.
const (
	// LabelVersion must carry the controller's major and minor version. The
	// controller deletes an AutoscalingRunnerSet whose label does not match.
	LabelVersion            = "app.kubernetes.io/version"
	LabelPartOf             = "app.kubernetes.io/part-of"
	LabelComponent          = "app.kubernetes.io/component"
	LabelScaleSetName       = "actions.github.com/scale-set-name"
	LabelScaleSetNamespace  = "actions.github.com/scale-set-namespace"
	AnnotationManagerRole   = "actions.github.com/cleanup-manager-role-name"
	AnnotationManagerRB     = "actions.github.com/cleanup-manager-role-binding"
	AnnotationNoPermission  = "actions.github.com/cleanup-no-permission-service-account-name"
	AnnotationGitHubSecret  = "actions.github.com/cleanup-github-secret-name"
	FinalizerCleanupProtect = "actions.github.com/cleanup-protection"
)

// AutoscalingRunnerSetSpec is the part of upstream's spec this operator sets.
type AutoscalingRunnerSetSpec struct {
	GitHubConfigUrl    string `json:"githubConfigUrl,omitempty"`
	GitHubConfigSecret string `json:"githubConfigSecret,omitempty"`
	RunnerGroup        string `json:"runnerGroup,omitempty"`
	RunnerScaleSetName string `json:"runnerScaleSetName,omitempty"`

	Template         corev1.PodTemplateSpec  `json:"template,omitempty"`
	ListenerTemplate *corev1.PodTemplateSpec `json:"listenerTemplate,omitempty"`

	MaxRunners *int `json:"maxRunners,omitempty"`
	MinRunners *int `json:"minRunners,omitempty"`
}

// AutoscalingRunnerSetStatus is the part of upstream's status this operator
// reads.
type AutoscalingRunnerSetStatus struct {
	Phase              string `json:"phase,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true

// AutoscalingRunnerSet mirrors actions.github.com/v1alpha1
// AutoscalingRunnerSet.
type AutoscalingRunnerSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AutoscalingRunnerSetSpec   `json:"spec,omitempty"`
	Status AutoscalingRunnerSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AutoscalingRunnerSetList mirrors actions.github.com/v1alpha1
// AutoscalingRunnerSetList.
type AutoscalingRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutoscalingRunnerSet `json:"items"`
}
