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

package v1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	piav1alpha1 "github.com/unmango/thecluster-operator/api/pia/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

var podlog = logf.Log.WithName("pod-resource")

// SetupPodWebhookWithManager registers the webhook for Pod in the manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&corev1.Pod{}).
		WithDefaulter(&PodCustomDefaulter{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpod-v1.kb.io,admissionReviewVersions=v1

type PodCustomDefaulter struct {
	client.Client
}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Pod.
func (d *PodCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected an Pod object but got %T", obj)
	}

	var configName string
	if configName, ok = pod.Annotations["pia.thecluster.io/config"]; !ok {
		podlog.Info("Ignoring Pod without matching annotation", "name", pod.GetName())
		return nil
	}

	podlog.Info("Applying config init container to Pod", "name", pod.GetName())

	config := &piav1alpha1.WireguardConfig{}
	if err := d.Get(ctx, types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      configName,
	}, config); err != nil {
		return fmt.Errorf("unable to look up wireguard config: %w", err)
	}

	pod.Spec.InitContainers = append(pod.Spec.InitContainers,
		corev1.Container{
			Name:  "generate-wireguard-config",
			Image: "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0",
			Env: []corev1.EnvVar{
				{Name: "PIA_USER", Value: config.Spec.Username.Value},
				{Name: "PIA_PASS", Value: config.Spec.Password.Value},
				{Name: "PIA_PF", Value: "false"},
				{Name: "PIA_CONNECT", Value: "false"},
				{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
				{Name: "VPN_PROTOCOL", Value: "wireguard"},
				{Name: "DISABLE_IPV6", Value: "no"},
				{Name: "DIP_TOKEN", Value: "no"},
				{Name: "AUTOCONNECT", Value: "true"},
			},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "config",
				MountPath: "/config",
			}},
		},
	)
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "config",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	return nil
}
