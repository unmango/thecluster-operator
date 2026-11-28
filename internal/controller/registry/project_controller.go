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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
)

// ProjectFinalizer holds a Project until its Harbor project is deleted.
const ProjectFinalizer = "project.registry.thecluster.io/finalizer"

type ProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=registry.thecluster.io,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=projects/finalizers,verbs=update

// Reconcile makes Harbor hold a private project named after the Project.
// Settings changed in Harbor are put back on the next resync.
func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	p := &registryv1alpha1.Project{}
	if err := r.Get(ctx, req.NamespacedName, p); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !p.DeletionTimestamp.IsZero() {
		done, err := r.finalize(ctx, p)
		if done {
			return ctrl.Result{}, err
		}
		return r.report(ctx, p, ctrl.Result{}, err)
	}

	if controllerutil.AddFinalizer(p, ProjectFinalizer) {
		if err := r.Update(ctx, p); err != nil {
			return ctrl.Result{}, err
		}
	}
	result, err := r.ensure(ctx, p)
	return r.report(ctx, p, result, err)
}

// report records the outcome of a reconcile on the Ready condition.
func (r *ProjectReconciler) report(ctx context.Context, p *registryv1alpha1.Project, result ctrl.Result, err error) (ctrl.Result, error) {
	result = outcome(ctx, &p.Status.Conditions, p.Generation, result, err,
		fmt.Sprintf("Harbor holds private project %s", p.Name))
	return result, r.Status().Update(ctx, p)
}

func (r *ProjectReconciler) ensure(ctx context.Context, p *registryv1alpha1.Project) (ctrl.Result, error) {
	reg, hc, err := r.harbor(ctx, p)
	if err != nil {
		return ctrl.Result{}, err
	}

	project, err := hc.GetProject(ctx, p.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	switch {
	case project == nil:
		// A project deleted in Harbor took its images with it, so
		// recreating it loses nothing more.
		if err := hc.CreateProject(ctx, p.Name, false); err != nil {
			return ctrl.Result{}, err
		}
		if project, err = hc.GetProject(ctx, p.Name); err != nil {
			return ctrl.Result{}, err
		}
		if project == nil {
			return ctrl.Result{}, fmt.Errorf("project %s missing after create", p.Name)
		}
		p.Status.ProjectID = project.ProjectID
	case project.ProjectID != p.Status.ProjectID:
		return ctrl.Result{}, errConflict{fmt.Sprintf(
			"Harbor project %s exists and was not created by this Project", p.Name)}
	case project.Metadata["public"] != "false":
		if err := hc.SetProjectPublic(ctx, p.Name, false); err != nil {
			return ctrl.Result{}, err
		}
	}

	p.Status.Endpoint = ""
	if reg.Status.Host != "" {
		p.Status.Endpoint = reg.Status.Host + "/" + p.Name
	}
	return ctrl.Result{RequeueAfter: resync}, nil
}

// finalize deletes the Harbor project once it is empty. Unlike a cache, a
// project holds what was pushed to it, so the images are never deleted
// with it: deletion waits, reporting why, until someone empties it.
func (r *ProjectReconciler) finalize(ctx context.Context, p *registryv1alpha1.Project) (bool, error) {
	if !controllerutil.ContainsFinalizer(p, ProjectFinalizer) {
		return true, nil
	}

	_, hc, err := r.harbor(ctx, p)
	switch {
	case apierrors.IsNotFound(err):
		// With the Registry gone there is no way to reach Harbor, and
		// holding the Project forever would block namespace deletion.
		logf.FromContext(ctx).Info("Registry is gone, leaving Harbor as it is", "registry", p.Spec.RegistryRef.Name)
	case err != nil:
		return false, err
	default:
		project, err := hc.GetProject(ctx, p.Name)
		if err != nil {
			return false, err
		}
		if project != nil && project.ProjectID == p.Status.ProjectID {
			if err := hc.DeleteEmptyProject(ctx, p.Name); err != nil {
				return false, fmt.Errorf("deleting Harbor project %s, which must be emptied first: %w", p.Name, err)
			}
		}
	}

	controllerutil.RemoveFinalizer(p, ProjectFinalizer)
	return true, r.Update(ctx, p)
}

func (r *ProjectReconciler) harbor(ctx context.Context, p *registryv1alpha1.Project) (*registryv1alpha1.Registry, *harbor.Client, error) {
	reg := &registryv1alpha1.Registry{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: p.Spec.RegistryRef.Name}, reg); err != nil {
		return nil, nil, fmt.Errorf("reading registry %s: %w", p.Spec.RegistryRef.Name, err)
	}
	hc, err := harborFor(ctx, r.Client, reg)
	return reg, hc, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&registryv1alpha1.Project{}).
		Named("registry-project").
		Complete(r)
}
