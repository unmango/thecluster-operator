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

// Package harbor adapts goharbor/go-client to the parts of Harbor's v2.0 API
// the registry controllers use: registry endpoints, projects, and the
// repositories that have to go before a project can be deleted.
package harbor

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-openapi/runtime"
	httptransport "github.com/go-openapi/runtime/client"
	v2 "github.com/goharbor/go-client/pkg/sdk/v2.0/client"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/project"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/registry"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/repository"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/systeminfo"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/user"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/models"
)

// Client talks to one Harbor instance as one user.
type Client struct {
	api *v2.HarborAPI
}

// New returns a Client for the Harbor at baseURL, which excludes /api/v2.0.
func New(baseURL, username, password string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + v2.DefaultBasePath
	// go-client's own ClientSet skips TLS verification unconditionally, so
	// the API is built directly, keeping the default transport.
	api := v2.New(v2.Config{URL: u, AuthInfo: httptransport.BasicAuth(username, password)})
	return &Client{api: api}, nil
}

// IsNotFound reports whether err is a 404 from Harbor.
func IsNotFound(err error) bool {
	// Operations that document a 404 return a typed response; the rest
	// return a runtime.APIError.
	var coded interface{ IsCode(int) bool }
	if errors.As(err, &coded) {
		return coded.IsCode(http.StatusNotFound)
	}
	var api *runtime.APIError
	return errors.As(err, &api) && api.Code == http.StatusNotFound
}

// Credential authenticates Harbor to an upstream registry.
type Credential struct {
	Type         string `json:"type"`
	AccessKey    string `json:"access_key"`
	AccessSecret string `json:"access_secret"`
}

// Registry is a Harbor registry endpoint, the upstream side of a proxy
// cache. Harbor masks AccessSecret when reading one back.
type Registry struct {
	ID         int64       `json:"id,omitempty"`
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	URL        string      `json:"url"`
	Insecure   bool        `json:"insecure"`
	Credential *Credential `json:"credential,omitempty"`
}

// Project is a Harbor project. RegistryID is non-zero on a proxy-cache
// project and cannot change after creation.
type Project struct {
	ProjectID  int64             `json:"project_id"`
	Name       string            `json:"name"`
	RegistryID int64             `json:"registry_id"`
	Metadata   map[string]string `json:"metadata"`
}

// SystemInfo is the subset of /systeminfo the controllers read.
type SystemInfo struct {
	ExternalURL string `json:"external_url"`
	RegistryURL string `json:"registry_url"`
}

// Ping checks the credentials by fetching the current user.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.api.User.GetCurrentUserInfo(ctx, user.NewGetCurrentUserInfoParams())
	return err
}

// SystemInfo returns the instance's public addresses.
func (c *Client) SystemInfo(ctx context.Context) (*SystemInfo, error) {
	resp, err := c.api.Systeminfo.GetSystemInfo(ctx, systeminfo.NewGetSystemInfoParams())
	if err != nil {
		return nil, err
	}
	return &SystemInfo{
		ExternalURL: deref(resp.Payload.ExternalURL),
		RegistryURL: deref(resp.Payload.RegistryURL),
	}, nil
}

// GetRegistry returns the registry endpoint called name, or nil if there is none.
func (c *Client) GetRegistry(ctx context.Context, name string) (*Registry, error) {
	q := "name=" + name
	resp, err := c.api.Registry.ListRegistries(ctx, registry.NewListRegistriesParams().WithQ(&q))
	if err != nil {
		return nil, err
	}
	// The query has matched fuzzily on some Harbor versions, so filter for
	// the exact name.
	for _, r := range resp.Payload {
		if r.Name != name {
			continue
		}
		out := &Registry{ID: r.ID, Name: r.Name, Type: r.Type, URL: r.URL, Insecure: r.Insecure}
		if cred := r.Credential; cred != nil {
			out.Credential = &Credential{Type: cred.Type, AccessKey: cred.AccessKey, AccessSecret: cred.AccessSecret}
		}
		return out, nil
	}
	return nil, nil
}

