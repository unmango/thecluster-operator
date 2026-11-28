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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
	"github.com/unmango/thecluster-operator/internal/secretstore"
)

// RegistryCredentialFinalizer holds a RegistryCredential until its robot
// and stored secret are deleted.
const RegistryCredentialFinalizer = "registrycredential.registry.thecluster.io/finalizer"

// robotLifetime is how long Harbor accepts a robot, per ADR 0006.
const robotLifetime = 90 * 24 * time.Hour

type RegistryCredentialReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Store receives each robot's secret. Without one, credentials report
	// that they cannot be issued.
	Store secretstore.Writer
	Clock clock.PassiveClock
}

// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registrycredentials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registrycredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=registrycredentials/finalizers,verbs=update
// +kubebuilder:rbac:groups=registry.thecluster.io,resources=projects,verbs=get;list;watch

// Reconcile issues a robot scoped to the credential's Project and writes its
// secret into the secret store.
func (r *RegistryCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rc := &registryv1alpha1.RegistryCredential{}
	if err := r.Get(ctx, req.NamespacedName, rc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rc.DeletionTimestamp.IsZero() {
		if err := r.finalize(ctx, rc); err != nil {
			return r.report(ctx, rc, ctrl.Result{}, err)
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(rc, RegistryCredentialFinalizer) {
		if err := r.Update(ctx, rc); err != nil {
			return ctrl.Result{}, err
		}
	}
	result, err := r.ensure(ctx, rc)
	return r.report(ctx, rc, result, err)
}

func (r *RegistryCredentialReconciler) report(ctx context.Context, rc *registryv1alpha1.RegistryCredential, result ctrl.Result, err error) (ctrl.Result, error) {
	result = outcome(ctx, &rc.Status.Conditions, rc.Generation, result, err,
		fmt.Sprintf("Secret %s holds a %s login to project %s", vaultName(rc), rc.Spec.Access, rc.Spec.ProjectRef.Name))
	return result, r.Status().Update(ctx, rc)
}

func (r *RegistryCredentialReconciler) ensure(ctx context.Context, rc *registryv1alpha1.RegistryCredential) (ctrl.Result, error) {
	if r.Store == nil {
		return ctrl.Result{}, errors.New("the operator has no secret store; set --harbor-vault-url")
	}
	project, reg, hc, err := r.harbor(ctx, rc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if project.Status.ProjectID == 0 {
		return ctrl.Result{}, fmt.Errorf("project %s has not been created yet", project.Name)
	}
	if reg.Status.Host == "" {
		return ctrl.Result{}, fmt.Errorf("registry %s has no host yet", reg.Name)
	}

	robot := rc.Status.Robot
	if robot != nil {
		own, err := findOwn(ctx, hc, rc, project)
		if err != nil {
			return ctrl.Result{}, err
		}
		if own == nil {
			robot, rc.Status.Robot = nil, nil
		}
	}

	var secret string
	switch {
	case robot == nil:
		if robot, secret, err = r.createRobot(ctx, hc, rc, project); err != nil {
			return ctrl.Result{}, err
		}
		rc.Status.Robot = robot
	case robot.Written == nil:
		// Harbor shows a secret only once, so one that never reached the
		// store is replaced.
		if secret, err = hc.RefreshRobotSecret(ctx, robot.ID); err != nil {
			return ctrl.Result{}, err
		}
	}
	if secret == "" {
		return ctrl.Result{RequeueAfter: resync}, nil
	}

	value, err := json.Marshal(map[string]string{"registry": reg.Status.Host, "username": robot.Name, "password": secret})
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Store.Set(ctx, vaultName(rc), string(value)); err != nil {
		return ctrl.Result{}, fmt.Errorf("writing secret %s: %w", vaultName(rc), err)
	}
	robot.Written = &metav1.Time{Time: r.Clock.Now()}
	return ctrl.Result{RequeueAfter: resync}, nil
}

// wantRobot is the robot a credential is issued. Its description carries the
// credential's UID, which marks it as this credential's own.
func wantRobot(rc *registryv1alpha1.RegistryCredential, project *registryv1alpha1.Project) *harbor.Robot {
	actions := []string{"pull"}
	if rc.Spec.Access == registryv1alpha1.AccessPush {
		actions = append(actions, "push")
	}
	want := &harbor.Robot{
		Name:        rc.Name + "-1",
		Level:       "project",
		Duration:    int64(robotLifetime / (24 * time.Hour)),
		Description: "registrycredential " + string(rc.UID),
		Permissions: []harbor.RobotPermission{{Kind: "project", Namespace: project.Name}},
	}
	for _, a := range actions {
		want.Permissions[0].Access = append(want.Permissions[0].Access, harbor.RobotAccess{Resource: "repository", Action: a})
	}
	return want
}

// findOwn returns the credential's robot if Harbor holds it, or nil. It looks
// up the recorded robot by ID, or one never recorded by name. A robot without
// the credential's mark and permissions counts as absent, so its secret is
// never handed out and it is never deleted, even after the backend changes.
func findOwn(ctx context.Context, hc *harbor.Client, rc *registryv1alpha1.RegistryCredential, project *registryv1alpha1.Project) (*harbor.Robot, error) {
	want := wantRobot(rc, project)
	var found *harbor.Robot
	var err error
	if rc.Status.Robot != nil {
		found, err = hc.GetRobot(ctx, rc.Status.Robot.ID)
	} else {
		found, err = hc.FindRobot(ctx, project.Status.ProjectID, want.Name)
	}
	if err != nil || found == nil || found.Description != want.Description || !reflect.DeepEqual(found.Permissions, want.Permissions) {
		return nil, err
	}
	return found, nil
}

// createRobot creates the credential's robot, or takes back one an earlier
// reconcile created without recording it, replacing its unknown secret.
func (r *RegistryCredentialReconciler) createRobot(ctx context.Context, hc *harbor.Client, rc *registryv1alpha1.RegistryCredential, project *registryv1alpha1.Project) (*registryv1alpha1.Robot, string, error) {
	want := wantRobot(rc, project)
	created, err := hc.CreateRobot(ctx, want)
	if harbor.IsConflict(err) {
		if created, err = findOwn(ctx, hc, rc, project); err == nil && created == nil {
			err = fmt.Errorf("robot %s exists but was not issued to this credential", want.Name)
		}
		if err == nil {
			created.Secret, err = hc.RefreshRobotSecret(ctx, created.ID)
		}
	}
	if err != nil {
		return nil, "", err
	}
	return &registryv1alpha1.Robot{
		ID:        created.ID,
		Name:      created.Name,
		ExpiresAt: metav1.Time{Time: r.Clock.Now().Add(robotLifetime)},
	}, created.Secret, nil
}

// finalize deletes the robot and the stored secret.
func (r *RegistryCredentialReconciler) finalize(ctx context.Context, rc *registryv1alpha1.RegistryCredential) error {
	if !controllerutil.ContainsFinalizer(rc, RegistryCredentialFinalizer) {
		return nil
	}

	project, _, hc, err := r.harbor(ctx, rc)
	switch {
	case errors.Is(err, errGone):
		// With the Project or Registry gone there is no way to reach
		// Harbor; any robot still expires on its own.
		logf.FromContext(ctx).Info("Cannot reach Harbor, leaving the robot to expire")
	case err != nil:
		return err
	case rc.Status.Robot != nil || project.Status.ProjectID != 0:
		own, err := findOwn(ctx, hc, rc, project)
		if err == nil && own != nil {
			err = hc.DeleteRobot(ctx, own.ID)
		}
		if err != nil {
			return err
		}
	}
	switch {
	case r.Store != nil:
		if err := r.Store.Delete(ctx, vaultName(rc)); err != nil {
			return fmt.Errorf("deleting secret %s: %w", vaultName(rc), err)
		}
	case rc.Status.Robot != nil && rc.Status.Robot.Written != nil:
		return fmt.Errorf("no secret store to delete %s from", vaultName(rc))
	}

	controllerutil.RemoveFinalizer(rc, RegistryCredentialFinalizer)
	return r.Update(ctx, rc)
}

// vaultName is the credential's secret name in the store, prefixed with its
// namespace so that no namespace can overwrite or delete another's secret.
func vaultName(rc *registryv1alpha1.RegistryCredential) string {
	return rc.Namespace + "--" + rc.Name
}

func (r *RegistryCredentialReconciler) harbor(ctx context.Context, rc *registryv1alpha1.RegistryCredential) (*registryv1alpha1.Project, *registryv1alpha1.Registry, *harbor.Client, error) {
	project := &registryv1alpha1.Project{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: rc.Namespace, Name: rc.Spec.ProjectRef.Name}, project); err != nil {
		return nil, nil, nil, fmt.Errorf("reading project %s: %w", rc.Spec.ProjectRef.Name, gone(err))
	}
	reg := &registryv1alpha1.Registry{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: rc.Namespace, Name: project.Spec.RegistryRef.Name}, reg); err != nil {
		return nil, nil, nil, fmt.Errorf("reading registry %s: %w", project.Spec.RegistryRef.Name, gone(err))
	}
	hc, err := harborFor(ctx, r.Client, reg)
	return project, reg, hc, err
}

// errGone marks a Project or Registry that no longer exists, so the robot
// it held can only be left to expire.
var errGone = errors.New("gone")

func gone(err error) error {
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: %w", errGone, err)
	}
	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *RegistryCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = clock.RealClock{}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&registryv1alpha1.RegistryCredential{}).
		Named("registry-registrycredential").
		Complete(r)
}
