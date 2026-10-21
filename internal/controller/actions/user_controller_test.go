/*
Copyright 2026.

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

package actions

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gogithub "github.com/google/go-github/v93/github"
	actionsv1alpha1 "github.com/unmango/thecluster-operator/api/actions/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/github"
	"github.com/unmango/thecluster-operator/internal/github/githubtest"
)

var _ = Describe("User Controller", func() {
	const login = "UnstoppableMango"

	var (
		name       string
		suffix     string
		user       *actionsv1alpha1.User
		reconciler *UserReconciler
		gh         *githubtest.Server
		secretKey  = types.NamespacedName{Namespace: "default", Name: "gh-token"}
	)

	doReconcile := func() reconcile.Result {
		GinkgoHelper()
		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	current := func() *actionsv1alpha1.User {
		GinkgoHelper()
		got := &actionsv1alpha1.User{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		return got
	}

	ready := func() *metav1.Condition {
		GinkgoHelper()
		return meta.FindStatusCondition(current().Status.Conditions, TypeReady)
	}

	repository := func(repo string) *actionsv1alpha1.Repository {
		GinkgoHelper()
		got := &actionsv1alpha1.Repository{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: repo}, got)).To(Succeed())
		return got
	}

	gone := func(repo string) bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: repo}, &actionsv1alpha1.Repository{})
		return apierrors.IsNotFound(err)
	}

	create := func() {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, user)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, user))).To(Succeed())
		})
	}

	// Repositories are cluster-scoped and outlive a test, so every test names
	// its repositories apart.
	repo := func(base string) string { return base + "-" + suffix }

	BeforeEach(func() {
		suffix = utilrand.String(6)
		name = "user-" + suffix

		gh = githubtest.New("token")
		DeferCleanup(gh.Close)

		reconciler = &UserReconciler{
			Client:       k8sClient,
			Scheme:       k8sClient.Scheme(),
			Defaults:     Defaults{GitHubConfigSecret: secretKey},
			GitHubURL:    "https://github.com",
			GitHubAPIURL: gh.URL,
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: secretKey.Namespace, Name: secretKey.Name},
			StringData: map[string]string{github.KeyToken: "token"},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, secret))).To(Succeed())

		user = &actionsv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       actionsv1alpha1.UserSpec{Login: login},
		}
	})

	It("generates the Repositories it lists", func() {
		user.Spec.Repositories = []string{repo("dotfiles"), repo("Some_Repo")}
		user.Spec.Template.MaxRunners = ptr.To[int32](2)
		create()

		res := doReconcile()
		Expect(res.RequeueAfter).To(BeZero())

		got := repository(repo("dotfiles"))
		Expect(got.Spec.URL).To(Equal("https://github.com/UnstoppableMango/" + repo("dotfiles")))
		Expect(got.Spec.MaxRunners).To(Equal(ptr.To[int32](2)))
		Expect(got.Labels).To(HaveKeyWithValue(UserLabel, name))
		Expect(metav1.IsControlledBy(got, current())).To(BeTrue())

		Expect(repository(repo("some-repo")).Spec.URL).To(HaveSuffix("/" + repo("Some_Repo")))
		Expect(current().Status.Repositories).To(Equal([]string{repo("dotfiles"), repo("some-repo")}))
		Expect(ready().Status).To(Equal(metav1.ConditionTrue))
		Expect(ready().Reason).To(Equal("Synced"))
	})

	It("keeps generated Repositories in step with the template", func() {
		user.Spec.Repositories = []string{repo("dotfiles")}
		create()
		doReconcile()

		got := current()
		got.Spec.Template.RunnerGroup = "nix"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		doReconcile()

		Expect(repository(repo("dotfiles")).Spec.RunnerGroup).To(Equal("nix"))
	})

	It("deletes the Repositories it no longer selects", func() {
		user.Spec.Repositories = []string{repo("keep"), repo("drop")}
		create()
		doReconcile()
		Expect(gone(repo("drop"))).To(BeFalse())

		got := current()
		got.Spec.Repositories = []string{repo("keep")}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		doReconcile()

		Expect(gone(repo("drop"))).To(BeTrue())
		Expect(gone(repo("keep"))).To(BeFalse())
		Expect(current().Status.Repositories).To(Equal([]string{repo("keep")}))
	})

	It("leaves alone a Repository it did not create", func() {
		taken := &actionsv1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{Name: repo("taken")},
			Spec:       actionsv1alpha1.RepositorySpec{URL: "https://github.com/someone/else"},
		}
		Expect(k8sClient.Create(ctx, taken)).To(Succeed())
		user.Spec.Repositories = []string{repo("taken")}
		create()
		doReconcile()

		Expect(repository(repo("taken")).Spec.URL).To(Equal("https://github.com/someone/else"))
		Expect(current().Status.Conflicts).To(Equal([]string{repo("taken")}))
		Expect(ready().Reason).To(Equal("Conflicts"))
	})

	Context("with discovery", func() {
		BeforeEach(func() {
			user.Spec.Discover = true
			user.Spec.Interval = &metav1.Duration{Duration: 5 * time.Minute}

			archived := githubtest.Repo(login, repo("old"))
			archived.Archived = new(true)
			fork := githubtest.Repo(login, repo("fork"))
			fork.Fork = new(true)
			gh.Repositories = []*gogithub.Repository{
				githubtest.Repo(login, repo("dotfiles")),
				githubtest.Repo(login, repo("Charts")),
				githubtest.Repo(login, repo("scratch")),
				githubtest.Repo("someone-else", repo("theirs")),
				archived,
				fork,
			}
		})

		It("generates a Repository for each repository it finds", func() {
			user.Spec.Exclude = []string{"scratch-*"}
			user.Spec.Repositories = []string{repo("listed")}
			create()

			res := doReconcile()
			Expect(res.RequeueAfter).To(Equal(5 * time.Minute))

			got := current()
			Expect(got.Status.Repositories).To(ConsistOf(repo("dotfiles"), repo("charts"), repo("listed")))
			Expect(got.Status.LastDiscoveryTime).NotTo(BeNil())
			Expect(repository(repo("charts")).Spec.URL).To(Equal("https://github.com/UnstoppableMango/" + repo("Charts")))
		})

		It("keeps archived repositories and forks when asked", func() {
			user.Spec.Include = []string{"old-*", "fork-*"}
			user.Spec.IncludeArchived = true
			user.Spec.IncludeForks = true
			create()

			doReconcile()
			Expect(current().Status.Repositories).To(ConsistOf(repo("old"), repo("fork")))
		})

		It("reports missing credentials", func() {
			reconciler.Defaults.GitHubConfigSecret = types.NamespacedName{Namespace: "default", Name: "missing"}
			user.Spec.Repositories = []string{repo("listed")}
			create()

			res := doReconcile()
			Expect(res.RequeueAfter).To(Equal(retryInterval))
			Expect(ready().Status).To(Equal(metav1.ConditionFalse))
			Expect(ready().Reason).To(Equal("NoCredentials"))
		})

		It("reports a failed discovery", func() {
			gh.Mu.Lock()
			gh.Token = "rotated"
			gh.Mu.Unlock()
			create()

			doReconcile()
			Expect(ready().Reason).To(Equal("DiscoveryFailed"))
		})

		It("reports a filter that is not a glob", func() {
			user.Spec.Include = []string{"["}
			create()

			doReconcile()
			Expect(ready().Reason).To(Equal("InvalidFilter"))
		})
	})
})

var _ = Describe("RepositoryName", func() {
	It("makes a repository name fit an object name", func() {
		Expect(RepositoryName("the-cluster")).To(Equal("the-cluster"))
		Expect(RepositoryName("Some_Repo.js")).To(Equal("some-repo-js"))
		Expect(RepositoryName(".github")).To(Equal("github"))
	})
})
