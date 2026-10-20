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

// Package github is a minimal client for the part of GitHub's REST API that
// User discovery calls: listing the repositories a set of credentials can see.
// It reads credentials in the actions-runner-controller's githubConfigSecret
// format, so discovery and the scale sets share one Secret.
package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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

// pageSize is the most GitHub returns in one page.
const pageSize = 100

// Client lists repositories as one GitHub App installation or token.
type Client struct {
	base string
	http *http.Client
	auth func(context.Context) (string, error)
	app  bool
}

// Repository is the part of GitHub's repository object discovery reads.
type Repository struct {
	Name     string `json:"name"`
	HTMLURL  string `json:"html_url"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Error is a non-2xx response from GitHub.
type Error struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("github: %s %s: %d %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// FromSecret returns a Client for the credentials in data, the contents of a
// githubConfigSecret: a token, or a GitHub App's ID, installation ID and
// private key.
func FromSecret(apiURL string, data map[string][]byte) (*Client, error) {
	c := &Client{
		base: strings.TrimSuffix(apiURL, "/"),
		http: &http.Client{Timeout: 30 * time.Second},
	}
	if token := string(data[KeyToken]); token != "" {
		c.auth = func(context.Context) (string, error) { return "token " + token, nil }
		return c, nil
	}

	appID := string(data[KeyAppID])
	installation := string(data[KeyAppInstallationID])
	if appID == "" || installation == "" || len(data[KeyAppPrivateKey]) == 0 {
		return nil, fmt.Errorf("the Secret holds neither %s nor all of %s, %s and %s",
			KeyToken, KeyAppID, KeyAppInstallationID, KeyAppPrivateKey)
	}
	key, err := parseKey(data[KeyAppPrivateKey])
	if err != nil {
		return nil, err
	}
	c.app = true
	c.auth = func(ctx context.Context) (string, error) {
		token, err := c.installationToken(ctx, appID, installation, key)
		return "token " + token, err
	}
	return c, nil
}

func parseKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("the GitHub App private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing the GitHub App private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App private key is not an RSA key")
	}
	return key, nil
}

// installationToken trades a JWT signed with the App's key for a token that
// acts as the installation.
func (c *Client) installationToken(ctx context.Context, appID, installation string, key *rsa.PrivateKey) (string, error) {
	jwt, err := signJWT(appID, key, time.Now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	path := "/app/installations/" + installation + "/access_tokens"
	if err := c.do(ctx, http.MethodPost, path, "Bearer "+jwt, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// signJWT makes the short-lived RS256 JWT a GitHub App authenticates as. It
// is backdated a minute to allow for clock drift, as GitHub recommends.
func signJWT(issuer string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": issuer,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

// Repositories lists every repository the credentials can see: those granted
// to the App installation, or those the token's owner owns.
func (c *Client) Repositories(ctx context.Context) ([]Repository, error) {
	auth, err := c.auth(ctx)
	if err != nil {
		return nil, err
	}
	var all []Repository
	for page := 1; ; page++ {
		var repos []Repository
		if c.app {
			var out struct {
				Repositories []Repository `json:"repositories"`
			}
			path := fmt.Sprintf("/installation/repositories?per_page=%d&page=%d", pageSize, page)
			if err := c.do(ctx, http.MethodGet, path, auth, &out); err != nil {
				return nil, err
			}
			repos = out.Repositories
		} else {
			path := fmt.Sprintf("/user/repos?affiliation=owner&per_page=%d&page=%d", pageSize, page)
			if err := c.do(ctx, http.MethodGet, path, auth, &repos); err != nil {
				return nil, err
			}
		}
		all = append(all, repos...)
		if len(repos) < pageSize {
			return all, nil
		}
	}
}

func (c *Client) do(ctx context.Context, method, path, auth string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", auth)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Method: method, Path: path, Status: resp.StatusCode, Body: string(body)}
	}
	return json.Unmarshal(body, out)
}
