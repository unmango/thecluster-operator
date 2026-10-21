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

package github_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/unmango/thecluster-operator/internal/github"
	"github.com/unmango/thecluster-operator/internal/github/githubtest"
)

func repos(n int) []*gogithub.Repository {
	out := make([]*gogithub.Repository, n)
	for i := range out {
		out[i] = githubtest.Repo("someone", fmt.Sprintf("repo-%d", i))
	}
	return out
}

func TestTokenPages(t *testing.T) {
	srv := githubtest.New("secret")
	defer srv.Close()
	srv.Repositories = repos(250)

	c, err := github.FromSecret(srv.URL, map[string][]byte{github.KeyToken: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Repositories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 250 || got[249].GetName() != "repo-249" {
		t.Fatalf("got %d repositories, want all 250", len(got))
	}
}

func TestBadToken(t *testing.T) {
	srv := githubtest.New("secret")
	defer srv.Close()

	c, err := github.FromSecret(srv.URL, map[string][]byte{github.KeyToken: []byte("wrong")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Repositories(t.Context()); err == nil {
		t.Fatal("listed repositories with a bad token")
	}
}

func TestApp(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := githubtest.New("")
	defer srv.Close()
	srv.AppKey = &key.PublicKey
	srv.Installation = "42"
	srv.Repositories = repos(3)

	for name, der := range map[string]func() ([]byte, string){
		"pkcs1": func() ([]byte, string) { return x509.MarshalPKCS1PrivateKey(key), "RSA PRIVATE KEY" },
		"pkcs8": func() ([]byte, string) {
			b, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				t.Fatal(err)
			}
			return b, "PRIVATE KEY"
		},
	} {
		t.Run(name, func(t *testing.T) {
			b, typ := der()
			c, err := github.FromSecret(srv.URL, map[string][]byte{
				github.KeyAppID:             []byte("1234"),
				github.KeyAppInstallationID: []byte("42"),
				github.KeyAppPrivateKey:     pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}),
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Repositories(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 {
				t.Fatalf("got %d repositories, want 3", len(got))
			}
		})
	}
}

func TestNonNumericAppID(t *testing.T) {
	_, err := github.FromSecret(github.DefaultAPIURL, map[string][]byte{
		github.KeyAppID:             []byte("Iv1.abc"),
		github.KeyAppInstallationID: []byte("42"),
		github.KeyAppPrivateKey:     []byte("key"),
	})
	if err == nil {
		t.Fatal("accepted an App ID that is not a number")
	}
}

func TestIncompleteSecret(t *testing.T) {
	_, err := github.FromSecret(github.DefaultAPIURL, map[string][]byte{github.KeyAppID: []byte("1234")})
	if err == nil {
		t.Fatal("accepted a Secret with only an App ID")
	}
}
