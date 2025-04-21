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

package pia

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		var wireguardconfig *piav1alpha1.WireguardConfig

		BeforeEach(func() {
			wireguardconfig = &piav1alpha1.WireguardConfig{
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
		})

		JustBeforeEach(func() {
			By("creating the custom resource for the Kind WireguardConfig")
			err := k8sClient.Get(ctx, typeNamespacedName, wireguardconfig)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, wireguardconfig)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &piav1alpha1.WireguardConfig{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance WireguardConfig")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Deleting any generate pods")
			podList := &corev1.PodList{}
			err = k8sClient.List(ctx, podList, client.MatchingLabels{
				"app.kubernetes.io/name":   "thecluster-operator",
				"pia.thecluster.io/config": typeNamespacedName.Name,
			})
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range podList.Items {
				Expect(k8sClient.Delete(ctx, &pod)).To(Succeed())
			}
		})

		It("should create a job to generate the config", func() {
			By("Reconciling the created resource")
			controllerReconciler := &WireguardConfigReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Fetching the config resource")
			resource := &piav1alpha1.WireguardConfig{}
			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			generating := meta.IsStatusConditionTrue(
				resource.Status.Conditions,
				TypeGeneratingWireguardConfig,
			)
			Expect(generating).To(BeTrueBecause("The config is generating"))

			jobList := &batchv1.JobList{}
			Eventually(func() error {
				return k8sClient.List(ctx, jobList, client.MatchingLabels{
					"app.kubernetes.io/name":   "thecluster-operator",
					"pia.thecluster.io/config": typeNamespacedName.Name,
				})
			}).Should(Succeed())

			Expect(jobList.Items).To(HaveLen(1))
			job := jobList.Items[0]

			Expect(job.Name).To(HavePrefix("generate-config-"))
			Expect(job.OwnerReferences).To(ConsistOf(And(
				HaveField("Kind", "WireguardConfig"),
				HaveField("Name", resourceName),
			)))

			templateSpec := job.Spec.Template.Spec
			Expect(templateSpec.Volumes).To(ConsistOf(corev1.Volume{
				Name: "results",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			}))

			Expect(templateSpec.InitContainers).To(HaveLen(1))
			initContainer := templateSpec.InitContainers[0]
			Expect(initContainer.Name).To(Equal("generate-config"))
			Expect(initContainer.Image).To(HavePrefix("unstoppablemango/pia-manual-connections:"))
			Expect(initContainer.Env).To(ConsistOf(
				corev1.EnvVar{Name: "PIA_USER", Value: piaUser},
				corev1.EnvVar{Name: "PIA_PASS", Value: piaPass},
				corev1.EnvVar{Name: "PIA_PF", Value: "false"},
				corev1.EnvVar{Name: "PIA_CONNECT", Value: "false"},
				corev1.EnvVar{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
				corev1.EnvVar{Name: "VPN_PROTOCOL", Value: "wireguard"},
				corev1.EnvVar{Name: "DISABLE_IPV6", Value: "no"},
				corev1.EnvVar{Name: "DIP_TOKEN", Value: "no"},
				corev1.EnvVar{Name: "AUTOCONNECT", Value: "true"},
			))
			Expect(initContainer.VolumeMounts).To(ConsistOf(corev1.VolumeMount{
				Name:      "results",
				MountPath: "/out",
			}))

			Expect(templateSpec.Containers).To(HaveLen(1))
			container := templateSpec.Containers[0]
			Expect(container.Name).To(Equal("create-secret"))
			Expect(container.Image).To(HavePrefix("bitnami/kubectl:"))
			Expect(container.Command).To(HaveExactElements(
				"create", "configmap", typeNamespacedName.Name,
				"--namespace", typeNamespacedName.Namespace,
				"--from-file=/out",
			))
			Expect(container.VolumeMounts).To(ConsistOf(corev1.VolumeMount{
				Name:      "results",
				MountPath: "/out",
			}))
		})

		When("username is provided in a secret", func() {
			const usernameKey = "pia-username"

			secretName := types.NamespacedName{
				Name:      "my-credentials",
				Namespace: typeNamespacedName.Namespace,
			}

			BeforeEach(func(ctx context.Context) {
				By("Creating the secret")
				sec := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretName.Name,
						Namespace: secretName.Namespace,
					},
					StringData: map[string]string{
						usernameKey: piaUser,
					},
				}
				Expect(k8sClient.Create(ctx, sec)).To(Succeed())

				wireguardconfig.Spec.Username = piav1alpha1.WireguardClientConfigValue{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: sec.Name,
						},
						Key: usernameKey,
					},
				}
			})

			AfterEach(func(ctx context.Context) {
				By("Cleaning up the secret")
				sec := &corev1.Secret{}
				if err := k8sClient.Get(ctx, secretName, sec); err == nil {
					Expect(k8sClient.Delete(ctx, sec)).To(Succeed())
				}
			})

			It("should create a job to generate the config", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				generating := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeGeneratingWireguardConfig,
				)
				Expect(generating).To(BeTrueBecause("The config is generating"))

				podList := &corev1.PodList{}
				Eventually(func() error {
					return k8sClient.List(ctx, podList, client.MatchingLabels{
						"app.kubernetes.io/name":   "thecluster-operator",
						"pia.thecluster.io/config": typeNamespacedName.Name,
					})
				}).Should(Succeed())

				Expect(podList.Items).To(HaveLen(1))
				pod := podList.Items[0]

				Expect(pod.Name).To(HavePrefix("generate-config-"))
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

				Expect(pod.Spec.Containers).To(ConsistOf(
					And(
						HaveField("Name", "generate-config"),
						HaveField("Image", "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0"),
						HaveField("Env", []corev1.EnvVar{
							{
								Name: "PIA_USER",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef: resource.Spec.Username.SecretKeyRef,
								},
							},
							{Name: "PIA_PASS", Value: piaPass},
							{Name: "PIA_PF", Value: "false"},
							{Name: "PIA_CONNECT", Value: "false"},
							{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
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

		When("password is provided in a secret", func() {
			const passwordKey = "pia-password"

			secretName := types.NamespacedName{
				Name:      "my-credentials",
				Namespace: typeNamespacedName.Namespace,
			}

			BeforeEach(func(ctx context.Context) {
				By("Creating the secret")
				sec := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretName.Name,
						Namespace: secretName.Namespace,
					},
					StringData: map[string]string{
						passwordKey: piaPass,
					},
				}
				Expect(k8sClient.Create(ctx, sec)).To(Succeed())

				wireguardconfig.Spec.Password = piav1alpha1.WireguardClientConfigValue{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: sec.Name,
						},
						Key: passwordKey,
					},
				}
			})

			AfterEach(func(ctx context.Context) {
				By("Cleaning up the secret")
				sec := &corev1.Secret{}
				if err := k8sClient.Get(ctx, secretName, sec); err == nil {
					Expect(k8sClient.Delete(ctx, sec)).To(Succeed())
				}
			})

			It("should create a job to generate the config", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				generating := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeGeneratingWireguardConfig,
				)
				Expect(generating).To(BeTrueBecause("The config is generating"))

				podList := &corev1.PodList{}
				Eventually(func() error {
					return k8sClient.List(ctx, podList, client.MatchingLabels{
						"app.kubernetes.io/name":   "thecluster-operator",
						"pia.thecluster.io/config": typeNamespacedName.Name,
					})
				}).Should(Succeed())

				Expect(podList.Items).To(HaveLen(1))
				pod := podList.Items[0]

				Expect(pod.Name).To(HavePrefix("generate-config-"))
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

				Expect(pod.Spec.Containers).To(ConsistOf(
					And(
						HaveField("Name", "generate-config"),
						HaveField("Image", "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0"),
						HaveField("Env", []corev1.EnvVar{
							{Name: "PIA_USER", Value: piaUser},
							{
								Name: "PIA_PASS",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef: resource.Spec.Password.SecretKeyRef,
								},
							},
							{Name: "PIA_PF", Value: "false"},
							{Name: "PIA_CONNECT", Value: "false"},
							{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
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

		When("username is provided in a config map", func() {
			const usernameKey = "pia-username"

			configMapName := types.NamespacedName{
				Name:      "my-credentials",
				Namespace: typeNamespacedName.Namespace,
			}

			BeforeEach(func(ctx context.Context) {
				By("Creating the config map")
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName.Name,
						Namespace: configMapName.Namespace,
					},
					Data: map[string]string{
						usernameKey: piaUser,
					},
				}
				Expect(k8sClient.Create(ctx, cm)).To(Succeed())

				wireguardconfig.Spec.Username = piav1alpha1.WireguardClientConfigValue{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cm.Name,
						},
						Key: usernameKey,
					},
				}
			})

			AfterEach(func(ctx context.Context) {
				By("Cleaning up the config map")
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, configMapName, cm); err == nil {
					Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
				}
			})

			It("should create a job to generate the config", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				generating := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeGeneratingWireguardConfig,
				)
				Expect(generating).To(BeTrueBecause("The config is generating"))

				podList := &corev1.PodList{}
				Eventually(func() error {
					return k8sClient.List(ctx, podList, client.MatchingLabels{
						"app.kubernetes.io/name":   "thecluster-operator",
						"pia.thecluster.io/config": typeNamespacedName.Name,
					})
				}).Should(Succeed())

				Expect(podList.Items).To(HaveLen(1))
				pod := podList.Items[0]

				Expect(pod.Name).To(HavePrefix("generate-config-"))
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

				Expect(pod.Spec.Containers).To(ConsistOf(
					And(
						HaveField("Name", "generate-config"),
						HaveField("Image", "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0"),
						HaveField("Env", []corev1.EnvVar{
							{
								Name: "PIA_USER",
								ValueFrom: &corev1.EnvVarSource{
									ConfigMapKeyRef: resource.Spec.Username.ConfigMapKeyRef,
								},
							},
							{Name: "PIA_PASS", Value: piaPass},
							{Name: "PIA_PF", Value: "false"},
							{Name: "PIA_CONNECT", Value: "false"},
							{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
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

		When("password is provided in a config map", func() {
			const passwordKey = "pia-password"

			configMapName := types.NamespacedName{
				Name:      "my-credentials",
				Namespace: typeNamespacedName.Namespace,
			}

			BeforeEach(func(ctx context.Context) {
				By("Creating the config map")
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName.Name,
						Namespace: configMapName.Namespace,
					},
					Data: map[string]string{
						passwordKey: piaPass,
					},
				}
				Expect(k8sClient.Create(ctx, cm)).To(Succeed())

				wireguardconfig.Spec.Password = piav1alpha1.WireguardClientConfigValue{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cm.Name,
						},
						Key: passwordKey,
					},
				}
			})

			AfterEach(func(ctx context.Context) {
				By("Cleaning up the config map")
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, configMapName, cm); err == nil {
					Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
				}
			})

			It("should create a job to generate the config", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				generating := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeGeneratingWireguardConfig,
				)
				Expect(generating).To(BeTrueBecause("The config is generating"))

				podList := &corev1.PodList{}
				Eventually(func() error {
					return k8sClient.List(ctx, podList, client.MatchingLabels{
						"app.kubernetes.io/name":   "thecluster-operator",
						"pia.thecluster.io/config": typeNamespacedName.Name,
					})
				}).Should(Succeed())

				Expect(podList.Items).To(HaveLen(1))
				pod := podList.Items[0]

				Expect(pod.Name).To(HavePrefix("generate-config-"))
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

				Expect(pod.Spec.Containers).To(ConsistOf(
					And(
						HaveField("Name", "generate-config"),
						HaveField("Image", "unstoppablemango/pia-manual-connections:v0.2.0-pia2023-02-06r0"),
						HaveField("Env", []corev1.EnvVar{
							{Name: "PIA_USER", Value: piaUser},
							{
								Name: "PIA_PASS",
								ValueFrom: &corev1.EnvVarSource{
									ConfigMapKeyRef: resource.Spec.Password.ConfigMapKeyRef,
								},
							},
							{Name: "PIA_PF", Value: "false"},
							{Name: "PIA_CONNECT", Value: "false"},
							{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
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

		When("a matching generate pod exists", func() {
			const genPodName = "generate-config-fjdsk"

			BeforeEach(func() {
				By("creating a generate pod")
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      genPodName,
						Namespace: typeNamespacedName.Namespace,
						Labels: map[string]string{
							"app.kubernetes.io/name":   "thecluster-operator",
							"pia.thecluster.io/config": typeNamespacedName.Name,
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "stub",
							Image: "busybox",
						}},
					},
				}
				Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			})

			AfterEach(func() {
				By("cleaning up the generate pod")
				podName := types.NamespacedName{
					Name:      genPodName,
					Namespace: typeNamespacedName.Namespace,
				}
				pod := &corev1.Pod{}
				if err := k8sClient.Get(ctx, podName, pod); err == nil {
					Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
				}
			})

			It("should not create any new pods", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Listing the pods with matching labels")
				podList := &corev1.PodList{}
				err = k8sClient.List(ctx, podList, client.MatchingLabels{
					"app.kubernetes.io/name":   "thecluster-operator",
					"pia.thecluster.io/config": typeNamespacedName.Name,
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(podList.Items).To(HaveLen(1), "too many pods created")
			})
		})

		When("a matching config exists", func() {
			BeforeEach(func(ctx context.Context) {
				By("Creating a matching config map")
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      typeNamespacedName.Name,
						Namespace: typeNamespacedName.Namespace,
					},
					Data: map[string]string{},
				}
				Expect(k8sClient.Create(ctx, cm)).To(Succeed())
			})

			AfterEach(func(ctx context.Context) {
				By("Cleaning up the config map")
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, typeNamespacedName, cm); err == nil {
					Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
				}
			})

			It("Should be available", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				available := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeAvailableWireguardConfig,
				)
				Expect(available).To(BeTrueBecause("The config is available"))
			})
		})

		When("username is not provided", func() {
			BeforeEach(func() {
				wireguardconfig.Spec.Username.Value = ""
			})

			It("Should error", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				errored := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeErrorWireguardConfig,
				)
				Expect(errored).To(BeTrueBecause("The config is invalid"))
			})
		})

		When("password is not provided", func() {
			BeforeEach(func() {
				wireguardconfig.Spec.Password.Value = ""
			})

			It("Should error", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Fetching the config resource")
				resource := &piav1alpha1.WireguardConfig{}
				err = k8sClient.Get(ctx, typeNamespacedName, resource)
				Expect(err).NotTo(HaveOccurred())

				errored := meta.IsStatusConditionTrue(
					resource.Status.Conditions,
					TypeErrorWireguardConfig,
				)
				Expect(errored).To(BeTrueBecause("The config is invalid"))
			})
		})
	})
})
