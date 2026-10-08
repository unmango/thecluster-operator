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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor/harbortest"
)

// newRegistry creates a password Secret and a Registry pointing at fake.
func newRegistry(ctx context.Context, name string, fake *harbortest.Server) *registryv1alpha1.Registry {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-admin", Namespace: "default"},
		StringData: map[string]string{"password": "secret"},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	DeferCleanup(func(ctx context.Context) {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
	})

	reg := &registryv1alpha1.Registry{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: registryv1alpha1.RegistrySpec{
			Harbor: &registryv1alpha1.HarborSpec{
				URL: fake.URL,
				PasswordSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
					Key:                  "password",
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, reg)).To(Succeed())
	DeferCleanup(func(ctx context.Context) {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, reg))).To(Succeed())
	})
	return reg
}

var _ = Describe("Registry Controller", func() {
	var fake *harbortest.Server

	BeforeEach(func() {
		fake = harbortest.New("secret")
		DeferCleanup(fake.Close)
	})

	reconcileRegistry := func(ctx context.Context, reg *registryv1alpha1.Registry) *registryv1alpha1.Registry {
		r := &RegistryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reg)})
		Expect(err).NotTo(HaveOccurred())
		out := &registryv1alpha1.Registry{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(reg), out)).To(Succeed())
		return out
	}

	It("defaults the username to admin", func(ctx SpecContext) {
		reg := newRegistry(ctx, "defaults", fake)
		Expect(reg.Spec.Harbor.Username).To(Equal("admin"))
	})

	It("reports Ready and the pull host when Harbor accepts the credentials", func(ctx SpecContext) {
		reg := reconcileRegistry(ctx, newRegistry(ctx, "ready", fake))

		Expect(meta.IsStatusConditionTrue(reg.Status.Conditions, TypeReady)).To(BeTrue())
		Expect(reg.Status.Host).To(Equal("harbor.example.com"))
	})

	It("reports not Ready when Harbor rejects the password", func(ctx SpecContext) {
		fake.Password = "rotated"
		reg := reconcileRegistry(ctx, newRegistry(ctx, "rejected", fake))

		cond := meta.FindStatusCondition(reg.Status.Conditions, TypeReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring("401"))
	})
})