// CreateRegistry adds a registry endpoint.
func (c *Client) CreateRegistry(ctx context.Context, r *Registry) error {
	m := &models.Registry{Name: r.Name, Type: r.Type, URL: r.URL, Insecure: r.Insecure}
	if cred := r.Credential; cred != nil {
		m.Credential = &models.RegistryCredential{Type: cred.Type, AccessKey: cred.AccessKey, AccessSecret: cred.AccessSecret}
	}
	_, err := c.api.Registry.CreateRegistry(ctx, registry.NewCreateRegistryParams().WithRegistry(m))
	return err
}

// UpdateRegistry sets an endpoint's URL and, when cred is non-nil, its
// credential. A registry's type cannot be updated.
func (c *Client) UpdateRegistry(ctx context.Context, id int64, url string, insecure bool, cred *Credential) error {
	m := &models.RegistryUpdate{URL: &url, Insecure: &insecure}
	if cred != nil {
		m.CredentialType = &cred.Type
		m.AccessKey = &cred.AccessKey
		m.AccessSecret = &cred.AccessSecret
	}
	_, err := c.api.Registry.UpdateRegistry(ctx, registry.NewUpdateRegistryParams().WithID(id).WithRegistry(m))
	return err
}

// DeleteRegistry removes a registry endpoint. Harbor refuses while a
// project still proxies it.
func (c *Client) DeleteRegistry(ctx context.Context, id int64) error {
	_, err := c.api.Registry.DeleteRegistry(ctx, registry.NewDeleteRegistryParams().WithID(id))
	return err
}

var byName = new(true)

// GetProject returns the project called name, or nil if there is none.
func (c *Client) GetProject(ctx context.Context, name string) (*Project, error) {
	params := project.NewGetProjectParams().WithProjectNameOrID(name).WithXIsResourceName(byName)
	resp, err := c.api.Project.GetProject(ctx, params)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := resp.Payload
	out := &Project{ProjectID: int64(p.ProjectID), Name: p.Name, RegistryID: p.RegistryID, Metadata: map[string]string{}}
	if p.Metadata != nil {
		out.Metadata["public"] = p.Metadata.Public
	}
	return out, nil
}

// CreateProxyProject creates a project that proxies registryID.
func (c *Client) CreateProxyProject(ctx context.Context, name string, registryID int64, public bool) error {
	req := &models.ProjectReq{
		ProjectName: name,
		RegistryID:  &registryID,
		Metadata:    &models.ProjectMetadata{Public: strconv.FormatBool(public)},
	}
	_, err := c.api.Project.CreateProject(ctx, project.NewCreateProjectParams().WithProject(req))
	return err
}

// SetProjectPublic changes whether anonymous clients can pull from a project.
func (c *Client) SetProjectPublic(ctx context.Context, name string, public bool) error {
	req := &models.ProjectReq{Metadata: &models.ProjectMetadata{Public: strconv.FormatBool(public)}}
	params := project.NewUpdateProjectParams().WithProjectNameOrID(name).WithXIsResourceName(byName).WithProject(req)
	_, err := c.api.Project.UpdateProject(ctx, params)
	return err
}

// DeleteProject deletes a project along with every repository in it, since
// Harbor refuses to delete a project that still holds any.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	pageSize := int64(100)
	for {
		params := repository.NewListRepositoriesParams().WithProjectName(name).WithPageSize(&pageSize)
		resp, err := c.api.Repository.ListRepositories(ctx, params)
		if IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(resp.Payload) == 0 {
			break
		}
		for _, repo := range resp.Payload {
			// Harbor wants the slashes in a repository name encoded twice,
			// and go-client encodes path parameters once.
			rel := url.PathEscape(strings.TrimPrefix(repo.Name, name+"/"))
			params := repository.NewDeleteRepositoryParams().WithProjectName(name).WithRepositoryName(rel)
			if _, err := c.api.Repository.DeleteRepository(ctx, params); err != nil && !IsNotFound(err) {
				return err
			}
		}
	}
	params := project.NewDeleteProjectParams().WithProjectNameOrID(name).WithXIsResourceName(byName)
	_, err := c.api.Project.DeleteProject(ctx, params)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
