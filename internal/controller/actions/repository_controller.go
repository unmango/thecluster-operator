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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	actionsv1alpha1 "github.com/unmango/thecluster-operator/api/actions/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/arc"
)

const (
	// RepositoryFinalizer holds a Repository until its AutoscalingRunnerSet
	// is gone, so the actions-runner-controller can deregister the scale set
	// and clean up its runners before the namespace goes.
	RepositoryFinalizer = "repository.actions.thecluster.io/finalizer"

	// TypeReady is the condition a Repository reports.
	TypeReady = "Ready"

	fieldOwner = client.FieldOwner("thecluster-operator")
	partOf     = "gha-rs"
	managedBy  = "thecluster-operator"
)

// RepositoryReconciler reconciles a Repository object
type RepositoryReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Defaults Defaults
}

// +kubebuilder:rbac:groups=actions.thecluster.io,resources=repositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.thecluster.io,resources=repositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.thecluster.io,resources=repositories/finalizers,verbs=update
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalingrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
//
// The manager Role grants the actions-runner-controller these in each scale
// set's namespace, and Kubernetes only lets the operator grant what it holds.
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;create;delete
// +kubebuilder:rbac:groups="",resources=pods/status,verbs=get

// Reconcile renders a Repository as the objects the gha-runner-scale-set chart
// would: a namespace, a copy of the GitHub credentials, a ServiceAccount with
// no permissions for the runners, a Role and RoleBinding for the controller,
// and the AutoscalingRunnerSet itself.
func (r *RepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	repo := &actionsv1alpha1.Repository{}
	if err := r.Get(ctx, req.NamespacedName, repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !repo.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, repo)
	}

	if controllerutil.AddFinalizer(repo, RepositoryFinalizer) {
		if err := r.Update(ctx, repo); err != nil {
			return ctrl.Result{}, err
		}
	}

	err := r.apply(ctx, repo)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Repository is not ready")
		var reason *notReady
		if errors.As(err, &reason) {
			setReady(repo, metav1.ConditionFalse, reason.reason, reason.Error())
			err = nil
		} else {
			setReady(repo, metav1.ConditionFalse, "Error", err.Error())
		}
	}
	if serr := r.Status().Update(ctx, repo); serr != nil {
		return ctrl.Result{}, errors.Join(err, serr)
	}
	return ctrl.Result{}, err
}

// notReady is a state the Repository reports and waits out rather than
// retrying with backoff.
type notReady struct {
	reason string
	msg    string
}

func (e *notReady) Error() string { return e.msg }

func (r *RepositoryReconciler) apply(ctx context.Context, repo *actionsv1alpha1.Repository) error {
	if err := r.Defaults.Validate(); err != nil {
		return &notReady{"Misconfigured", err.Error()}
	}

	res := r.Defaults.resolve(repo)
	// A namespace recorded in status wins over a recomputed default, so a
	// change to the operator's prefix does not orphan a running scale set.
	if repo.Status.Namespace != "" {
		res.Namespace = repo.Status.Namespace
	}
	names := namesFor(res)

	if err := r.applyNamespace(ctx, repo, res.Namespace); err != nil {
		return err
	}
	repo.Status.Namespace = res.Namespace

	if err := r.copySecret(ctx, repo, res); err != nil {
		return err
	}
	if err := r.applyRBAC(ctx, repo, res, names); err != nil {
		return err
	}

	ars, err := r.applyScaleSet(ctx, repo, res, names)
	if err != nil {
		return err
	}

	repo.Status.Phase = ars.Status.Phase
	switch {
	case ars.Status.Phase == "Running" && ars.Status.ObservedGeneration >= ars.Generation:
		setReady(repo, metav1.ConditionTrue, "Running", "The scale set's listener is running")
		return nil
	case ars.Status.Phase == "":
		return &notReady{"Pending", "The actions-runner-controller has not picked up the scale set yet"}
	default:
		return &notReady{ars.Status.Phase, fmt.Sprintf("The scale set is %s", ars.Status.Phase)}
	}
}

