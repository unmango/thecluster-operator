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

package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	piav1alpha1 "github.com/unmango/thecluster-operator/api/pia/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Pod Webhook", func() {
	var (
		obj       *corev1.Pod
		oldObj    *corev1.Pod
		defaulter PodCustomDefaulter
	)

	BeforeEach(func() {
		obj = &corev1.Pod{}
		oldObj = &corev1.Pod{}
		defaulter = PodCustomDefaulter{}
		Expect(defaulter).NotTo(BeNil(), "Expected defaulter to be initialized")
		Expect(oldObj).NotTo(BeNil(), "Expected oldObj to be initialized")
		Expect(obj).NotTo(BeNil(), "Expected obj to be initialized")
	})

	AfterEach(func() {
		// TODO (user): Add any teardown logic common to all tests
	})

	Context("When creating Pod under Defaulting Webhook", func() {
		// TODO (user): Add logic for defaulting webhooks
		// Example:
		// It("Should apply defaults when a required field is empty", func() {
		//     By("simulating a scenario where defaults should be applied")
		//     obj.SomeFieldWithDefault = ""
		//     By("calling the Default method to apply defaults")
		//     defaulter.Default(ctx, obj)
		//     By("checking that the default values are set")
		//     Expect(obj.SomeFieldWithDefault).To(Equal("default_value"))
		// })

		It("should not add init containers to un-annotated pods", func() {
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.InitContainers).To(BeEmpty())
		})

		When("the pod is annotated with a wireguard config", func() {
			BeforeEach(func() {
				obj.Annotations = map[string]string{
					"pia.thecluster.io/config": "blah",
				}
			})

			It("should error reporting the config does not exist", func() {
				err := defaulter.Default(ctx, obj)

				Expect(err).To(MatchError("config 'blah' does not exist"))
			})

			Context("and the wireguard config exists", func() {
				const (
					username = "test-user"
					password = "test-password"
				)

				BeforeEach(func() {
					config := &piav1alpha1.WireguardConfig{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-config",
							Namespace: "default",
						},
						Spec: piav1alpha1.WireguardConfigSpec{
							Username: piav1alpha1.WireguardClientConfigValue{
								Value: username,
							},
							Password: piav1alpha1.WireguardClientConfigValue{
								Value: password,
							},
						},
					}
					Expect(k8sClient.Create(ctx, config)).To(Succeed())
				})

				It("should add an init container", func() {
					Expect(defaulter.Default(ctx, obj)).To(Succeed())

					var container *corev1.Container
					Expect(obj.Spec.InitContainers).To(ContainElement(
						HaveField("Name", "generate-wireguard-config"), container,
					))

					Expect(obj.Spec.Volumes).To(ConsistOf(corev1.Volume{
						Name: "results",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					}))

					Expect(container.Name).To(Equal("generate-config"))
					Expect(container.Image).To(HavePrefix("unstoppablemango/pia-manual-connections:"))
					Expect(container.Env).To(ConsistOf(
						corev1.EnvVar{Name: "PIA_USER", Value: username},
						corev1.EnvVar{Name: "PIA_PASS", Value: password},
						corev1.EnvVar{Name: "PIA_PF", Value: "false"},
						corev1.EnvVar{Name: "PIA_CONNECT", Value: "false"},
						corev1.EnvVar{Name: "PIA_CONF_PATH", Value: "/out/pia0.conf"},
						corev1.EnvVar{Name: "VPN_PROTOCOL", Value: "wireguard"},
						corev1.EnvVar{Name: "DISABLE_IPV6", Value: "no"},
						corev1.EnvVar{Name: "DIP_TOKEN", Value: "no"},
						corev1.EnvVar{Name: "AUTOCONNECT", Value: "true"},
					))
					Expect(container.VolumeMounts).To(ConsistOf(corev1.VolumeMount{
						Name:      "results",
						MountPath: "/out",
					}))
				})
			})
		})
	})
})
