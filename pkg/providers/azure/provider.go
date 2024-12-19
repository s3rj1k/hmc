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

package azure

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	capz "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Mirantis/hmc/pkg/credspropagation"
)

func PropagateSecrets(ctx context.Context, cfg *credspropagation.PropagationCfg) error {
	azureCluster := &capz.AzureCluster{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      cfg.ManagedCluster.Name,
		Namespace: cfg.ManagedCluster.Namespace,
	}, azureCluster); err != nil {
		return fmt.Errorf("failed to get AzureCluster %s: %w", cfg.ManagedCluster.Name, err)
	}

	azureClIdty := &capz.AzureClusterIdentity{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      azureCluster.Spec.IdentityRef.Name,
		Namespace: azureCluster.Spec.IdentityRef.Namespace,
	}, azureClIdty); err != nil {
		return fmt.Errorf("failed to get AzureClusterIdentity %s: %w", azureCluster.Spec.IdentityRef.Name, err)
	}

	azureSecret := &corev1.Secret{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      azureClIdty.Spec.ClientSecret.Name,
		Namespace: azureClIdty.Spec.ClientSecret.Namespace,
	}, azureSecret); err != nil {
		return fmt.Errorf("failed to get Azure Secret %s: %w", azureClIdty.Spec.ClientSecret.Name, err)
	}

	azureJSON, err := json.Marshal(
		map[string]any{
			"cloud":                        azureCluster.Spec.AzureEnvironment,
			"tenantId":                     azureClIdty.Spec.TenantID,
			"subscriptionId":               azureCluster.Spec.SubscriptionID,
			"aadClientId":                  azureClIdty.Spec.ClientID,
			"aadClientSecret":              string(azureSecret.Data["clientSecret"]),
			"resourceGroup":                azureCluster.Spec.ResourceGroup,
			"securityGroupName":            azureCluster.Spec.NetworkSpec.Subnets[0].SecurityGroup.Name,
			"securityGroupResourceGroup":   azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup,
			"location":                     azureCluster.Spec.Location,
			"vmType":                       "vmss",
			"vnetName":                     azureCluster.Spec.NetworkSpec.Vnet.Name,
			"vnetResourceGroup":            azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup,
			"subnetName":                   azureCluster.Spec.NetworkSpec.Subnets[0].Name,
			"loadBalancerSku":              "Standard",
			"loadBalancerName":             "",
			"maximumLoadBalancerRuleCount": 250,
			"useManagedIdentityExtension":  false,
			"useInstanceMetadata":          true,
		},
	)
	if err != nil {
		return fmt.Errorf("error marshalling azure.json: %w", err)
	}

	ccmSecret := credspropagation.MakeSecret("azure-cloud-provider",
		metav1.NamespaceSystem,
		map[string][]byte{
			"cloud-config": azureJSON,
		},
	)

	if err := credspropagation.ApplyCCMConfigs(ctx, cfg.KubeconfSecret, ccmSecret); err != nil {
		return fmt.Errorf("failed to apply Azure CCM secret: %w", err)
	}

	return nil
}

type Provider struct{}

func NewProvider() any {
	return &Provider{}
}

// func init() {
// 	providers.Register(&Provider{})
// }

func (*Provider) GetName() string {
	return "azure"
}

func (*Provider) GetTitleName() string {
	return "Azure"
}

func (*Provider) GetClusterGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "AzureCluster",
	}
}

func (*Provider) GetClusterIdentityKinds() []string {
	return []string{"AzureClusterIdentity"}
}

func (p *Provider) CredentialPropagationFunc() func(
	ctx context.Context,
	propnCfg *credspropagation.PropagationCfg,
	l logr.Logger,
) (enabled bool, err error) {
	return func(
		ctx context.Context,
		propnCfg *credspropagation.PropagationCfg,
		l logr.Logger,
	) (enabled bool, err error) {
		l.Info(p.GetTitleName() + " creds propagation start")
		enabled, err = true, PropagateSecrets(ctx, propnCfg)
		return enabled, err
	}
}