// names are the objects a scale set's chart release would name after it.
type names struct {
	ScaleSet     string
	ManagerRole  string
	NoPermission string
}

func namesFor(res resolved) names {
	base := strings.ReplaceAll(res.RunnerScaleSetName, "_", "-")
	full := strings.TrimSuffix(truncate(base+"-gha-rs", 63), "-")
	return names{
		ScaleSet:     base,
		ManagerRole:  full + "-manager",
		NoPermission: full + "-no-permission",
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (r *RepositoryReconciler) owner(repo *actionsv1alpha1.Repository) *metav1ac.OwnerReferenceApplyConfiguration {
	return metav1ac.OwnerReference().
		WithAPIVersion(actionsv1alpha1.SchemeGroupVersion.String()).
		WithKind("Repository").
		WithName(repo.Name).
		WithUID(repo.UID).
		WithController(true).
		WithBlockOwnerDeletion(true)
}

func (r *RepositoryReconciler) labels(res resolved, component string) map[string]string {
	l := map[string]string{
		"app.kubernetes.io/name":       res.RunnerScaleSetName,
		"app.kubernetes.io/instance":   res.RunnerScaleSetName,
		"app.kubernetes.io/managed-by": managedBy,
		arc.LabelPartOf:                partOf,
		arc.LabelVersion:               r.Defaults.ControllerVersion,
		arc.LabelScaleSetName:          res.RunnerScaleSetName,
		arc.LabelScaleSetNamespace:     res.Namespace,
	}
	if component != "" {
		l[arc.LabelComponent] = component
	}
	return l
}

// applyNamespace creates the scale set's namespace, or takes it over only if
// this Repository already owns it. A namespace someone else made is left
// alone, because deleting the Repository deletes its namespace.
func (r *RepositoryReconciler) applyNamespace(ctx context.Context, repo *actionsv1alpha1.Repository, name string) error {
	existing := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: name}, existing)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	case !metav1.IsControlledBy(existing, repo):
		return &notReady{"NamespaceInUse", fmt.Sprintf("Namespace %s exists and is not owned by this Repository", name)}
	}

	ns := corev1ac.Namespace(name).
		WithLabels(map[string]string{"app.kubernetes.io/managed-by": managedBy}).
		WithOwnerReferences(r.owner(repo))
	return r.Apply(ctx, ns, fieldOwner, client.ForceOwnership)
}

// copySecret copies the GitHub credentials into the scale set's namespace,
// since an AutoscalingRunnerSet names its Secret without a namespace.
func (r *RepositoryReconciler) copySecret(ctx context.Context, repo *actionsv1alpha1.Repository, res resolved) error {
	if res.GitHubConfigSecret.Name == "" {
		return &notReady{"NoCredentials", "No githubConfigSecret is set and the operator has no default"}
	}
	src := &corev1.Secret{}
	if err := r.Get(ctx, res.GitHubConfigSecret, src); err != nil {
		if apierrors.IsNotFound(err) {
			return &notReady{"NoCredentials", fmt.Sprintf("Secret %s not found", res.GitHubConfigSecret)}
		}
		return err
	}

	dst := corev1ac.Secret(res.GitHubConfigSecret.Name, res.Namespace).
		WithLabels(r.labels(res, "github-secret")).
		WithOwnerReferences(r.owner(repo)).
		WithType(src.Type).
		WithData(src.Data)
	return r.Apply(ctx, dst, fieldOwner, client.ForceOwnership)
}

