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

package core

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1alpha1 "github.com/unmango/thecluster-operator/api/core/v1alpha1"
)

var _ = Describe("WireguardClient Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		wireguardclient := &corev1alpha1.WireguardClient{}
		configMap := &corev1.ConfigMap{}
		secret := &corev1.Secret{}

		BeforeEach(func() {
			By("Creating a config map with a client config")
			err := k8sClient.Get(ctx, typeNamespacedName, configMap)
			if err != nil && errors.IsNotFound(err) {
				configMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Data: map[string]string{
						"client.conf": "blah blah config",
					},
				}
				Expect(k8sClient.Create(ctx, configMap)).To(Succeed())
			}

			By("Creating a secret with a client config")
			err = k8sClient.Get(ctx, typeNamespacedName, secret)
			if err != nil && errors.IsNotFound(err) {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					StringData: map[string]string{
						"client.conf": "blah blah config",
					},
				}
				Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			}

			By("Creating the custom resource for the Kind WireguardClient")
			err = k8sClient.Get(ctx, typeNamespacedName, wireguardclient)
			if err != nil && errors.IsNotFound(err) {
				resource := &corev1alpha1.WireguardClient{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: corev1alpha1.WireguardClientSpec{
						PUID: 6969,
						PGID: 4200,
						TZ:   "America/Chicago",
						Configs: []corev1alpha1.WireguardClientConfig{
							{
								Name: "test-config",
								ValueFrom: &corev1alpha1.WireguardClientConfigSource{
									ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: configMap.Name,
										},
										Key: "client.conf",
									},
								},
							},
							{
								Name: "test-secret",
								ValueFrom: &corev1alpha1.WireguardClientConfigSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: secret.Name,
										},
										Key: "client.conf",
									},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &corev1alpha1.WireguardClient{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleaning up the specific resource instance WireguardClient")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Removing any dangling deployments")
			deployment := &appsv1.Deployment{}
			err = k8sClient.Get(ctx, typeNamespacedName, deployment)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

			By("Cleaning up everything else")
			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		It("should successfully reconcile the resource", func(ctx context.Context) {
			By("Reconciling the created resource")
			controllerReconciler := &WireguardClientReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the deployment was created")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, typeNamespacedName, deployment)
			}).Should(Succeed())

			Expect(deployment.OwnerReferences).To(ConsistOf(And(
				HaveField("Kind", "WireguardClient"),
				HaveField("Name", resourceName),
			)))

			Expect(deployment.Spec.Replicas).To(Equal(ptr.To[int32](1)))
			Expect(deployment.Spec.Selector.MatchLabels).To(And(
				HaveKeyWithValue("app.kubernetes.io/name", "wireguard"),
				HaveKeyWithValue("app.kubernetes.io/version", "latest"),
				HaveKeyWithValue("app.kubernetes.io/managed-by", "WireguardClientController"),
			))
			Expect(deployment.Spec.Template.Labels).To(And(
				HaveKeyWithValue("app.kubernetes.io/name", "wireguard"),
				HaveKeyWithValue("app.kubernetes.io/version", "latest"),
				HaveKeyWithValue("app.kubernetes.io/managed-by", "WireguardClientController"),
			))

			Expect(deployment.Spec.Template.Spec.Volumes).To(HaveLen(2))
			volume := deployment.Spec.Template.Spec.Volumes[0]
			Expect(volume.ConfigMap).NotTo(BeNil())
			Expect(volume.ConfigMap.Name).To(Equal(configMap.Name))
			Expect(volume.ConfigMap.Items).To(ConsistOf(
				corev1.KeyToPath{Key: "client.conf", Path: "client.conf"},
			))
			volume = deployment.Spec.Template.Spec.Volumes[1]
			Expect(volume.Secret).NotTo(BeNil())
			Expect(volume.Secret.SecretName).To(Equal(secret.Name))
			Expect(volume.Secret.Items).To(ConsistOf(
				corev1.KeyToPath{Key: "client.conf", Path: "client.conf"},
			))

			Expect(deployment.Spec.Template.Spec.SecurityContext).NotTo(BeNil())
			securityContext := deployment.Spec.Template.Spec.SecurityContext
			Expect(securityContext.RunAsNonRoot).To(Equal(ptr.To(true)), "RunAsNonRoot")
			Expect(securityContext.SeccompProfile).NotTo(BeNil())
			Expect(securityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))

			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := deployment.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal("wireguard"))
			Expect(container.Image).To(Equal("lscr.io/linuxserver/wireguard:latest"))
			Expect(container.Env).To(ConsistOf(
				corev1.EnvVar{Name: "PUID", Value: "6969"},
				corev1.EnvVar{Name: "PGID", Value: "4200"},
				corev1.EnvVar{Name: "TZ", Value: "America/Chicago"},
				corev1.EnvVar{Name: "S6_READ_ONLY_ROOT", Value: "1"},
			))
			Expect(container.Ports).To(ConsistOf(corev1.ContainerPort{
				ContainerPort: 51820,
				Protocol:      corev1.ProtocolUDP,
			}))

			Expect(container.VolumeMounts).To(HaveLen(2))
			volumeMount := container.VolumeMounts[0]
			Expect(volumeMount.Name).To(Equal("test-config"))
			Expect(volumeMount.MountPath).To(Equal("/config/test-config.conf"))
			volumeMount = container.VolumeMounts[1]
			Expect(volumeMount.Name).To(Equal("test-secret"))
			Expect(volumeMount.MountPath).To(Equal("/config/test-secret.conf"))

			Expect(container.SecurityContext).NotTo(BeNil())
			Expect(container.SecurityContext.Capabilities).NotTo(BeNil())
			Expect(container.SecurityContext.Capabilities.Add).To(ConsistOf(
				corev1.Capability("NET_ADMIN"),
			))
			Expect(container.SecurityContext.RunAsUser).To(Equal(ptr.To[int64](6969)), "RunAsUser")
			Expect(container.SecurityContext.RunAsGroup).To(Equal(ptr.To[int64](4200)), "RunAsGroup")
			Expect(container.SecurityContext.RunAsNonRoot).To(Equal(ptr.To(true)), "RunAsNonRoot")
			Expect(container.SecurityContext.ReadOnlyRootFilesystem).To(BeNil(), "ReadOnlyRootFilesystem")
			Expect(container.SecurityContext.AllowPrivilegeEscalation).To(Equal(ptr.To(false)), "AllowPrivilegeEscalation")

			By("Checking the latest status condition")
			Expect(k8sClient.Get(ctx, typeNamespacedName, wireguardclient)).To(Succeed())
			conditions := []metav1.Condition{}
			Expect(wireguardclient.Status.Conditions).To(ContainElement(
				HaveField("Type", TypeAvailableWireguardClient), &conditions,
			))
			Expect(conditions).To(HaveLen(1), "Multiple conditions of type %s", TypeAvailableWireguardClient)
			Expect(conditions[0].Status).To(Equal(metav1.ConditionTrue), "condition %s", TypeAvailableWireguardClient)
			Expect(conditions[0].Reason).To(Equal("Reconciling"), "condition %s", TypeAvailableWireguardClient)

			By("Checking that the finalizer was added")
			Expect(wireguardclient.Finalizers).To(ConsistOf(WireguardClientFinalizer))
		})
	})
})
