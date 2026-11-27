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
	"time"

	"github.com/go-openapi/runtime"
	httptransport "github.com/go-openapi/runtime/client"
	v2 "github.com/goharbor/go-client/pkg/sdk/v2.0/client"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/project"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/registry"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/repository"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/robot"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/systeminfo"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/user"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/models"
)

// timeout bounds each request. go-client only applies its own deadline to a
// call made without a context, and its HTTP client has none.
const timeout = 30 * time.Second

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
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is a 409 from Harbor.
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

func hasStatus(err error, code int) bool {
	// Operations that document a status return a typed response; the rest
	// return a runtime.APIError.
	var coded interface{ IsCode(int) bool }
	if errors.As(err, &coded) {
		return coded.IsCode(code)
	}
	var api *runtime.APIError
	return errors.As(err, &api) && api.Code == code
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

// Robot is a Harbor robot account. Name is the full username Harbor
// assigns, robot$<project>+<name> for a project robot, and Secret is only
// filled on creation or refresh.
type Robot struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Level       string            `json:"level"`
	Duration    int64             `json:"duration"`
	Permissions []RobotPermission `json:"permissions"`
	Secret      string            `json:"secret,omitempty"`
}

// RobotPermission grants a robot actions on resources in one project.
type RobotPermission struct {
	Kind      string        `json:"kind"`
	Namespace string        `json:"namespace"`
	Access    []RobotAccess `json:"access"`
}

// RobotAccess is one action on one kind of resource.
type RobotAccess struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// SystemInfo is the subset of /systeminfo the controllers read.
type SystemInfo struct {
	ExternalURL string `json:"external_url"`
	RegistryURL string `json:"registry_url"`
}

// Ping checks the credentials by fetching the current user.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := c.api.User.GetCurrentUserInfo(ctx, user.NewGetCurrentUserInfoParams())
	return err
}

// SystemInfo returns the instance's public addresses.
func (c *Client) SystemInfo(ctx context.Context) (*SystemInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := c.api.Registry.DeleteRegistry(ctx, registry.NewDeleteRegistryParams().WithID(id))
	return err
}

var byName = new(true)

// GetProject returns the project called name, or nil if there is none.
func (c *Client) GetProject(ctx context.Context, name string) (*Project, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
	return c.createProject(ctx, name, &registryID, public)
}

// CreateProject creates a project that holds what is pushed to it.
func (c *Client) CreateProject(ctx context.Context, name string, public bool) error {
	return c.createProject(ctx, name, nil, public)
}

func (c *Client) createProject(ctx context.Context, name string, registryID *int64, public bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := &models.ProjectReq{
		ProjectName: name,
		RegistryID:  registryID,
		Metadata:    &models.ProjectMetadata{Public: strconv.FormatBool(public)},
	}
	_, err := c.api.Project.CreateProject(ctx, project.NewCreateProjectParams().WithProject(req))
	return err
}

// SetProjectPublic changes whether anonymous clients can pull from a project.
func (c *Client) SetProjectPublic(ctx context.Context, name string, public bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := c.api.Repository.ListRepositories(reqCtx, params)
		cancel()
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
			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			_, err := c.api.Repository.DeleteRepository(reqCtx, params)
			cancel()
			if err != nil && !IsNotFound(err) {
				return err
			}
		}
	}
	return c.DeleteEmptyProject(ctx, name)
}

// DeleteEmptyProject deletes a project, which Harbor refuses while it holds
// any repository. A project that is already gone is not an error.
func (c *Client) DeleteEmptyProject(ctx context.Context, name string) error {
	params := project.NewDeleteProjectParams().WithProjectNameOrID(name).WithXIsResourceName(byName)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := c.api.Project.DeleteProject(ctx, params)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// CreateRobot creates a robot and returns it with its ID, full name and secret.
func (c *Client) CreateRobot(ctx context.Context, r *Robot) (*Robot, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	m := &models.RobotCreate{Name: r.Name, Level: r.Level, Duration: r.Duration}
	for _, p := range r.Permissions {
		perm := &models.RobotPermission{Kind: p.Kind, Namespace: p.Namespace}
		for _, a := range p.Access {
			perm.Access = append(perm.Access, &models.Access{Resource: a.Resource, Action: a.Action})
		}
		m.Permissions = append(m.Permissions, perm)
	}
	resp, err := c.api.Robot.CreateRobot(ctx, robot.NewCreateRobotParams().WithRobot(m))
	if err != nil {
		return nil, err
	}
	out := *r
	out.ID, out.Name, out.Secret = resp.Payload.ID, resp.Payload.Name, resp.Payload.Secret
	return &out, nil
}

// RobotExists reports whether the robot with id is still in Harbor.
func (c *Client) RobotExists(ctx context.Context, id int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := c.api.Robot.GetRobotByID(ctx, robot.NewGetRobotByIDParams().WithRobotID(id))
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// FindRobot returns the id of the robot whose full name is name, or 0.
func (c *Client) FindRobot(ctx context.Context, name string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	q := "name=" + name
	resp, err := c.api.Robot.ListRobot(ctx, robot.NewListRobotParams().WithQ(&q))
	if err != nil {
		return 0, err
	}
	for _, r := range resp.Payload {
		if r.Name == name {
			return r.ID, nil
		}
	}
	return 0, nil
}

// RefreshRobotSecret replaces a robot's secret and returns the new one.
func (c *Client) RefreshRobotSecret(ctx context.Context, id int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	params := robot.NewRefreshSecParams().WithRobotID(id).WithRobotSec(&models.RobotSec{})
	resp, err := c.api.Robot.RefreshSec(ctx, params)
	if err != nil {
		return "", err
	}
	return resp.Payload.Secret, nil
}

// DeleteRobot deletes a robot; one already gone is not an error.
func (c *Client) DeleteRobot(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := c.api.Robot.DeleteRobot(ctx, robot.NewDeleteRobotParams().WithRobotID(id))
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
