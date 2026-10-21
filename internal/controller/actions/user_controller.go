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

package actions

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gogithub "github.com/google/go-github/v93/github"
	actionsv1alpha1 "github.com/unmango/thecluster-operator/api/actions/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/github"
)

const (
	// UserLabel names the User that generated a Repository.
	UserLabel = "actions.thecluster.io/user"

	defaultInterval = 10 * time.Minute
	retryInterval   = time.Minute
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Defaults Defaults

	// GitHubURL is where repositories live, for the URL of a Repository the
	// User lists by name. GitHubAPIURL is what discovery calls.
	GitHubURL    string
	GitHubAPIURL string
}

// +kubebuilder:rbac:groups=actions.thecluster.io,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=actions.thecluster.io,resources=users/status,verbs=get;update;patch

// Reconcile generates a Repository for each repository the User selects, and
// deletes the Repositories it generated for repositories it no longer
// selects. A Repository someone else created is left alone, even when the
// User selects its repository. Deleting the User deletes its Repositories
// through their owner references.
func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	user := &actionsv1alpha1.User{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !user.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	result := ctrl.Result{}
	if user.Spec.Discover {
		result.RequeueAfter = defaultInterval
		if i := user.Spec.Interval; i != nil && i.Duration > 0 {
			result.RequeueAfter = i.Duration
		}
	}

	err := r.sync(ctx, user)
	if err != nil {
		logf.FromContext(ctx).Error(err, "User is not ready")
		var reason *notReady
		if errors.As(err, &reason) {
			setUserReady(user, metav1.ConditionFalse, reason.reason, reason.Error())
			result.RequeueAfter = retryInterval
			err = nil
		} else {
			setUserReady(user, metav1.ConditionFalse, "Error", err.Error())
		}
	}
	if serr := r.Status().Update(ctx, user); serr != nil {
		return ctrl.Result{}, errors.Join(err, serr)
	}
	return result, err
}

func (r *UserReconciler) sync(ctx context.Context, user *actionsv1alpha1.User) error {
	want, err := r.selected(ctx, user)
	if err != nil {
		return err
	}

	owned := &actionsv1alpha1.RepositoryList{}
	if err := r.List(ctx, owned, client.MatchingLabels{UserLabel: user.Name}); err != nil {
		return err
	}

	var made, conflicts []string
	for name, url := range want {
		ok, err := r.applyRepository(ctx, user, name, url)
		if err != nil {
			return err
		}
		if ok {
			made = append(made, name)
		} else {
			conflicts = append(conflicts, name)
		}
	}

	for i := range owned.Items {
		repo := &owned.Items[i]
		if _, keep := want[repo.Name]; keep || !metav1.IsControlledBy(repo, user) || !repo.DeletionTimestamp.IsZero() {
			continue
		}
		if err := r.Delete(ctx, repo); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	slices.Sort(made)
	slices.Sort(conflicts)
	user.Status.Repositories = made
	user.Status.Conflicts = conflicts
	if len(conflicts) > 0 {
		setUserReady(user, metav1.ConditionTrue, "Conflicts", fmt.Sprintf(
			"Generated %d Repositories; left %s alone, since the User did not create them",
			len(made), strings.Join(conflicts, ", ")))
	} else {
		setUserReady(user, metav1.ConditionTrue, "Synced", fmt.Sprintf("Generated %d Repositories", len(made)))
	}
	return nil
}

// selected maps the name of each Repository the User wants to its URL.
func (r *UserReconciler) selected(ctx context.Context, user *actionsv1alpha1.User) (map[string]string, error) {
	for _, p := range slices.Concat(user.Spec.Include, user.Spec.Exclude) {
		if _, err := path.Match(p, ""); err != nil {
			return nil, &notReady{"InvalidFilter", fmt.Sprintf("%q is not a glob: %v", p, err)}
		}
	}

	want := map[string]string{}
	add := func(repo, url string) {
		if name := RepositoryName(repo); name != "" {
			want[name] = url
		}
	}

	if user.Spec.Discover {
		repos, err := r.discover(ctx, user)
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			if selects(user.Spec, repo) {
				add(repo.GetName(), repo.GetHTMLURL())
			}
		}
		user.Status.LastDiscoveryTime = &metav1.Time{Time: time.Now()}
	}
	base := strings.TrimSuffix(r.GitHubURL, "/")
	for _, repo := range user.Spec.Repositories {
		add(repo, base+"/"+user.Spec.Login+"/"+repo)
	}
	return want, nil
}