// applyRBAC gives the runners a ServiceAccount with no permissions, and binds
// the controller to a Role that lets it manage runner pods and their
// credentials. Both carry the controller's cleanup-protection finalizer, which
// it removes once it has torn down the scale set, so neither disappears while
// it still needs them.
func (r *RepositoryReconciler) applyRBAC(ctx context.Context, repo *actionsv1alpha1.Repository, res resolved, n names) error {
	sa := corev1ac.ServiceAccount(n.NoPermission, res.Namespace).
		WithLabels(r.labels(res, "")).
		WithOwnerReferences(r.owner(repo)).
		WithFinalizers(arc.FinalizerCleanupProtect)
	if err := r.Apply(ctx, sa, fieldOwner, client.ForceOwnership); err != nil {
		return err
	}

	crud := []string{"create", "delete", "get", "list", "patch", "update"}
	role := rbacv1ac.Role(n.ManagerRole, res.Namespace).
		WithLabels(r.labels(res, "manager-role")).
		WithOwnerReferences(r.owner(repo)).
		WithFinalizers(arc.FinalizerCleanupProtect).
		WithRules(
			rbacv1ac.PolicyRule().WithAPIGroups("").WithResources("pods").WithVerbs("create", "delete", "get"),
			rbacv1ac.PolicyRule().WithAPIGroups("").WithResources("pods/status").WithVerbs("get"),
			rbacv1ac.PolicyRule().WithAPIGroups("").WithResources("secrets").WithVerbs(crud...),
			rbacv1ac.PolicyRule().WithAPIGroups("").WithResources("serviceaccounts").WithVerbs(crud...),
			rbacv1ac.PolicyRule().WithAPIGroups("rbac.authorization.k8s.io").WithResources("rolebindings").WithVerbs("create", "delete", "get", "patch", "update"),
			rbacv1ac.PolicyRule().WithAPIGroups("rbac.authorization.k8s.io").WithResources("roles").WithVerbs("create", "delete", "get", "patch", "update"),
		)
	if err := r.Apply(ctx, role, fieldOwner, client.ForceOwnership); err != nil {
		return err
	}

	sva := r.Defaults.ControllerServiceAccount
	rb := rbacv1ac.RoleBinding(n.ManagerRole, res.Namespace).
		WithLabels(r.labels(res, "manager-role-binding")).
		WithOwnerReferences(r.owner(repo)).
		WithFinalizers(arc.FinalizerCleanupProtect).
		WithRoleRef(rbacv1ac.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("Role").WithName(n.ManagerRole)).
		WithSubjects(rbacv1ac.Subject().WithKind("ServiceAccount").WithName(sva.Name).WithNamespace(sva.Namespace))
	return r.Apply(ctx, rb, fieldOwner, client.ForceOwnership)
}

func (r *RepositoryReconciler) applyScaleSet(ctx context.Context, repo *actionsv1alpha1.Repository, res resolved, n names) (*arc.AutoscalingRunnerSet, error) {
	template := r.Defaults.podTemplate(res, n.NoPermission)
	if repo.Spec.Template != nil {
		template = *repo.Spec.Template.DeepCopy()
		if template.Spec.ServiceAccountName == "" {
			template.Spec.ServiceAccountName = n.NoPermission
		}
		if template.Spec.RestartPolicy == "" {
			template.Spec.RestartPolicy = corev1.RestartPolicyNever
		}
	}
	listener := repo.Spec.ListenerTemplate
	if listener == nil {
		listener = defaultListenerTemplate()
	}

	ars := &arc.AutoscalingRunnerSet{
		TypeMeta: metav1.TypeMeta{APIVersion: arc.SchemeGroupVersion.String(), Kind: "AutoscalingRunnerSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      n.ScaleSet,
			Namespace: res.Namespace,
			Labels:    r.labels(res, "autoscaling-runner-set"),
			Annotations: map[string]string{
				arc.AnnotationManagerRole:  n.ManagerRole,
				arc.AnnotationManagerRB:    n.ManagerRole,
				arc.AnnotationNoPermission: n.NoPermission,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         actionsv1alpha1.SchemeGroupVersion.String(),
				Kind:               "Repository",
				Name:               repo.Name,
				UID:                repo.UID,
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			}},
		},
		Spec: arc.AutoscalingRunnerSetSpec{
			GitHubConfigUrl:    strings.TrimSuffix(repo.Spec.URL, "/"),
			GitHubConfigSecret: res.GitHubConfigSecret.Name,
			RunnerGroup:        repo.Spec.RunnerGroup,
			RunnerScaleSetName: res.RunnerScaleSetName,
			Template:           template,
			ListenerTemplate:   listener,
			MaxRunners:         ptr.To(int(res.MaxRunners)),
		},
	}
	if repo.Spec.MinRunners != nil {
		ars.Spec.MinRunners = ptr.To(int(*repo.Spec.MinRunners))
	}

	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ars)
	if err != nil {
		return nil, err
	}
	// The converter writes the zero time that metav1.Time marshals to as
	// null. Applied, a null asks the server to clear the field, which is not
	// this operator's to own.
	unstructured.RemoveNestedField(u, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(u, "spec", "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(u, "spec", "listenerTemplate", "metadata", "creationTimestamp")
	delete(u, "status")

	obj := &unstructured.Unstructured{Object: u}
	if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), fieldOwner, client.ForceOwnership); err != nil {
		return nil, err
	}

	applied := &arc.AutoscalingRunnerSet{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(ars), applied); err != nil {
		return nil, err
	}
	return applied, nil
}

