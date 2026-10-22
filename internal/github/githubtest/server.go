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

// Package githubtest is an in-memory stand-in for the parts of GitHub's API
// that package github calls.
package githubtest

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"
)

// Server is a fake GitHub. Its fields may be read and changed between
// requests while holding Mu.
type Server struct {
	*httptest.Server

	Mu sync.Mutex
	// Token is the personal access token /user/repos accepts.
	Token string
	// AppKey verifies the JWTs an App signs, and Installation is the one
	// installation it may get a token for.
	AppKey       *rsa.PublicKey
	Installation string
	// Repositories are what both listings return, in pages.
	Repositories []*github.Repository
}

const installationToken = "installation-token"

// New starts a fake GitHub that accepts token.
func New(token string) *Server {
	s := &Server{Token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", s.accessToken)
	mux.HandleFunc("GET /installation/repositories", s.installationRepositories)
	mux.HandleFunc("GET /user/repos", s.userRepos)
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *Server) accessToken(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.AppKey == nil || !verify(jwt, s.AppKey) {
		http.Error(w, `{"message":"A JSON web token could not be decoded"}`, http.StatusUnauthorized)
		return
	}
	if r.PathValue("id") != s.Installation {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      installationToken,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

func verify(jwt string, key *rsa.PublicKey) bool {
	i := strings.LastIndex(jwt, ".")
	if i < 0 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(jwt[i+1:])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(jwt[:i]))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) == nil
}

func (s *Server) installationRepositories(w http.ResponseWriter, r *http.Request) {
	if !authorized(r, installationToken) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	repos := s.page(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(repos), "repositories": repos})
}

func (s *Server) userRepos(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	token := s.Token
	s.Mu.Unlock()
	if !authorized(r, token) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, s.page(w, r))
}

func authorized(r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	return token != "" && (auth == "token "+token || auth == "Bearer "+token)
}

// page returns the requested page and links the next one, as GitHub does.
func (s *Server) page(w http.ResponseWriter, r *http.Request) []*github.Repository {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if size <= 0 {
		size = 30
	}
	if page <= 0 {
		page = 1
	}
	start := min((page-1)*size, len(s.Repositories))
	end := min(start+size, len(s.Repositories))
	if end < len(s.Repositories) {
		next := *r.URL
		q := next.Query()
		q.Set("page", strconv.Itoa(page+1))
		next.RawQuery = q.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<%s%s>; rel="next"`, s.URL, next.RequestURI()))
	}
	return s.Repositories[start:end]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Repo is a repository owned by owner.
func Repo(owner, name string) *github.Repository {
	return &github.Repository{
		Name:    new(name),
		HTMLURL: new("https://github.com/" + owner + "/" + name),
		Owner:   &github.User{Login: new(owner)},
	}
}
