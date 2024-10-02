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
	"net"
	"reflect"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	hcloudsdk "github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pkg/errors"

	"github.com/mrsimonemms/provider-hetzner/pkg/hcloud"
)

// FirewallParameters are the configurable fields of a Firewall.
type FirewallParameters struct {
	// +kubebuilder:validation:Optional
	ApplyTo []FirewallApplyTo `json:"applyTo"`

	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	Rules []FirewallRules `json:"rules"`
}

type FirewallApplyTo struct {
	Type hcloudsdk.FirewallResourceType `json:"type"`

	// +kubebuilder:validation:Optional
	ServerID *int64 `json:"serverID,omitempty"`

	// +kubebuilder:validation:Optional
	Labels *map[string]string `json:"labels,omitempty"`
}

func (f *FirewallApplyTo) ToFirewallResource() hcloudsdk.FirewallResource {
	var server *hcloudsdk.FirewallResourceServer
	var labels *hcloudsdk.FirewallResourceLabelSelector

	if f.ServerID != nil {
		server = &hcloudsdk.FirewallResourceServer{
			ID: *f.ServerID,
		}
	}
	if f.Labels != nil {
		labels = &hcloudsdk.FirewallResourceLabelSelector{
			Selector: hcloud.ToSelector(*f.Labels),
		}
	}

	return hcloudsdk.FirewallResource{
		Type:          f.Type,
		Server:        server,
		LabelSelector: labels,
	}
}

type FirewallRules struct {
	Direction hcloudsdk.FirewallRuleDirection `json:"direction"`
	Protocol  hcloudsdk.FirewallRuleProtocol  `json:"protocol"`

	// +kubebuilder:validation:MinItems:=1
	TargetIPs []string `json:"targetIPs"`

	// +kubebuilder:validation:Optional
	Description *string `json:"description,omitempty"`

	// +kubebuilder:validation:Optional
	Port *FirewallPort `json:"port,omitempty"`
}

func (f *FirewallRules) ToFirewallRule() (*hcloudsdk.FirewallRule, error) {
	opts := hcloudsdk.FirewallRule{
		Description: f.Description,
		Direction:   f.Direction,
		Protocol:    f.Protocol,
	}

	if f.Protocol == hcloudsdk.FirewallRuleProtocolTCP || f.Protocol == hcloudsdk.FirewallRuleProtocolUDP {
		opts.Port = hcloudsdk.Ptr(f.Port.String())
	}

	targetIPs := []net.IPNet{}
	for _, ip := range f.TargetIPs {
		_, netip, err := net.ParseCIDR(ip)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing firewall cidr")
		}
		targetIPs = append(targetIPs, *netip)
	}

	switch f.Direction {
	case hcloudsdk.FirewallRuleDirectionIn:
		opts.SourceIPs = targetIPs
	case hcloudsdk.FirewallRuleDirectionOut:
		opts.DestinationIPs = targetIPs
	}

	return &opts, nil
}

// Allow more explicit control of the port
type FirewallPort struct {
	// +kubebuilder:validation:Optional
	All bool `json:"all"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum:=1
	Start *int `json:"start,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum:=1
	End *int `json:"end,omitempty"`
}

func (f *FirewallPort) String() (s string) {
	if f == nil || f.All {
		s = "any"
		return
	}

	if f.Start != nil {
		start := *f.Start

		s = strconv.Itoa(start)

		if f.End != nil {
			end := *f.End

			if start != end {
				s += "-" + strconv.Itoa(end)
			}
		}
	}

	return
}

// FirewallObservation are the observable fields of a Firewall.
type FirewallObservation struct {
	// +kubebuilder:validation:Optional
	ID int64 `json:"id"`

	// +kubebuilder:validation:Optional
	*FirewallParameters `json:"params,omitempty"`
}

// A FirewallSpec defines the desired state of a Firewall.
type FirewallSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       FirewallParameters `json:"forProvider"`
}

// A FirewallStatus represents the observed state of a Firewall.
type FirewallStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          FirewallObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Firewall is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hetzner}
type Firewall struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FirewallSpec   `json:"spec"`
	Status FirewallStatus `json:"status,omitempty"`
}

func (f *Firewall) IsUpToDate() bool {
	target := f.Spec.ForProvider
	current := f.Status.AtProvider.FirewallParameters

	if current == nil {
		// No parameters set
		return false
	}
	if !reflect.DeepEqual(target.ApplyTo, current.ApplyTo) {
		return false
	}
	if !reflect.DeepEqual(target.Labels, current.Labels) {
		return false
	}
	if !reflect.DeepEqual(target.Rules, current.Rules) {
		return false
	}

	return true
}

// +kubebuilder:object:root=true

// FirewallList contains a list of Firewall
type FirewallList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Firewall `json:"items"`
}

// Firewall type metadata.
var (
	FirewallKind             = reflect.TypeOf(Firewall{}).Name()
	FirewallGroupKind        = schema.GroupKind{Group: Group, Kind: FirewallKind}.String()
	FirewallKindAPIVersion   = FirewallKind + "." + SchemeGroupVersion.String()
	FirewallGroupVersionKind = SchemeGroupVersion.WithKind(FirewallKind)
)

func init() {
	SchemeBuilder.Register(&Firewall{}, &FirewallList{})
}