// defaultListenerTemplate sizes the listener, which upstream leaves without
// resources. It measures a couple of millicores and a few MiB at rest.
func defaultListenerTemplate() *corev1.PodTemplateSpec {
	return &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "listener",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2m"),
						corev1.ResourceMemory: resource.MustParse("16Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("25m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
			}},
		},
	}
}

// finalize deletes the AutoscalingRunnerSet and waits for the controller to
// finish with it, then deletes the namespace. Deleting the namespace first
// would race the controller's cleanup of runners it registered with GitHub.
func (r *RepositoryReconciler) finalize(ctx context.Context, repo *actionsv1alpha1.Repository) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(repo, RepositoryFinalizer) {
		return ctrl.Result{}, nil
	}

	if ns := repo.Status.Namespace; ns != "" {
		list := &arc.AutoscalingRunnerSetList{}
		if err := r.List(ctx, list, client.InNamespace(ns)); err != nil && !meta.IsNoMatchError(err) {
			return ctrl.Result{}, err
		}
		pending := false
		for i := range list.Items {
			ars := &list.Items[i]
			if !metav1.IsControlledBy(ars, repo) {
				continue
			}
			pending = true
			if ars.DeletionTimestamp.IsZero() {
				if err := r.Delete(ctx, ars); client.IgnoreNotFound(err) != nil {
					return ctrl.Result{}, err
				}
			}
		}
		if pending {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		existing := &corev1.Namespace{}
		err := r.Get(ctx, types.NamespacedName{Name: ns}, existing)
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return ctrl.Result{}, err
		case metav1.IsControlledBy(existing, repo) && existing.DeletionTimestamp.IsZero():
			if err := r.Delete(ctx, existing); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}
	}

	controllerutil.RemoveFinalizer(repo, RepositoryFinalizer)
	return ctrl.Result{}, r.Update(ctx, repo)
}

func setReady(repo *actionsv1alpha1.Repository, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&repo.Status.Conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             status,
		ObservedGeneration: repo.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *RepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&actionsv1alpha1.Repository{}).
		Owns(&arc.AutoscalingRunnerSet{}).
		Owns(&corev1.Namespace{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.forSecret)).
		Named("actions-repository").
		Complete(r)
}

// forSecret maps a GitHub credentials Secret to the Repositories that copy
// it, so a rotated key reaches every scale set.
func (r *RepositoryReconciler) forSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &actionsv1alpha1.RepositoryList{}
	if err := r.List(ctx, list); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Repositories for a Secret")
		return nil
	}
	key := client.ObjectKeyFromObject(obj)
	var reqs []reconcile.Request
	for i := range list.Items {
		if r.Defaults.resolve(&list.Items[i]).GitHubConfigSecret == key {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
		}
	}
	return reqs
}
