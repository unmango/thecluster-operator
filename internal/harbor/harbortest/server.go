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

// Package harbortest is an in-memory stand-in for the parts of Harbor's API
// that package harbor calls.
package harbortest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/unmango/thecluster-operator/internal/harbor"
)

// Server is a fake Harbor. Its fields may be read and changed between
// requests while holding Mu.
type Server struct {
	*httptest.Server

	Mu         sync.Mutex
	Password   string
	Host       string
	Registries map[int64]*harbor.Registry
	Projects   map[string]*harbor.Project
	// Repositories maps a project name to its repository names, which
	// include the project prefix as Harbor's API returns them.
	Repositories map[string][]string

	nextID int64
}

// New starts a fake Harbor that accepts admin with password.
func New(password string) *Server {
	s := &Server{
		Password:     password,
		Host:         "harbor.example.com",
		Registries:   map[int64]*harbor.Registry{},
		Projects:     map[string]*harbor.Project{},
		Repositories: map[string][]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2.0/users/current", s.ok)
	mux.HandleFunc("GET /api/v2.0/systeminfo", s.systemInfo)
	mux.HandleFunc("GET /api/v2.0/registries", s.listRegistries)
	mux.HandleFunc("POST /api/v2.0/registries", s.createRegistry)
	mux.HandleFunc("PUT /api/v2.0/registries/{id}", s.updateRegistry)
	mux.HandleFunc("DELETE /api/v2.0/registries/{id}", s.deleteRegistry)
	mux.HandleFunc("GET /api/v2.0/projects/{name}", s.getProject)
	mux.HandleFunc("POST /api/v2.0/projects", s.createProject)
	mux.HandleFunc("PUT /api/v2.0/projects/{name}", s.updateProject)
	mux.HandleFunc("DELETE /api/v2.0/projects/{name}", s.deleteProject)
	mux.HandleFunc("GET /api/v2.0/projects/{name}/repositories", s.listRepositories)
	mux.HandleFunc("DELETE /api/v2.0/projects/{name}/repositories/{repo}", s.deleteRepository)

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "admin" || pass != s.Password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.Mu.Lock()
		defer s.Mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	return s
}

// Registry returns the registry endpoint called name, or nil.
func (s *Server) Registry(name string) *harbor.Registry {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for _, r := range s.Registries {
		if r.Name == name {
			c := *r
			return &c
		}
	}
	return nil
}

// Project returns the project called name, or nil.
func (s *Server) Project(name string) *harbor.Project {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if p, ok := s.Projects[name]; ok {
		c := *p
		return &c
	}
	return nil
}

func (s *Server) ok(w http.ResponseWriter, _ *http.Request) {
	reply(w, map[string]string{"username": "admin"})
}

func (s *Server) systemInfo(w http.ResponseWriter, _ *http.Request) {
	reply(w, harbor.SystemInfo{RegistryURL: s.Host, ExternalURL: "https://" + s.Host})
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Query().Get("q"), "name=")
	out := []harbor.Registry{}
	for _, reg := range s.Registries {
		if strings.Contains(reg.Name, name) {
			out = append(out, *reg)
		}
	}
	reply(w, out)
}

func (s *Server) createRegistry(w http.ResponseWriter, r *http.Request) {
	reg := &harbor.Registry{}
	if !decode(w, r, reg) {
		return
	}
	for _, existing := range s.Registries {
		if existing.Name == reg.Name {
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
	}
	s.nextID++
	reg.ID = s.nextID
	s.Registries[reg.ID] = reg
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) registry(w http.ResponseWriter, r *http.Request) *harbor.Registry {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	reg, ok := s.Registries[id]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
	}
	return reg
}

func (s *Server) updateRegistry(w http.ResponseWriter, r *http.Request) {
	reg := s.registry(w, r)
	if reg == nil {
		return
	}
	var body struct {
		URL          *string `json:"url"`
		CredType     *string `json:"credential_type"`
		AccessKey    *string `json:"access_key"`
		AccessSecret *string `json:"access_secret"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.URL != nil {
		reg.URL = *body.URL
	}
	if body.CredType != nil {
		reg.Credential = &harbor.Credential{Type: *body.CredType, AccessKey: *body.AccessKey, AccessSecret: *body.AccessSecret}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteRegistry(w http.ResponseWriter, r *http.Request) {
	reg := s.registry(w, r)
	if reg == nil {
		return
	}
	for _, p := range s.Projects {
		if p.RegistryID == reg.ID {
			http.Error(w, "registry in use", http.StatusPreconditionFailed)
			return
		}
	}
	delete(s.Registries, reg.ID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.Projects[r.PathValue("name")]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	reply(w, p)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string            `json:"project_name"`
		RegistryID int64             `json:"registry_id"`
		Metadata   map[string]string `json:"metadata"`
	}
	if !decode(w, r, &body) {
		return
	}
	if _, ok := s.Projects[body.Name]; ok {
		http.Error(w, "conflict", http.StatusConflict)
		return
	}
	if _, ok := s.Registries[body.RegistryID]; !ok && body.RegistryID != 0 {
		http.Error(w, "no such registry", http.StatusBadRequest)
		return
	}
	s.nextID++
	s.Projects[body.Name] = &harbor.Project{ProjectID: s.nextID, Name: body.Name, RegistryID: body.RegistryID, Metadata: body.Metadata}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.Projects[r.PathValue("name")]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var body struct {
		Metadata map[string]string `json:"metadata"`
	}
	if !decode(w, r, &body) {
		return
	}
	for k, v := range body.Metadata {
		p.Metadata[k] = v
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.Projects[name]; !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if len(s.Repositories[name]) > 0 {
		http.Error(w, "project contains repositories", http.StatusPreconditionFailed)
		return
	}
	delete(s.Projects, name)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) listRepositories(w http.ResponseWriter, r *http.Request) {
	out := []map[string]string{}
	for _, repo := range s.Repositories[r.PathValue("name")] {
		out = append(out, map[string]string{"name": repo})
	}
	reply(w, out)
}

func (s *Server) deleteRepository(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("name")
	// Harbor wants the slashes in a repository name encoded twice, so after
	// the mux decodes once there is still one level left.
	rel, err := url.PathUnescape(r.PathValue("repo"))
	if err != nil || strings.Contains(r.PathValue("repo"), "/") {
		http.Error(w, "repository name not double-encoded", http.StatusBadRequest)
		return
	}
	repos := s.Repositories[project]
	for i, repo := range repos {
		if repo == project+"/"+rel {
			s.Repositories[project] = append(repos[:i], repos[i+1:]...)
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
