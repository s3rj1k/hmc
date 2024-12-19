// Copyright 2024
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"k8s.io/apimachinery/pkg/runtime/schema"

	hmc "github.com/Mirantis/hmc/api/v1alpha1"
	"github.com/Mirantis/hmc/pkg/credspropagation"
	"github.com/Mirantis/hmc/pkg/providers/aws"
)

const (
	// InfraPrefix is the prefix used for infrastructure provider names
	InfraPrefix = "infrastructure-"
	// ProviderPrefix is the prefix used for cluster API provider names
	ProviderPrefix = "cluster-api-provider-"
)

var (
	mu sync.RWMutex

	providers = []hmc.Provider{
		{
			Name: hmc.ProviderK0smotronName,
		},
		{
			Name: hmc.ProviderSveltosName,
		},
	}

	registry    map[string]ProviderModule
	interpreter *interp.Interpreter
	pluginsPath string
)

type ProviderModule interface {
	// GetName returns the short name of the provider
	GetName() string
	// GetTitleName returns the display title of the provider
	GetTitleName() string
	// GetClusterGVK returns the GroupVersionKind for the provider's cluster resource
	GetClusterGVK() schema.GroupVersionKind
	// GetClusterIdentityKinds returns a list of supported cluster identity kinds
	GetClusterIdentityKinds() []string
	// CredentialPropagationFunc returns a function to handle credential propagation
	CredentialPropagationFunc() func(
		ctx context.Context,
		propnCfg *credspropagation.PropagationCfg,
		l logr.Logger,
	) (enabled bool, err error)
}

// InitializePluginSystem sets up the Yaegi interpreter and loads plugins from the directory
func InitializePluginSystem(pluginDir string) error {
	mu.Lock()
	defer mu.Unlock()

	pluginsPath = pluginDir

	if registry == nil {
		registry = make(map[string]ProviderModule)
	}

	interpreter = interp.New(interp.Options{
		GoPath:       filepath.Join(pluginDir, "vendor"),
		Unrestricted: true,
	})

	if err := interpreter.Use(stdlib.Symbols); err != nil {
		return fmt.Errorf("failed to load stdlib: %w", err)
	}

	return loadPluginsFromDirectory()
}

func loadPluginsFromDirectory() error {
	entries, err := os.ReadDir(pluginsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Directory doesn't exist, silently return
			return nil
		}
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			if err := loadSinglePlugin(entry.Name()); err != nil {
				return fmt.Errorf("failed to load plugin %q: %w", entry.Name(), err)
			}
		}
	}

	return nil
}

func loadSinglePlugin(filename string) error {
	fullPath := filepath.Join(pluginsPath, filename)

	_, err := interpreter.EvalPath(fullPath)
	if err != nil {
		return fmt.Errorf("failed to evaluate plugin: %w", err)
	}

	v, err := interpreter.Eval("NewProvider()")
	if err != nil {
		return fmt.Errorf("failed to instantiate plugin: %w", err)
	}

	provider, ok := v.Interface().(ProviderModule)
	if !ok {
		return errors.New("does not implement ProviderModule interface")
	}

	return registerProvider(provider)
}

func registerProvider(p ProviderModule) error {
	shortName := p.GetName()

	if _, exists := registry[shortName]; exists {
		return fmt.Errorf("provider %q already registered", shortName)
	}

	providers = append(providers,
		hmc.Provider{
			Name: ProviderPrefix + shortName,
		},
	)

	registry[shortName] = p

	return nil
}

// Register adds a new provider module to the registry (static registration)
func Register(p ProviderModule) {
	mu.Lock()
	defer mu.Unlock()

	if registry == nil {
		registry = make(map[string]ProviderModule)
	}

	if err := registerProvider(p); err != nil {
		panic(err)
	}
}

// List returns a copy of all registered providers
func List() []hmc.Provider {
	return slices.Clone(providers)
}

// CredentialPropagationFunc returns the credential propagation function for a given provider
func CredentialPropagationFunc(fullName string) (
	func(ctx context.Context, propnCfg *credspropagation.PropagationCfg, l logr.Logger) (enabled bool, err error), bool,
) {
	mu.RLock()
	defer mu.RUnlock()

	shortName := strings.TrimPrefix(fullName, ProviderPrefix)

	module, ok := registry[shortName]
	if !ok {
		return nil, false
	}

	f := module.CredentialPropagationFunc()

	return f, f != nil
}

// GetClusterGVK returns the GroupVersionKind for a provider's cluster resource
func GetClusterGVK(shortName string) schema.GroupVersionKind {
	mu.RLock()
	defer mu.RUnlock()

	module, ok := registry[shortName]
	if !ok {
		return schema.GroupVersionKind{}
	}

	return module.GetClusterGVK()
}

// GetClusterIdentityKind returns the supported identity kinds for a given infrastructure provider
func GetClusterIdentityKind(infraName string) ([]string, bool) {
	mu.RLock()
	defer mu.RUnlock()

	shortName := strings.TrimPrefix(infraName, InfraPrefix)

	module, ok := registry[shortName]
	if !ok {
		return nil, false
	}

	list := slices.Clone(module.GetClusterIdentityKinds())

	return list, list != nil
}

// GetProviderTitleName returns the display title for a given provider
func GetProviderTitleName(shortName string) string {
	mu.RLock()
	defer mu.RUnlock()

	module, ok := registry[shortName]
	if !ok {
		return ""
	}

	return module.GetTitleName()
}

func init() {
	Register(&aws.Provider{})
}
