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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
					Spec: piav1alpha1.WireguardConfigSpec{
						Username: piav1alpha1.WireguardClientConfigValue{
							Value: piaUser,
						},
						Password: piav1alpha1.WireguardClientConfigValue{
							Value: piaPass,
						},
					},
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

			podName := types.NamespacedName{
				Namespace: typeNamespacedName.Namespace,
				Name:      "generate-config",
			}
			pod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, podName, pod)
			}).Should(Succeed())

			Expect(pod.OwnerReferences).To(ConsistOf(And(
				HaveField("Kind", "WireguardConfig"),
				HaveField("Name", resourceName),
			)))

			Expect(pod.Spec.Volumes).To(ConsistOf(
				corev1.Volume{
					Name: "results",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			))

			Expect(pod.Spec.InitContainers).To(ConsistOf(And(
				HaveField("Name", "setup"),
				HaveField("Image", "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0"),
				HaveField("Env", []corev1.EnvVar{
					{Name: "PIA_USER", Value: piaUser},
					{Name: "PIA_PASS", Value: piaPass},
					{Name: "PIA_PF", Value: "false"},
					{Name: "PIA_CONNECT", Value: "false"},
					{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
					{Name: "VPN_PROTOCOL", Value: "no"},
					{Name: "DISABLE_IPV6", Value: "no"},
					{Name: "DIP_TOKEN", Value: "no"},
					{Name: "AUTOCONNECT", Value: "true"},
				}),
				HaveField("VolumeMounts", []corev1.VolumeMount{{
					Name:      "results",
					MountPath: "/out",
				}}),
			)))

			Expect(pod.Spec.Containers).To(ConsistOf(
				And(
					HaveField("Name", "generate-config"),
					HaveField("Image", "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0"),
					HaveField("Command", []string{"/src/connect_to_wireguard_with_token.sh"}),
					HaveField("Env", []corev1.EnvVar{
						{Name: "PIA_USER", Value: piaUser},
						{Name: "PIA_PASS", Value: piaPass},
						{Name: "PIA_PF", Value: "false"},
						{Name: "PIA_CONNECT", Value: "false"},
						{Name: "VPN_PROTOCOL", Value: "wireguard"},
						{Name: "DISABLE_IPV6", Value: "no"},
						{Name: "DIP_TOKEN", Value: "no"},
						{Name: "AUTOCONNECT", Value: "true"},
					}),
					HaveField("VolumeMounts", []corev1.VolumeMount{{
						Name:      "results",
						MountPath: "/out",
					}}),
				),
				And(
					HaveField("Name", "results"),
					HaveField("Image", "busybox:latest"),
					HaveField("Command", []string{"sh", "-c", "sleep infinity"}),
					HaveField("VolumeMounts", []corev1.VolumeMount{{
						Name:      "results",
						ReadOnly:  true,
						MountPath: "/out",
					}}),
				),
			))
		})
	})
})
