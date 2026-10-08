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

package registry

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	registryv1alpha1 "github.com/unmango/thecluster-operator/api/registry/v1alpha1"
	"github.com/unmango/thecluster-operator/internal/harbor"
)

const (
	// TypeReady is the condition every registry resource reports.
	TypeReady = "Ready"

	// resync is how often a Ready resource is checked against Harbor again,
	// which is what puts back a setting changed in Harbor's UI.
	resync = 10 * time.Minute
	// retry is the delay after a failure caused by something outside the
	// cluster, such as Harbor being unreachable, which no watch reports.
	retry = time.Minute
)

// harborFor returns a client for reg's Harbor backend.
func harborFor(ctx context.Context, c client.Client, reg *registryv1alpha1.Registry) (*harbor.Client, error) {
	h := reg.Spec.Harbor
	if h == nil {
		return nil, fmt.Errorf("registry %s has no backend", reg.Name)
	}
	password, err := secretValue(ctx, c, reg.Namespace, h.PasswordSecretRef.Name, h.PasswordSecretRef.Key)
	if err != nil {
		return nil, err
	}
	username := h.Username
	if username == "" {
		username = "admin"
	}
	return harbor.New(h.URL, username, password), nil
}

func secretValue(ctx context.Context, c client.Client, namespace, name, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return "", fmt.Errorf("reading secret %s: %w", name, err)
	}
	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s has no key %q", name, key)
	}
	return string(value), nil
}

func setReady(conditions *[]metav1.Condition, generation int64, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
	})
}
