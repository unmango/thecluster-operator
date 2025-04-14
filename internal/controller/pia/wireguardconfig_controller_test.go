/*
Copyright 2025 UnstoppableMango.

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

package pia

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	piav1alpha1 "github.com/unmango/thecluster-operator/api/pia/v1alpha1"
)

var _ = Describe("WireguardConfig Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName = "test-resource"
			piaUser      = "test-user"
			piaPass      = "test-password"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		wireguardconfig := &piav1alpha1.WireguardConfig{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind WireguardConfig")
			err := k8sClient.Get(ctx, typeNamespacedName, wireguardconfig)
			if err != nil && errors.IsNotFound(err) {
				resource := &piav1alpha1.WireguardConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &piav1alpha1.WireguardConfig{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance WireguardConfig")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &WireguardConfigReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, typeNamespacedName, job)
			}).Should(Succeed())

			Expect(job.OwnerReferences).To(ConsistOf(And(
				HaveField("Kind", "WireguardConfig"),
				HaveField("Name", resourceName),
			)))

			Expect(job.Spec.Selector.MatchLabels).To(And(
				HaveKeyWithValue("app.kubernetes.io/name", "wireguard"),
				HaveKeyWithValue("app.kubernetes.io/version", "latest"),
				HaveKeyWithValue("app.kubernetes.io/managed-by", "WireguardConfigController"),
			))
			Expect(job.Spec.Template.Labels).To(And(
				HaveKeyWithValue("app.kubernetes.io/name", "wireguard"),
				HaveKeyWithValue("app.kubernetes.io/version", "latest"),
				HaveKeyWithValue("app.kubernetes.io/managed-by", "WireguardConfigController"),
			))

			Expect(job.Spec.Template.Spec.Containers).To(HaveLen(2))
			container := job.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal("get-token"))
			Expect(container.Image).To(Equal("unstoppablemango/pia-manual-connections:latest"))
			Expect(container.Command).To(ConsistOf("/src/get_token.sh"))
			Expect(container.Env).To(ConsistOf(
				corev1.EnvVar{Name: "PIA_USER", Value: piaUser},
				corev1.EnvVar{Name: "PIA_PASS", Value: piaPass},
			))

			container = job.Spec.Template.Spec.Containers[1]
			Expect(container.Name).To(Equal("get-region"))
			Expect(container.Image).To(Equal("unstoppablemango/pia-manual-connections:latest"))
			Expect(container.Command).To(ConsistOf("/src/get_region.sh"))
			Expect(container.Env).To(ConsistOf(
				corev1.EnvVar{Name: "VPN_PROTOCOL", Value: "no"},
				// Any non-empty value so the script doesn't attempt to authenticate
				corev1.EnvVar{Name: "PIA_TOKEN", Value: "ignore"},
			))

			// TODO: WG connect script env vars
			Expect(container.Env).To(ConsistOf(
				corev1.EnvVar{Name: "PIA_CONNECT", Value: "false"},
				corev1.EnvVar{Name: "WG_SERVER_IP", Value: "TODO"},
				corev1.EnvVar{Name: "WG_HOSTNAME", Value: "TODO"},
				corev1.EnvVar{Name: "PIA_TOKEN", Value: "TODO"},
				corev1.EnvVar{Name: "PIA_PF", Value: "TODO"},
			))
		})
	})
})
