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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	actionsv1alpha1 "github.com/unmango/thecluster-operator/api/actions/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/arc"
)

var _ = Describe("Repository Controller", func() {
	var (
		name       string
		repo       *actionsv1alpha1.Repository
		reconciler *RepositoryReconciler
		secretKey  = types.NamespacedName{Namespace: "default", Name: "gh"}
	)

	testDefaults := func() Defaults {
		return Defaults{
			ControllerVersion:        "0.15.0",
			ControllerServiceAccount: types.NamespacedName{Namespace: "arc-system", Name: "arc-gha-rs-controller"},
			GitHubConfigSecret:       secretKey,
			NamespacePrefix:          "arc-",
			RunnerScaleSetName:       "thecluster",
			MaxRunners:               6,
			RunnerImage:              "ghcr.io/unmango/actions-runner:test",
			DindImage:                "docker:dind",
			StorageClass:             "fast-rwo",
			NixStorageSize:           resource.MustParse("20Gi"),
			WorkSize:                 resource.MustParse("5Gi"),
		}
	}

	doReconcile := func() (reconcile.Result, error) {
		return reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
	}

	ready := func() *metav1.Condition {
		GinkgoHelper()
		got := &actionsv1alpha1.Repository{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		return meta.FindStatusCondition(got.Status.Conditions, TypeReady)
	}

	scaleSet := func() *arc.AutoscalingRunnerSet {
		GinkgoHelper()
		ars := &arc.AutoscalingRunnerSet{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster"}, ars)).To(Succeed())
		return ars
	}

	BeforeEach(func() {
		// Namespaces never finish deleting in envtest, so every test gets a
		// fresh name.
		name = "repo-" + utilrand.String(6)
		reconciler = &RepositoryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Defaults: testDefaults()}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: secretKey.Namespace, Name: secretKey.Name},
			StringData: map[string]string{"github_token": "test"},
		}
		if err := k8sClient.Create(ctx, secret); err != nil {
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
		}

		repo = &actionsv1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       actionsv1alpha1.RepositorySpec{URL: "https://github.com/UnstoppableMango/" + name},
		}
	})

	JustBeforeEach(func() {
		Expect(k8sClient.Create(ctx, repo)).To(Succeed())
	})

	AfterEach(func() {
		got := &actionsv1alpha1.Repository{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, got); err != nil {
			return
		}
		got.Finalizers = nil
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, got))).To(Succeed())
	})

	It("reports a misconfigured operator", func() {
		reconciler.Defaults.RunnerImage = ""

		_, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())

		cond := ready()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("Misconfigured"))
	})

	It("reports missing credentials", func() {
		reconciler.Defaults.GitHubConfigSecret = types.NamespacedName{Namespace: "default", Name: "missing"}

		_, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())
		Expect(ready().Reason).To(Equal("NoCredentials"))
	})

	It("renders the scale set and everything it needs", func() {
		_, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())

		got := &actionsv1alpha1.Repository{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		Expect(got.Finalizers).To(ContainElement(RepositoryFinalizer))
		Expect(got.Status.Namespace).To(Equal("arc-" + name))

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "arc-" + name}, ns)).To(Succeed())
		Expect(metav1.IsControlledBy(ns, got)).To(BeTrue())

		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "gh"}, secret)).To(Succeed())
		Expect(secret.Data).To(HaveKeyWithValue("github_token", []byte("test")))

		sa := &corev1.ServiceAccount{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster-gha-rs-no-permission"}, sa)).To(Succeed())
		Expect(sa.Finalizers).To(ContainElement(arc.FinalizerCleanupProtect))

		role := &rbacv1.Role{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster-gha-rs-manager"}, role)).To(Succeed())
		Expect(role.Finalizers).To(ContainElement(arc.FinalizerCleanupProtect))

		rb := &rbacv1.RoleBinding{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster-gha-rs-manager"}, rb)).To(Succeed())
		Expect(rb.Subjects).To(ConsistOf(rbacv1.Subject{Kind: "ServiceAccount", Name: "arc-gha-rs-controller", Namespace: "arc-system"}))

		ars := scaleSet()
		Expect(metav1.IsControlledBy(ars, got)).To(BeTrue())
		Expect(ars.Labels).To(HaveKeyWithValue(arc.LabelVersion, "0.15.0"))
		Expect(ars.Annotations).To(HaveKeyWithValue(arc.AnnotationManagerRole, "thecluster-gha-rs-manager"))
		Expect(ars.Annotations).To(HaveKeyWithValue(arc.AnnotationNoPermission, "thecluster-gha-rs-no-permission"))
		Expect(ars.Spec.GitHubConfigUrl).To(Equal(repo.Spec.URL))
		Expect(ars.Spec.GitHubConfigSecret).To(Equal("gh"))
		Expect(ars.Spec.MaxRunners).To(Equal(ptr.To(6)))
		Expect(ars.Spec.Template.Spec.ServiceAccountName).To(Equal("thecluster-gha-rs-no-permission"))
		Expect(ars.Spec.Template.Spec.Containers).To(ContainElement(HaveField("Name", "runner")))
		Expect(ars.Spec.Template.Spec.InitContainers).To(ContainElement(HaveField("Name", "dind")))
		Expect(ars.Spec.ListenerTemplate).NotTo(BeNil())

		Expect(ready().Reason).To(Equal("Pending"))
	})

	It("is ready once the listener runs", func() {
		_, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())

		ars := scaleSet()
		ars.Status = arc.AutoscalingRunnerSetStatus{Phase: "Running", ObservedGeneration: ars.Generation}
		Expect(k8sClient.Status().Update(ctx, ars)).To(Succeed())

		_, err = doReconcile()
		Expect(err).NotTo(HaveOccurred())
		Expect(ready().Status).To(Equal(metav1.ConditionTrue))
	})

	When("the spec overrides the defaults", func() {
		BeforeEach(func() {
			repo.Spec.MaxRunners = ptr.To[int32](2)
			repo.Spec.MinRunners = ptr.To[int32](1)
			repo.Spec.Template = &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "custom"}}},
			}
		})

		It("uses the overrides", func() {
			_, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())

			ars := scaleSet()
			Expect(ars.Spec.MaxRunners).To(Equal(ptr.To(2)))
			Expect(ars.Spec.MinRunners).To(Equal(ptr.To(1)))
			Expect(ars.Spec.Template.Spec.Containers).To(ConsistOf(HaveField("Image", "custom")))
			Expect(ars.Spec.Template.Spec.InitContainers).To(BeEmpty())
			Expect(ars.Spec.Template.Spec.ServiceAccountName).To(Equal("thecluster-gha-rs-no-permission"))
			Expect(ars.Spec.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		})
	})

	When("the namespace belongs to someone else", func() {
		BeforeEach(func() {
			Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "arc-" + name}})).To(Succeed())
		})

		It("leaves it alone", func() {
			_, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())
			Expect(ready().Reason).To(Equal("NamespaceInUse"))

			ars := &arc.AutoscalingRunnerSetList{}
			Expect(k8sClient.List(ctx, ars, client.InNamespace("arc-"+name))).To(Succeed())
			Expect(ars.Items).To(BeEmpty())
		})
	})

	It("rejects a namespace that is not a DNS label", func() {
		bad := repo.DeepCopy()
		bad.Name = name + "-bad"
		bad.Spec.Namespace = "Invalid_Name"
		Expect(k8sClient.Create(ctx, bad)).NotTo(Succeed())
	})

	It("replaces the scale set when its name changes", func() {
		_, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())

		got := &actionsv1alpha1.Repository{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		got.Spec.RunnerScaleSetName = "renamed"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		_, err = doReconcile()
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster"}, &arc.AutoscalingRunnerSet{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "renamed"}, &arc.AutoscalingRunnerSet{})).To(Succeed())
		Expect(ready().Reason).To(Equal("Replacing"))

		_, err = doReconcile()
		Expect(err).NotTo(HaveOccurred())

		// The old RBAC is deleted, and waits on the controller's finalizer.
		sa := &corev1.ServiceAccount{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster-gha-rs-no-permission"}, sa)).To(Succeed())
		Expect(sa.DeletionTimestamp).NotTo(BeNil())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "renamed-gha-rs-no-permission"}, sa)).To(Succeed())
		Expect(sa.DeletionTimestamp).To(BeNil())
		Expect(ready().Reason).To(Equal("Pending"))
	})

	It("tears the scale set down before the namespace", func() {
		_, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Delete(ctx, repo)).To(Succeed())

		result, err := doReconcile()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "arc-" + name}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp).To(BeNil())

		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: "arc-" + name, Name: "thecluster"}, &arc.AutoscalingRunnerSet{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		_, err = doReconcile()
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "arc-" + name}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp).NotTo(BeNil())

		err = k8sClient.Get(ctx, types.NamespacedName{Name: name}, &actionsv1alpha1.Repository{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})

var _ = Describe("DefaultNamespace", func() {
	It("replaces characters a namespace cannot hold", func() {
		Expect(DefaultNamespace("arc-", "unmango.github_io")).To(Equal("arc-unmango-github-io"))
	})

	It("keeps long names within a namespace's length and apart", func() {
		long := strings.Repeat("a", 70)
		a, b := DefaultNamespace("arc-", long+"-one"), DefaultNamespace("arc-", long+"-two")
		Expect(len(a)).To(BeNumerically("<=", 63))
		Expect(a).NotTo(Equal(b))
	})
})
