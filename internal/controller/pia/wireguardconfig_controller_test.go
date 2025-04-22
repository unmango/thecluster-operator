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

			job := findJob(typeNamespacedName.Name)
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

			BeforeEach(func() {
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

			AfterEach(func() {
				By("Cleaning up the secret")
				sec := &corev1.Secret{}
				if err := k8sClient.Get(ctx, secretName, sec); err == nil {
					Expect(k8sClient.Delete(ctx, sec)).To(Succeed())
				}
			})

			It("should set the PIA_USER env var source", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				job := findJob(typeNamespacedName.Name)
				var initContainer *corev1.Container
				Expect(job.Spec.Template.Spec.InitContainers).To(ContainElement(
					HaveField("Name", "generate-config"), initContainer,
				))
				Expect(initContainer.Env).To(ContainElement(corev1.EnvVar{
					Name: "PIA_USER",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: wireguardconfig.Spec.Username.SecretKeyRef,
					},
				}))
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

			It("should set the PIA_PASS env var source", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				job := findJob(typeNamespacedName.Name)
				var initContainer *corev1.Container
				Expect(job.Spec.Template.Spec.InitContainers).To(ContainElement(
					HaveField("Name", "generate-config"), initContainer,
				))
				Expect(initContainer.Env).To(ContainElement(corev1.EnvVar{
					Name: "PIA_PASS",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: wireguardconfig.Spec.Password.SecretKeyRef,
					},
				}))
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

			It("should set the PIA_USER env var source", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				job := findJob(typeNamespacedName.Name)
				var initContainer *corev1.Container
				Expect(job.Spec.Template.Spec.InitContainers).To(ContainElement(
					HaveField("Name", "generate-config"), initContainer,
				))
				Expect(initContainer.Env).To(ContainElement(corev1.EnvVar{
					Name: "PIA_USER",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: wireguardconfig.Spec.Username.ConfigMapKeyRef,
					},
				}))
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

			It("should set the PIA_PASS env var source", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				job := findJob(typeNamespacedName.Name)
				var initContainer *corev1.Container
				Expect(job.Spec.Template.Spec.InitContainers).To(ContainElement(
					HaveField("Name", "generate-config"), initContainer,
				))
				Expect(initContainer.Env).To(ContainElement(corev1.EnvVar{
					Name: "PIA_PASS",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: wireguardconfig.Spec.Password.ConfigMapKeyRef,
					},
				}))
			})
		})

		When("a matching generate job exists", func() {
			const jobName = "generate-config-fjdsk"

			BeforeEach(func() {
				By("Creating a matching generate job")
				job := &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      jobName,
						Namespace: typeNamespacedName.Namespace,
						Labels: map[string]string{
							"app.kubernetes.io/name":   "thecluster-operator",
							"pia.thecluster.io/config": typeNamespacedName.Name,
						},
					},
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								RestartPolicy: corev1.RestartPolicyNever,
								Containers: []corev1.Container{{
									Name:  "stub",
									Image: "busybox",
								}},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, job)).To(Succeed())
			})

			AfterEach(func() {
				By("cleaning up the generate job")
				namespacedName := types.NamespacedName{
					Name:      jobName,
					Namespace: typeNamespacedName.Namespace,
				}
				job := &batchv1.Job{}
				if err := k8sClient.Get(ctx, namespacedName, job); err == nil {
					Expect(k8sClient.Delete(ctx, job)).To(Succeed())
				}
			})

			It("should not create any new jobs", func() {
				By("Reconciling the created resource")
				controllerReconciler := &WireguardConfigReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Listing the jobs with matching labels")
				jobList := &batchv1.JobList{}
				err = k8sClient.List(ctx, jobList, client.MatchingLabels{
					"app.kubernetes.io/name":   "thecluster-operator",
					"pia.thecluster.io/config": typeNamespacedName.Name,
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(jobList.Items).To(HaveLen(1), "Too many jobs created")
			})
		})

		When("a matching config exists", func() {
			BeforeEach(func(ctx context.Context) {
				By("Creating a matching config map")
				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      typeNamespacedName.Name,
						Namespace: typeNamespacedName.Namespace,
					},
					Data: map[string]string{},
				}
				Expect(k8sClient.Create(ctx, configMap)).To(Succeed())
			})

			AfterEach(func(ctx context.Context) {
				By("Cleaning up the config map")
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, typeNamespacedName, cm); err == nil {
					Expect(k8sClient.Delete(ctx, cm)).To(Succeed())
				}
			})

			It("should mark the wireguard config as available", func() {
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

			It("should mark the wireguard config as errored", func() {
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

			It("should mark the wireguard config as errored", func() {
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

func findJob(name string) batchv1.Job {
	GinkgoHelper()

	jobList := &batchv1.JobList{}
	Eventually(func() error {
		return k8sClient.List(ctx, jobList, client.MatchingLabels{
			"app.kubernetes.io/name":   "thecluster-operator",
			"pia.thecluster.io/config": name,
		})
	}).Should(Succeed())

	Expect(jobList.Items).To(HaveLen(1))
	return jobList.Items[0]
}
