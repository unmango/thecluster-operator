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
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
)

// ProxyCacheFinalizer holds a ProxyCache until its Harbor project and
// registry endpoint are deleted.
const ProxyCacheFinalizer = "proxycache.registry.thecluster.io/finalizer"

// errConflict is a state in Harbor the controller will not overwrite. It is
// reported on the condition and not retried until the spec changes or the
// next resync.
type errConflict struct{ msg string }

func (e errConflict) Error() string { return e.msg }

// ProxyCacheReconciler reconciles a ProxyCache object
type ProxyCacheReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=registry.thecluster.io,resources=proxycaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=proxycaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=proxycaches/finalizers,verbs=update
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registries,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile makes Harbor hold a registry endpoint for the upstream and a
// proxy-cache project in front of it, both named after the ProxyCache.
// Settings changed in Harbor are put back on the next resync.
func (r *ProxyCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pc := &registryv1alpha1.ProxyCache{}
	if err := r.Get(ctx, req.NamespacedName, pc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !pc.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, pc)
	}

	if controllerutil.AddFinalizer(pc, ProxyCacheFinalizer) {
		if err := r.Update(ctx, pc); err != nil {
			return ctrl.Result{}, err
		}
	}

	result, err := r.ensure(ctx, pc)
	var conflict errConflict
	switch {
	case errors.As(err, &conflict):
		setReady(&pc.Status.Conditions, pc.Generation, metav1.ConditionFalse, "Conflict", err.Error())
		result, err = ctrl.Result{RequeueAfter: resync}, nil
	case err != nil:
		log.Error(err, "ProxyCache is not ready")
		setReady(&pc.Status.Conditions, pc.Generation, metav1.ConditionFalse, "Unavailable", err.Error())
		result, err = ctrl.Result{RequeueAfter: retry}, nil
	default:
		setReady(&pc.Status.Conditions, pc.Generation, metav1.ConditionTrue, "Available",
			fmt.Sprintf("Harbor proxies %s as project %s", pc.Spec.Upstream.URL, pc.Name))
	}
	if err := r.Status().Update(ctx, pc); err != nil {
		return ctrl.Result{}, err
	}
	return result, err
}

func (r *ProxyCacheReconciler) ensure(ctx context.Context, pc *registryv1alpha1.ProxyCache) (ctrl.Result, error) {
	reg := &registryv1alpha1.Registry{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: pc.Namespace, Name: pc.Spec.RegistryRef.Name}, reg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reading registry %s: %w", pc.Spec.RegistryRef.Name, err)
	}
	hc, err := harborFor(ctx, r.Client, reg)
	if err != nil {
		return ctrl.Result{}, err
	}

	upstream := pc.Spec.Upstream
	var cred *harbor.Credential
	if ref := upstream.CredentialsSecretRef; ref != nil {
		username, err := secretValue(ctx, r.Client, pc.Namespace, ref.Name, "username")
		if err != nil {
			return ctrl.Result{}, err
		}
		password, err := secretValue(ctx, r.Client, pc.Namespace, ref.Name, "password")
		if err != nil {
			return ctrl.Result{}, err
		}
		cred = &harbor.Credential{Type: "basic", AccessKey: username, AccessSecret: password}
	}

	endpoint, err := hc.GetRegistry(ctx, pc.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	switch {
	case endpoint == nil:
		err = hc.CreateRegistry(ctx, &harbor.Registry{
			Name:       pc.Name,
			Type:       string(upstream.Type),
			URL:        upstream.URL,
			Credential: cred,
		})
		if err == nil {
			endpoint, err = hc.GetRegistry(ctx, pc.Name)
		}
		if err == nil && endpoint == nil {
			err = fmt.Errorf("registry endpoint %s missing after create", pc.Name)
		}
	case endpoint.Type != string(upstream.Type):
		return ctrl.Result{}, errConflict{fmt.Sprintf(
			"Harbor registry endpoint %s is type %s, not %s, and Harbor cannot change it in place",
			pc.Name, endpoint.Type, upstream.Type)}
	default:
		// The secret is masked on read, so the credential is always sent.
		err = hc.UpdateRegistry(ctx, endpoint.ID, upstream.URL, false, cred)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	public := pc.Spec.Public == nil || *pc.Spec.Public
	project, err := hc.GetProject(ctx, pc.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	switch {
	case project == nil:
		err = hc.CreateProxyProject(ctx, pc.Name, endpoint.ID, public)
	case project.RegistryID != endpoint.ID:
		return ctrl.Result{}, errConflict{fmt.Sprintf(
			"Harbor project %s exists and does not proxy registry endpoint %s", pc.Name, pc.Name)}
	case project.Metadata["public"] != fmt.Sprint(public):
		err = hc.SetProjectPublic(ctx, pc.Name, public)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	pc.Status.Endpoint = ""
	if reg.Status.Host != "" {
		pc.Status.Endpoint = reg.Status.Host + "/" + pc.Name
	}
	return ctrl.Result{RequeueAfter: resync}, nil
}

// finalize deletes the project, its cached repositories and then the
// registry endpoint. A cache holds nothing that cannot be pulled again.
func (r *ProxyCacheReconciler) finalize(ctx context.Context, pc *registryv1alpha1.ProxyCache) error {
	if !controllerutil.ContainsFinalizer(pc, ProxyCacheFinalizer) {
		return nil
	}

	reg := &registryv1alpha1.Registry{}
	err := r.Get(ctx, client.ObjectKey{Namespace: pc.Namespace, Name: pc.Spec.RegistryRef.Name}, reg)
	switch {
	case apierrors.IsNotFound(err):
		// With the Registry gone there is no way to reach Harbor, and
		// holding the ProxyCache forever would block namespace deletion.
		logf.FromContext(ctx).Info("Registry is gone, leaving Harbor as it is", "registry", pc.Spec.RegistryRef.Name)
	case err != nil:
		return err
	default:
		hc, err := harborFor(ctx, r.Client, reg)
		if err != nil {
			return err
		}
		if err := hc.DeleteProject(ctx, pc.Name); err != nil {
			return err
		}
		endpoint, err := hc.GetRegistry(ctx, pc.Name)
		if err != nil {
			return err
		}
		if endpoint != nil && endpoint.Type == string(pc.Spec.Upstream.Type) {
			if err := hc.DeleteRegistry(ctx, endpoint.ID); err != nil && !harbor.IsNotFound(err) {
				return err
			}
		}
	}

	controllerutil.RemoveFinalizer(pc, ProxyCacheFinalizer)
	return r.Update(ctx, pc)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProxyCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&registryv1alpha1.ProxyCache{}).
		Named("registry-proxycache").
		Complete(r)
}
