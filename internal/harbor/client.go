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

// Package harbor is a minimal client for the parts of Harbor's v2.0 API the
// registry controllers use: registry endpoints, projects, and the
// repositories that have to go before a project can be deleted.
package harbor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one Harbor instance as one user.
type Client struct {
	base     string
	username string
	password string
	http     *http.Client
}

// New returns a Client for the Harbor at baseURL, which excludes /api/v2.0.
func New(baseURL, username, password string) *Client {
	return &Client{
		base:     strings.TrimSuffix(baseURL, "/") + "/api/v2.0",
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Error is a non-2xx response from Harbor.
type Error struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("harbor: %s %s: %d %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// IsNotFound reports whether err is a 404 from Harbor.
func IsNotFound(err error) bool {
	var herr *Error
	return errors.As(err, &herr) && herr.Status == http.StatusNotFound
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
	return c.do(ctx, http.MethodGet, "/users/current", nil, nil, nil)
}

// SystemInfo returns the instance's public addresses.
func (c *Client) SystemInfo(ctx context.Context) (*SystemInfo, error) {
	info := &SystemInfo{}
	if err := c.do(ctx, http.MethodGet, "/systeminfo", nil, nil, info); err != nil {
		return nil, err
	}
	return info, nil
}

// GetRegistry returns the registry endpoint called name, or nil if there is none.
func (c *Client) GetRegistry(ctx context.Context, name string) (*Registry, error) {
	var found []Registry
	path := "/registries?q=" + url.QueryEscape("name="+name)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &found); err != nil {
		return nil, err
	}
	// q=name=x is a fuzzy match, so filter for the exact name.
	for i := range found {
		if found[i].Name == name {
			return &found[i], nil
		}
	}
	return nil, nil
}

// CreateRegistry adds a registry endpoint.
func (c *Client) CreateRegistry(ctx context.Context, r *Registry) error {
	return c.do(ctx, http.MethodPost, "/registries", nil, r, nil)
}

// UpdateRegistry sets an endpoint's URL and, when cred is non-nil, its
// credential. A registry's type cannot be updated.
func (c *Client) UpdateRegistry(ctx context.Context, id int64, url string, insecure bool, cred *Credential) error {
	body := map[string]any{"url": url, "insecure": insecure}
	if cred != nil {
		body["credential_type"] = cred.Type
		body["access_key"] = cred.AccessKey
		body["access_secret"] = cred.AccessSecret
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/registries/%d", id), nil, body, nil)
}

// DeleteRegistry removes a registry endpoint. Harbor refuses while a
// project still proxies it.
func (c *Client) DeleteRegistry(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/registries/%d", id), nil, nil, nil)
}

var byName = http.Header{"X-Is-Resource-Name": []string{"true"}}

// GetProject returns the project called name, or nil if there is none.
func (c *Client) GetProject(ctx context.Context, name string) (*Project, error) {
	p := &Project{}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(name), byName, nil, p)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CreateProxyProject creates a project that proxies registryID.
func (c *Client) CreateProxyProject(ctx context.Context, name string, registryID int64, public bool) error {
	body := map[string]any{
		"project_name": name,
		"registry_id":  registryID,
		"metadata":     map[string]string{"public": fmt.Sprint(public)},
	}
	return c.do(ctx, http.MethodPost, "/projects", nil, body, nil)
}

// SetProjectPublic changes whether anonymous clients can pull from a project.
func (c *Client) SetProjectPublic(ctx context.Context, name string, public bool) error {
	body := map[string]any{"metadata": map[string]string{"public": fmt.Sprint(public)}}
	return c.do(ctx, http.MethodPut, "/projects/"+url.PathEscape(name), byName, body, nil)
}

// DeleteProject deletes a project along with every repository in it, since
// Harbor refuses to delete a project that still holds any.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	project := url.PathEscape(name)
	for {
		var repos []struct {
			Name string `json:"name"`
		}
		if err := c.do(ctx, http.MethodGet, "/projects/"+project+"/repositories?page_size=100", nil, nil, &repos); err != nil {
			if IsNotFound(err) {
				return nil
			}
			return err
		}
		if len(repos) == 0 {
			break
		}
		for _, repo := range repos {
			// The repository name is relative to the project, and its slashes
			// have to arrive encoded, so they are escaped twice.
			rel := strings.TrimPrefix(repo.Name, name+"/")
			path := "/projects/" + project + "/repositories/" + url.PathEscape(url.PathEscape(rel))
			if err := c.do(ctx, http.MethodDelete, path, nil, nil, nil); err != nil && !IsNotFound(err) {
				return err
			}
		}
	}
	err := c.do(ctx, http.MethodDelete, "/projects/"+project, byName, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path string, header http.Header, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &Error{Method: method, Path: path, Status: resp.StatusCode, Body: string(msg)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
