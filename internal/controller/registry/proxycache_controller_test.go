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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
	"github.com/unmango/thecluster-operator/internal/harbor/harbortest"
)

var _ = Describe("ProxyCache Controller", func() {
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

	newProxyCache := func(ctx context.Context, name string, upstream registryv1alpha1.Upstream) *registryv1alpha1.ProxyCache {
		pc := &registryv1alpha1.ProxyCache{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: registryv1alpha1.ProxyCacheSpec{
				RegistryRef: corev1.LocalObjectReference{Name: reg.Name},
				Upstream:    upstream,
			},
		}
		Expect(k8sClient.Create(ctx, pc)).To(Succeed())
		DeferCleanup(func(ctx context.Context) {
			// Drop the finalizer so a failed spec cannot leave the object behind.
			current := &registryv1alpha1.ProxyCache{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pc), current); err == nil {
				current.Finalizers = nil
				Expect(k8sClient.Update(ctx, current)).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, current))).To(Succeed())
			}
		})
		return pc
	}

	reconcileProxyCache := func(ctx context.Context, pc *registryv1alpha1.ProxyCache) *registryv1alpha1.ProxyCache {
		r := &ProxyCacheReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(pc)})
		Expect(err).NotTo(HaveOccurred())
		out := &registryv1alpha1.ProxyCache{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pc), out); apierrors.IsNotFound(err) {
			return nil
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		return out
	}

	dockerHub := registryv1alpha1.Upstream{Type: registryv1alpha1.UpstreamDockerHub, URL: "https://hub.docker.com"}

	It("creates a registry endpoint and a public proxy project", func(ctx SpecContext) {
		pc := reconcileProxyCache(ctx, newProxyCache(ctx, "dockerhub", dockerHub))

		Expect(meta.IsStatusConditionTrue(pc.Status.Conditions, TypeReady)).To(BeTrue())
		Expect(pc.Status.Endpoint).To(Equal("harbor.example.com/dockerhub"))
		Expect(pc.Finalizers).To(ContainElement(ProxyCacheFinalizer))

		endpoint := fake.Registry("dockerhub")
		Expect(endpoint).NotTo(BeNil())
		Expect(endpoint.Type).To(Equal("docker-hub"))
		Expect(endpoint.URL).To(Equal("https://hub.docker.com"))

		project := fake.Project("dockerhub")
		Expect(project).NotTo(BeNil())
		Expect(project.RegistryID).To(Equal(endpoint.ID))
		Expect(project.Metadata).To(HaveKeyWithValue("public", "true"))
	})

	It("puts back settings changed in Harbor", func(ctx SpecContext) {
		pc := newProxyCache(ctx, "ghcr", registryv1alpha1.Upstream{Type: registryv1alpha1.UpstreamGitHubGHCR, URL: "https://ghcr.io"})
		reconcileProxyCache(ctx, pc)

		fake.Mu.Lock()
		fake.Projects["ghcr"].Metadata["public"] = "false"
		for _, r := range fake.Registries {
			r.URL = "https://example.com"
		}
		fake.Mu.Unlock()

		reconcileProxyCache(ctx, pc)
		Expect(fake.Project("ghcr").Metadata).To(HaveKeyWithValue("public", "true"))
		Expect(fake.Registry("ghcr").URL).To(Equal("https://ghcr.io"))
	})

	It("follows spec.public", func(ctx SpecContext) {
		pc := newProxyCache(ctx, "quay", registryv1alpha1.Upstream{Type: registryv1alpha1.UpstreamQuay, URL: "https://quay.io"})
		reconcileProxyCache(ctx, pc)

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pc), pc)).To(Succeed())
		pc.Spec.Public = ptr.To(false)
		Expect(k8sClient.Update(ctx, pc)).To(Succeed())
		reconcileProxyCache(ctx, pc)

		Expect(fake.Project("quay").Metadata).To(HaveKeyWithValue("public", "false"))
	})

	It("sends upstream credentials from a Secret", func(ctx SpecContext) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "hub-login", Namespace: "default"},
			StringData: map[string]string{"username": "erik", "password": "token"},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { Expect(k8sClient.Delete(ctx, secret)).To(Succeed()) })

		upstream := dockerHub
		upstream.CredentialsSecretRef = &corev1.LocalObjectReference{Name: secret.Name}
		reconcileProxyCache(ctx, newProxyCache(ctx, "hub-authed", upstream))

		Expect(fake.Registry("hub-authed").Credential).To(Equal(&harbor.Credential{
			Type: "basic", AccessKey: "erik", AccessSecret: "token",
		}))
	})

	It("refuses to take over a project that proxies something else", func(ctx SpecContext) {
		fake.Mu.Lock()
		fake.Projects["k8s"] = &harbor.Project{ProjectID: 99, Name: "k8s", Metadata: map[string]string{"public": "true"}}
		fake.Mu.Unlock()

		pc := reconcileProxyCache(ctx, newProxyCache(ctx, "k8s",
			registryv1alpha1.Upstream{Type: registryv1alpha1.UpstreamDockerRegistry, URL: "https://registry.k8s.io"}))

		cond := meta.FindStatusCondition(pc.Status.Conditions, TypeReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("Conflict"))
		Expect(fake.Project("k8s").RegistryID).To(BeZero())
	})

	It("reports a missing Registry", func(ctx SpecContext) {
		pc := newProxyCache(ctx, "orphan", dockerHub)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pc), pc)).To(Succeed())
		pc.Spec.RegistryRef.Name = "missing"
		Expect(k8sClient.Update(ctx, pc)).To(Succeed())

		pc = reconcileProxyCache(ctx, pc)
		cond := meta.FindStatusCondition(pc.Status.Conditions, TypeReady)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring("missing"))
	})

	It("deletes the cached repositories, project and endpoint on deletion", func(ctx SpecContext) {
		pc := newProxyCache(ctx, "dockerhub", dockerHub)
		reconcileProxyCache(ctx, pc)

		fake.Mu.Lock()
		fake.Repositories["dockerhub"] = []string{"dockerhub/library/nginx", "dockerhub/library/busybox"}
		fake.Mu.Unlock()

		Expect(k8sClient.Delete(ctx, pc)).To(Succeed())
		Expect(reconcileProxyCache(ctx, pc)).To(BeNil())

		Expect(fake.Project("dockerhub")).To(BeNil())
		Expect(fake.Registry("dockerhub")).To(BeNil())
	})
})
