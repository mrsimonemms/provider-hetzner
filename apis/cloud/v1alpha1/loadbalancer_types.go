/*
Copyright 2022 The Crossplane Authors.

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

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	hcloudsdk "github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/mrsimonemms/provider-hetzner/pkg/hcloud"
)

// LoadBalancerParameters are the configurable fields of a LoadBalancer.
type LoadBalancerParameters struct {
	Type string `json:"type"`

	// +kubebuilder:default:=round_robin
	// +kubebuilder:validation:Enum:=round_robin;least_connections
	// +kubebuilder:validation:Optional
	Algorithm hcloudsdk.LoadBalancerAlgorithmType `json:"algorithm"`

	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	Location *string `json:"location,omitempty"`

	// +kubebuilder:validation:Optional
	NetworkZone hcloudsdk.NetworkZone `json:"networkZone"`

	// +kubebuilder:validation:Optional
	NetworkID *int64 `json:"networkID,omitempty"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	PublicInterface bool `json:"publicInterface"`

	// +kubebuilder:validation:Optional
	Services []LoadBalancerService `json:"services"`

	// +kubebuilder:validation:Optional
	Targets []LoadBalancerTarget `json:"targets"`
}

type LoadBalancerService struct {
	DestinationPort int                                   `json:"destinationPort"`
	HealthCheck     LoadBalancerHealthCheck               `json:"healthCheck"`
	ListenPort      int                                   `json:"listenPort"`
	Protocol        hcloudsdk.LoadBalancerServiceProtocol `json:"protocol"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	ProxyProtocol bool `json:"proxyProtocol"`

	// +kubebuilder:validation:Optional
	HTTP *LoadBalancerHTTPConfig `json:"http,omitempty"`
}

type LoadBalancerHealthCheck struct {
	// +kubebuilder:default:=http
	Protocol hcloudsdk.LoadBalancerServiceProtocol `json:"protocol"`

	// +kubebuilder:default:=80
	Port *int `json:"port"`

	// +kubebuilder:default:="15s"
	// +kubebuilder:validation:Format:=duration
	Interval *hcloud.Duration `json:"interval"`

	// +kubebuilder:default:="10s"
	// +kubebuilder:validation:Format:=duration
	Timeout *hcloud.Duration `json:"timeout"`

	// +kubebuilder:default:=3
	Retries *int `json:"retries"`

	// +kubebuilder:validation:Optional
	HTTP *LoadBalancerHealthCheckHTTP `json:"http,omitempty"`
}

type LoadBalancerHealthCheckHTTP struct {
	Path *string `json:"path"`

	// +kubebuilder:validation:Optional
	Domain *string `json:"domain"`

	// +kubebuilder:validation:Optional
	Response *string `json:"response,omitempty"`

	// +kubebuilder:default:={"2??","3??"}
	// +kubebuilder:validation:Optional
	StatusCodes []string `json:"statusCodes"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	TLS *bool `json:"tls,omitempty"`
}

type LoadBalancerHTTPConfig struct {
	// +kubebuilder:validation:Optional
	CertificateIDs []int64 `json:"certificateIDs"`

	// +kubebuilder:validation:Optional
	CookieName *string `json:"cookieName,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="300s"
	// +kubebuilder:validation:Format:=duration
	CookieLifetime *hcloud.Duration `json:"cookieLifetime"`

	// +kubebuilder:validation:Optional
	RedirectHTTP *bool `json:"redirectHTTP,omitempty"`

	// +kubebuilder:validation:Optional
	StickySessions *bool `json:"stickySessions,omitempty"`
}

type LoadBalancerTarget struct {
	Type hcloudsdk.LoadBalancerTargetType `json:"type"`

	// +kubebuilder:validation:Optional
	Labels *map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	ServerID *int64 `json:"serverID,omitempty"`

	// +kubebuilder:validation:Optional
	IP *string `json:"ip,omitempty"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	UsePrivateIP bool `json:"usePrivateIP"`
}

// LoadBalancerObservation are the observable fields of a LoadBalancer.
type LoadBalancerObservation struct {
	// +kubebuilder:validation:Optional
	ID int64 `json:"id"`

	// +kubebuilder:validation:Optional
	*LoadBalancerParameters `json:"params,omitempty"`
}

// A LoadBalancerSpec defines the desired state of a LoadBalancer.
type LoadBalancerSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       LoadBalancerParameters `json:"forProvider"`
}

// A LoadBalancerStatus represents the observed state of a LoadBalancer.
type LoadBalancerStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          LoadBalancerObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A LoadBalancer is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hetzner}
type LoadBalancer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LoadBalancerSpec   `json:"spec"`
	Status LoadBalancerStatus `json:"status,omitempty"`
}

func (l *LoadBalancer) IsUpToDate() bool {
	target := l.Spec.ForProvider
	current := l.Status.AtProvider.LoadBalancerParameters

	if current == nil {
		// No parameters set
		return false
	}
	if !reflect.DeepEqual(target.Labels, current.Labels) {
		return false
	}
	if target.Type != current.Type {
		return false
	}
	if target.PublicInterface != current.PublicInterface {
		return false
	}
	if target.Algorithm != current.Algorithm {
		return false
	}
	if target.NetworkID != current.NetworkID {
		return false
	}
	if !reflect.DeepEqual(target.Services, current.Services) {
		return false
	}
	if !reflect.DeepEqual(target.Targets, current.Targets) {
		return false
	}

	return true
}

// +kubebuilder:object:root=true

// LoadBalancerList contains a list of LoadBalancer
type LoadBalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoadBalancer `json:"items"`
}

// LoadBalancer type metadata.
var (
	LoadBalancerKind             = reflect.TypeOf(LoadBalancer{}).Name()
	LoadBalancerGroupKind        = schema.GroupKind{Group: Group, Kind: LoadBalancerKind}.String()
	LoadBalancerKindAPIVersion   = LoadBalancerKind + "." + SchemeGroupVersion.String()
	LoadBalancerGroupVersionKind = SchemeGroupVersion.WithKind(LoadBalancerKind)
)

func init() {
	SchemeBuilder.Register(&LoadBalancer{}, &LoadBalancerList{})
}
