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
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
	"github.com/unmango/thecluster-operator/internal/harbor/harbortest"
	"github.com/unmango/thecluster-operator/internal/secretstore"
)

var _ = Describe("RegistryCredential Controller", func() {
	var (
		fake  *harbortest.Server
		store *secretstore.Memory
		now   = time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	)

	BeforeEach(func(ctx SpecContext) {
		fake = harbortest.New("secret")
		DeferCleanup(fake.Close)
		store = secretstore.NewMemory()

		reg := newRegistry(ctx, "harbor", fake)
		_, err := (&RegistryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}).
			Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reg)})
		Expect(err).NotTo(HaveOccurred())

		project := &registryv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Name: "mve", Namespace: "default"},
			Spec:       registryv1alpha1.ProjectSpec{RegistryRef: corev1.LocalObjectReference{Name: reg.Name}},
		}
		Expect(k8sClient.Create(ctx, project)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { removeFinalized(ctx, project) })
		_, err = (&ProjectReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}).
			Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(project)})
		Expect(err).NotTo(HaveOccurred())
	})

	newCredential := func(ctx context.Context, namespace string, access registryv1alpha1.Access) *registryv1alpha1.RegistryCredential {
		rc := &registryv1alpha1.RegistryCredential{
			ObjectMeta: metav1.ObjectMeta{Name: "mve-" + string(access), Namespace: namespace},
			Spec: registryv1alpha1.RegistryCredentialSpec{
				ProjectRef: corev1.LocalObjectReference{Name: "mve"},
				Access:     access,
				SecretName: "harbor-mve-" + string(access),
			},
		}
		Expect(k8sClient.Create(ctx, rc)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { removeFinalized(ctx, rc) })
		return rc
	}

	reconcileCredential := func(ctx context.Context, rc *registryv1alpha1.RegistryCredential) *registryv1alpha1.RegistryCredential {
		r := &RegistryCredentialReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			Store: store, Clock: clocktesting.NewFakePassiveClock(now),
		}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rc)})
		Expect(err).NotTo(HaveOccurred())
		out := &registryv1alpha1.RegistryCredential{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rc), out); apierrors.IsNotFound(err) {
			return nil
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		return out
	}

	storedLogin := func(name string) map[string]string {
		value, ok := store.Get(name)
		Expect(ok).To(BeTrue(), "secret %s is not in the store", name)
		login := map[string]string{}
		Expect(json.Unmarshal([]byte(value), &login)).To(Succeed())
		return login
	}

	actions := func(robot *harbor.Robot) []string {
		Expect(robot.Permissions).To(HaveLen(1))
		Expect(robot.Permissions[0].Namespace).To(Equal("mve"))
		var out []string
		for _, a := range robot.Permissions[0].Access {
			Expect(a.Resource).To(Equal("repository"))
			out = append(out, a.Action)
		}
		return out
	}

	It("writes a push robot's login to the store", func(ctx SpecContext) {
		rc := reconcileCredential(ctx, newCredential(ctx, "default", registryv1alpha1.AccessPush))
		Expect(meta.IsStatusConditionTrue(rc.Status.Conditions, TypeReady)).To(BeTrue())

		robot := fake.Robot("robot$mve+mve-push-1")
		Expect(robot).NotTo(BeNil())
		Expect(actions(robot)).To(ConsistOf("pull", "push"))
		Expect(robot.Duration).To(Equal(int64(90)))

		Expect(storedLogin("default-harbor-mve-push")).To(Equal(map[string]string{
			"registry": "harbor.example.com", "username": robot.Name, "password": robot.Secret,
		}))
		Expect(rc.Status.Robot.ID).To(Equal(robot.ID))
		Expect(rc.Status.Robot.ExpiresAt.Time).To(BeTemporally("==", now.Add(90*24*time.Hour)))
	})

	It("gives a pull credential no push", func(ctx SpecContext) {
		reconcileCredential(ctx, newCredential(ctx, "default", registryv1alpha1.AccessPull))
		Expect(actions(fake.Robot("robot$mve+mve-pull-1"))).To(ConsistOf("pull"))
	})

	It("cannot reach a Project in another namespace", func(ctx SpecContext) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())

		rc := reconcileCredential(ctx, newCredential(ctx, "other", registryv1alpha1.AccessPush))
		cond := meta.FindStatusCondition(rc.Status.Conditions, TypeReady)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring("not found"))
		Expect(fake.Robots).To(BeEmpty())
	})

	It("retries a failed write, keeping the robot", func(ctx SpecContext) {
		store.Fail = errors.New("vault unreachable")
		rc := reconcileCredential(ctx, newCredential(ctx, "default", registryv1alpha1.AccessPush))

		cond := meta.FindStatusCondition(rc.Status.Conditions, TypeReady)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring("vault unreachable"))
		Expect(rc.Status.Robot).NotTo(BeNil())
		Expect(rc.Status.Robot.Written).To(BeNil())

		store.Fail = nil
		rc = reconcileCredential(ctx, rc)
		Expect(meta.IsStatusConditionTrue(rc.Status.Conditions, TypeReady)).To(BeTrue())
		robot := fake.Robot("robot$mve+mve-push-1")
		Expect(robot.ID).To(Equal(rc.Status.Robot.ID))
		Expect(storedLogin("default-harbor-mve-push")).To(HaveKeyWithValue("password", robot.Secret))
	})

	It("deletes the robot and the stored login on deletion", func(ctx SpecContext) {
		rc := newCredential(ctx, "default", registryv1alpha1.AccessPush)
		reconcileCredential(ctx, rc)

		Expect(k8sClient.Delete(ctx, rc)).To(Succeed())
		Expect(reconcileCredential(ctx, rc)).To(BeNil())
		Expect(fake.Robots).To(BeEmpty())
		_, ok := store.Get("default-harbor-mve-push")
		Expect(ok).To(BeFalse())
	})
})

// removeFinalized drops obj's finalizers and deletes it, so a failed spec
// cannot leave it behind.
func removeFinalized(ctx context.Context, obj client.Object) {
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err == nil {
		obj.SetFinalizers(nil)
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
	}
}
