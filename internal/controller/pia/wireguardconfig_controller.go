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

package pia

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	piav1alpha1 "github.com/unmango/thecluster-operator/api/pia/v1alpha1"
)

var (
	TypeAvailableWireguardConfig  = "Available"
	TypeErrorWireguardConfig      = "Error"
	TypeGeneratingWireguardConfig = "Generating"
	WireguardConfigFinalizer      = "wireguardconfig.pia.thecluster.io/finalizer"
)

// WireguardConfigReconciler reconciles a WireguardConfig object
type WireguardConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=pia.thecluster.io,resources=wireguardconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=pia.thecluster.io,resources=wireguardconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=pia.thecluster.io,resources=wireguardconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=pods;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete

func (r *WireguardConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	wg := &piav1alpha1.WireguardConfig{}
	if err := r.Get(ctx, req.NamespacedName, wg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if len(wg.Status.Conditions) == 0 {
		_ = meta.SetStatusCondition(&wg.Status.Conditions,
			metav1.Condition{
				Type:    TypeAvailableWireguardConfig,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			},
		)
		if err := r.Status().Update(ctx, wg); err != nil {
			log.Error(err, "Failed to update wireguard config status")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, wg); err != nil {
			log.Error(err, "Failed to re-fetch wireguard config")
			return ctrl.Result{}, err
		}
	}

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, cm); err == nil {
		_ = meta.SetStatusCondition(&wg.Status.Conditions,
			metav1.Condition{
				Type:    TypeAvailableWireguardConfig,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Config map exists",
			},
		)
		if err := r.Status().Update(ctx, wg); err != nil {
			log.Error(err, "Failed to update wireguard config status")
			return ctrl.Result{}, err
		} else {
			log.Info("Found existing config map, nothing to do")
			return ctrl.Result{}, nil
		}
	}

	podList := &corev1.PodList{}
	err := r.List(ctx, podList, client.MatchingLabels{
		"app.kubernetes.io/name":   "thecluster.operator",
		"pia.thecluster.io/config": wg.Name,
	})
	if err != nil {
		log.Error(err, "Failed to list pods matching config labels")
		return ctrl.Result{}, err
	}

	if len(podList.Items) == 0 {
		return r.createGenPod(ctx, wg)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WireguardConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&piav1alpha1.WireguardConfig{}).
		Named("pia-wireguardconfig").
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func (r *WireguardConfigReconciler) createGenPod(ctx context.Context, c *piav1alpha1.WireguardConfig) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !hasValue(c.Spec.Username) {
		_ = meta.SetStatusCondition(&c.Status.Conditions,
			metav1.Condition{
				Type:    TypeErrorWireguardConfig,
				Status:  metav1.ConditionTrue,
				Reason:  "Invalid",
				Message: "Configuration is missing username",
			},
		)
		if err := r.Status().Update(ctx, c); err != nil {
			log.Error(err, "Failed to update wireguard config status")
			return ctrl.Result{}, err
		} else {
			log.Info("Spec is missing username")
			return ctrl.Result{}, nil
		}
	}

	if !hasValue(c.Spec.Password) {
		_ = meta.SetStatusCondition(&c.Status.Conditions,
			metav1.Condition{
				Type:    TypeErrorWireguardConfig,
				Status:  metav1.ConditionTrue,
				Reason:  "Invalid",
				Message: "Configuration is missing password",
			},
		)
		if err := r.Status().Update(ctx, c); err != nil {
			log.Error(err, "Failed to update wireguard config status")
			return ctrl.Result{}, err
		} else {
			log.Info("Spec is missing password")
			return ctrl.Result{}, nil
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "generate-config-",
			Namespace:    c.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":   "thecluster-operator",
				"pia.thecluster.io/config": c.Name,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "generate-config",
					Image: "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0",
					Env: []corev1.EnvVar{
						getEnvVar("PIA_USER", c.Spec.Username),
						getEnvVar("PIA_PASS", c.Spec.Password),
						{Name: "PIA_PF", Value: "false"},
						{Name: "PIA_CONNECT", Value: "false"},
						{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
						{Name: "VPN_PROTOCOL", Value: "wireguard"},
						{Name: "DISABLE_IPV6", Value: "no"},
						{Name: "DIP_TOKEN", Value: "no"},
						{Name: "AUTOCONNECT", Value: "true"},
					},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "results",
						MountPath: "/out",
					}},
				},
				{
					Name:    "results",
					Image:   "busybox:latest",
					Command: []string{"sh", "-c", "sleep infinity"},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "results",
						ReadOnly:  true,
						MountPath: "/out",
					}},
				},
			},
			Volumes: []corev1.Volume{{
				Name: "results",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			}},
		},
	}

	if err := ctrl.SetControllerReference(c, pod, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, pod); err != nil {
		return ctrl.Result{}, err
	}

	_ = meta.SetStatusCondition(&c.Status.Conditions,
		metav1.Condition{
			Type:    TypeGeneratingWireguardConfig,
			Status:  metav1.ConditionTrue,
			Reason:  "Reconciling",
			Message: "Config generator script pod created",
		},
	)
	if err := r.Status().Update(ctx, c); err != nil {
		log.Error(err, "Failed to update wireguard config status")
		return ctrl.Result{}, err
	} else {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
}

func hasValue(config piav1alpha1.WireguardClientConfigValue) bool {
	if config.Value != "" {
		return true
	}
	if config.SecretKeyRef != nil {
		return true
	}
	if config.ConfigMapKeyRef != nil {
		return true
	}

	return false
}

func getEnvVar(name string, config piav1alpha1.WireguardClientConfigValue) corev1.EnvVar {
	env := corev1.EnvVar{
		Name:  name,
		Value: config.Value,
	}
	if ref := config.SecretKeyRef; ref != nil {
		env.ValueFrom = &corev1.EnvVarSource{
			SecretKeyRef: ref,
		}
	}
	if ref := config.ConfigMapKeyRef; ref != nil {
		env.ValueFrom = &corev1.EnvVarSource{
			ConfigMapKeyRef: ref,
		}
	}

	return env
}