func (r *UserReconciler) discover(ctx context.Context, user *actionsv1alpha1.User) ([]*gogithub.Repository, error) {
	key := r.Defaults.GitHubConfigSecret
	if ref := user.Spec.Template.GitHubConfigSecret; ref != nil {
		key = types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
	}
	if key.Name == "" {
		return nil, &notReady{"NoCredentials", "No githubConfigSecret is set and the operator has no default"}
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &notReady{"NoCredentials", fmt.Sprintf("Secret %s not found", key)}
		}
		return nil, err
	}
	gh, err := github.FromSecret(r.GitHubAPIURL, secret.Data)
	if err != nil {
		return nil, &notReady{"NoCredentials", fmt.Sprintf("Secret %s: %v", key, err)}
	}
	repos, err := gh.Repositories(ctx)
	if err != nil {
		return nil, &notReady{"DiscoveryFailed", err.Error()}
	}
	return repos, nil
}

// selects reports whether discovery keeps repo.
func selects(spec actionsv1alpha1.UserSpec, repo *gogithub.Repository) bool {
	if !strings.EqualFold(repo.GetOwner().GetLogin(), spec.Login) {
		return false
	}
	if (repo.GetArchived() && !spec.IncludeArchived) || (repo.GetFork() && !spec.IncludeForks) {
		return false
	}
	name := strings.ToLower(repo.GetName())
	if len(spec.Include) > 0 && !matchAny(spec.Include, name) {
		return false
	}
	return !matchAny(spec.Exclude, name)
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), name); ok {
			return true
		}
	}
	return false
}

// RepositoryName is the name of the Repository a User generates for the
// repository called repo: lowercased, with dots and underscores replaced by
// dashes. It matches the scale set names in the-cluster's arc-runners, so a
// generated Repository lands in the namespace the chart used.
func RepositoryName(repo string) string {
	name := strings.NewReplacer(".", "-", "_", "-").Replace(strings.ToLower(repo))
	return strings.Trim(name, "-")
}

// applyRepository creates or updates the Repository called name, and reports
// false without touching it when the User does not control it.
func (r *UserReconciler) applyRepository(ctx context.Context, user *actionsv1alpha1.User, name, url string) (bool, error) {
	spec := actionsv1alpha1.RepositorySpec{URL: url, RepositoryTemplate: *user.Spec.Template.DeepCopy()}

	repo := &actionsv1alpha1.Repository{}
	err := r.Get(ctx, types.NamespacedName{Name: name}, repo)
	if apierrors.IsNotFound(err) {
		repo = &actionsv1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{UserLabel: user.Name}},
			Spec:       spec,
		}
		if err := controllerutil.SetControllerReference(user, repo, r.Scheme); err != nil {
			return false, err
		}
		return true, r.Create(ctx, repo)
	}
	if err != nil {
		return false, err
	}
	if !metav1.IsControlledBy(repo, user) {
		return false, nil
	}
	// The namespace is immutable and the template has none, so whatever the
	// Repository has stays put.
	spec.Namespace = repo.Spec.Namespace
	if equality.Semantic.DeepEqual(repo.Spec, spec) && repo.Labels[UserLabel] == user.Name {
		return true, nil
	}
	repo.Spec = spec
	if repo.Labels == nil {
		repo.Labels = map[string]string{}
	}
	repo.Labels[UserLabel] = user.Name
	return true, r.Update(ctx, repo)
}

func setUserReady(user *actionsv1alpha1.User, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&user.Status.Conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             status,
		ObservedGeneration: user.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&actionsv1alpha1.User{}).
		Owns(&actionsv1alpha1.Repository{}).
		Named("actions-user").
		Complete(r)
}
