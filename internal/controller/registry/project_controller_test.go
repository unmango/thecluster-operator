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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
	"github.com/unmango/thecluster-operator/internal/harbor/harbortest"
)

var _ = Describe("Project Controller", func() {
	var (
		fake *harbortest.Server
		reg  *registryv1alpha1.Registry
	)

	BeforeEach(func(ctx SpecContext) {
		fake = harbortest.New("secret")
		DeferCleanup(fake.Close)
		reg = newRegistry(ctx, "harbor", fake)
		r := &RegistryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reg)})
		Expect(err).NotTo(HaveOccurred())
	})

	newProject := func(ctx context.Context, name string) *registryv1alpha1.Project {
		p := &registryv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       registryv1alpha1.ProjectSpec{RegistryRef: corev1.LocalObjectReference{Name: reg.Name}},
		}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		DeferCleanup(func(ctx context.Context) {
			// Drop the finalizer so a failed spec cannot leave the object behind.
			current := &registryv1alpha1.Project{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(p), current); err == nil {
				current.Finalizers = nil
				Expect(k8sClient.Update(ctx, current)).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, current))).To(Succeed())
			}
		})
		return p
	}

	reconcileProject := func(ctx context.Context, p *registryv1alpha1.Project) *registryv1alpha1.Project {
		r := &ProjectReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(p)})
		Expect(err).NotTo(HaveOccurred())
		out := &registryv1alpha1.Project{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(p), out); apierrors.IsNotFound(err) {
			return nil
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		return out
	}

	readyReason := func(p *registryv1alpha1.Project) string {
		cond := meta.FindStatusCondition(p.Status.Conditions, TypeReady)
		Expect(cond).NotTo(BeNil())
		return cond.Reason
	}

	It("creates a private project", func(ctx SpecContext) {
		p := reconcileProject(ctx, newProject(ctx, "mve"))

		Expect(meta.IsStatusConditionTrue(p.Status.Conditions, TypeReady)).To(BeTrue())
		Expect(p.Status.Endpoint).To(Equal("harbor.example.com/mve"))
		Expect(p.Finalizers).To(ContainElement(ProjectFinalizer))

		project := fake.Project("mve")
		Expect(project).NotTo(BeNil())
		Expect(project.RegistryID).To(BeZero())
		Expect(project.Metadata).To(HaveKeyWithValue("public", "false"))
		Expect(p.Status.ProjectID).To(Equal(project.ProjectID))
	})

	It("puts back settings changed in Harbor", func(ctx SpecContext) {
		p := newProject(ctx, "mve")
		reconcileProject(ctx, p)

		fake.Mu.Lock()
		fake.Projects["mve"].Metadata["public"] = "true"
		fake.Mu.Unlock()

		reconcileProject(ctx, p)
		Expect(fake.Project("mve").Metadata).To(HaveKeyWithValue("public", "false"))
	})

	It("refuses a project it did not create, and leaves it on deletion", func(ctx SpecContext) {
		fake.Mu.Lock()
		fake.Projects["library"] = &harbor.Project{ProjectID: 99, Name: "library", Metadata: map[string]string{"public": "true"}}
		fake.Mu.Unlock()

		p := reconcileProject(ctx, newProject(ctx, "library"))
		Expect(readyReason(p)).To(Equal("Conflict"))
		Expect(fake.Project("library").Metadata).To(HaveKeyWithValue("public", "true"))

		Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		Expect(reconcileProject(ctx, p)).To(BeNil())
		Expect(fake.Project("library")).NotTo(BeNil())
	})

	It("reports a missing Registry", func(ctx SpecContext) {
		p := newProject(ctx, "orphan")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
		p.Spec.RegistryRef.Name = "missing"
		Expect(k8sClient.Update(ctx, p)).To(Succeed())

		p = reconcileProject(ctx, p)
		cond := meta.FindStatusCondition(p.Status.Conditions, TypeReady)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring("missing"))
	})

	It("deletes the project on deletion", func(ctx SpecContext) {
		p := newProject(ctx, "mve")
		reconcileProject(ctx, p)

		Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		Expect(reconcileProject(ctx, p)).To(BeNil())
		Expect(fake.Project("mve")).To(BeNil())
	})

	It("keeps pushed images, waiting for the project to be emptied", func(ctx SpecContext) {
		p := newProject(ctx, "mve")
		reconcileProject(ctx, p)

		fake.Mu.Lock()
		fake.Repositories["mve"] = []string{"mve/api"}
		fake.Mu.Unlock()

		Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		p = reconcileProject(ctx, p)
		Expect(p).NotTo(BeNil())
		Expect(readyReason(p)).To(Equal("Unavailable"))
		Expect(fake.Project("mve")).NotTo(BeNil())

		fake.Mu.Lock()
		fake.Repositories["mve"] = nil
		fake.Mu.Unlock()

		Expect(reconcileProject(ctx, p)).To(BeNil())
		Expect(fake.Project("mve")).To(BeNil())
	})
})
