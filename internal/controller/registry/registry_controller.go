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

package registry

import (
	"context"
	"net/url"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
)

// RegistryReconciler reconciles a Registry object
type RegistryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registries/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile checks that the backend answers with the configured credentials
// and records the host clients pull from. A Registry owns nothing in the
// backend, so it needs no finalizer.
func (r *RegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	reg := &registryv1alpha1.Registry{}
	if err := r.Get(ctx, req.NamespacedName, reg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	result, err := r.check(ctx, reg)
	if err != nil {
		log.Error(err, "Registry is not ready")
		setReady(&reg.Status.Conditions, reg.Generation, metav1.ConditionFalse, "Unavailable", err.Error())
	} else {
		setReady(&reg.Status.Conditions, reg.Generation, metav1.ConditionTrue, "Available", "Harbor answered with the configured credentials")
	}
	if err := r.Status().Update(ctx, reg); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

func (r *RegistryReconciler) check(ctx context.Context, reg *registryv1alpha1.Registry) (ctrl.Result, error) {
	hc, err := harborFor(ctx, r.Client, reg)
	if err != nil {
		return ctrl.Result{RequeueAfter: retry}, err
	}
	if err := hc.Ping(ctx); err != nil {
		return ctrl.Result{RequeueAfter: retry}, err
	}
	info, err := hc.SystemInfo(ctx)
	if err != nil {
		return ctrl.Result{RequeueAfter: retry}, err
	}
	reg.Status.Host = info.RegistryURL
	if reg.Status.Host == "" {
		if u, err := url.Parse(info.ExternalURL); err == nil {
			reg.Status.Host = u.Host
		}
	}
	return ctrl.Result{RequeueAfter: resync}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&registryv1alpha1.Registry{}).
		Named("registry-registry").
		Complete(r)
}
