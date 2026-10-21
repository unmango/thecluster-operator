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

// Package github lists the repositories User discovery selects from, with
// credentials in the actions-runner-controller's githubConfigSecret format,
// so discovery and the scale sets share one Secret.
package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v93/github"
)

// DefaultAPIURL is GitHub.com's REST API.
const DefaultAPIURL = "https://api.github.com"

// The keys of an actions-runner-controller githubConfigSecret.
const (
	KeyToken             = "github_token"
	KeyAppID             = "github_app_id"
	KeyAppInstallationID = "github_app_installation_id"
	KeyAppPrivateKey     = "github_app_private_key"
)

// Client lists repositories as one GitHub App installation or token.
type Client struct {
	gh  *github.Client
	app bool
}

// FromSecret returns a Client for the GitHub REST API at apiURL with the
// credentials in data, the contents of a githubConfigSecret: a token, or a
// GitHub App's ID, installation ID and private key.
func FromSecret(apiURL string, data map[string][]byte) (*Client, error) {
	base := strings.TrimSuffix(apiURL, "/") + "/"
	opts := []github.ClientOptionsFunc{
		github.WithURLs(&base, nil),
		github.WithTimeout(30 * time.Second),
	}
	c := &Client{}

	if token := string(data[KeyToken]); token != "" {
		opts = append(opts, github.WithAuthToken(token))
	} else {
		appID, installation, key := string(data[KeyAppID]), string(data[KeyAppInstallationID]), data[KeyAppPrivateKey]
		if appID == "" || installation == "" || len(key) == 0 {
			return nil, fmt.Errorf("the Secret holds neither %s nor all of %s, %s and %s",
				KeyToken, KeyAppID, KeyAppInstallationID, KeyAppPrivateKey)
		}
		id, err := strconv.ParseInt(appID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be the App's numeric ID: %w", KeyAppID, err)
		}
		inst, err := strconv.ParseInt(installation, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", KeyAppInstallationID, err)
		}
		tr, err := ghinstallation.New(http.DefaultTransport, id, inst, key)
		if err != nil {
			return nil, err
		}
		tr.BaseURL = strings.TrimSuffix(base, "/")
		opts = append(opts, github.WithTransport(tr))
		c.app = true
	}

	gh, err := github.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("GitHub client: %w", err)
	}
	c.gh = gh
	return c, nil
}

// Repositories lists every repository the credentials can see: those granted
// to the App installation, or those the token's owner owns.
func (c *Client) Repositories(ctx context.Context) ([]*github.Repository, error) {
	page := github.ListOptions{PerPage: 100}
	seq := c.gh.Repositories.ListByAuthenticatedUserIter(ctx,
		&github.RepositoryListByAuthenticatedUserOptions{Affiliation: "owner", ListOptions: page})
	if c.app {
		seq = c.gh.Apps.ListReposIter(ctx, &page)
	}
	all := make([]*github.Repository, 0, page.PerPage)
	for repo, err := range seq {
		if err != nil {
			return nil, err
		}
		all = append(all, repo)
	}
	return all, nil
}
