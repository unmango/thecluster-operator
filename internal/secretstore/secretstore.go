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

// Package secretstore writes the secrets the operator produces into the
// store programs read them from.
package secretstore

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// Writer sets and deletes named secrets.
type Writer interface {
	Set(ctx context.Context, name, value string) error
	// Delete removes a secret; one already gone is not an error.
	Delete(ctx context.Context, name string) error
}

// KeyVault writes to one Azure Key Vault.
type KeyVault struct {
	client *azsecrets.Client
}

// NewKeyVault returns a Writer for the vault at url, authenticated by
// azidentity's default chain, which reads AZURE_CLIENT_ID, AZURE_TENANT_ID
// and AZURE_CLIENT_SECRET or a workload identity token from the environment.
func NewKeyVault(url string) (*KeyVault, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	client, err := azsecrets.NewClient(url, cred, nil)
	if err != nil {
		return nil, err
	}
	return &KeyVault{client: client}, nil
}

// Set writes the secret. A name held by a soft-deleted secret is refused
// until that secret is recovered or purged, so Set recovers it and still
// fails, leaving the retry to overwrite it once recovery finishes.
func (k *KeyVault) Set(ctx context.Context, name, value string) error {
	_, err := k.client.SetSecret(ctx, name, azsecrets.SetSecretParameters{Value: &value}, nil)
	var resp *azcore.ResponseError
	if errors.As(err, &resp) && resp.StatusCode == http.StatusConflict {
		_, rerr := k.client.RecoverDeletedSecret(ctx, name, nil)
		return errors.Join(err, rerr)
	}
	return err
}

// Delete soft-deletes the secret, so the vault keeps it for its retention
// period.
func (k *KeyVault) Delete(ctx context.Context, name string) error {
	_, err := k.client.DeleteSecret(ctx, name, nil)
	var resp *azcore.ResponseError
	if errors.As(err, &resp) && resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

// Memory is a Writer that keeps secrets in a map, for tests. Fail, when
// set, is returned by every call instead.
type Memory struct {
	Mu      sync.Mutex
	Secrets map[string]string
	Fail    error
}

func NewMemory() *Memory {
	return &Memory{Secrets: map[string]string{}}
}

func (m *Memory) Set(_ context.Context, name, value string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.Secrets[name] = value
	return nil
}

func (m *Memory) Delete(_ context.Context, name string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.Secrets, name)
	return nil
}

// Get returns the secret called name and whether it exists.
func (m *Memory) Get(name string) (string, bool) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	v, ok := m.Secrets[name]
	return v, ok
}
