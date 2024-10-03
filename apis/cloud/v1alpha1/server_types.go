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
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// ServerParameters are the configurable fields of a Server.
type ServerParameters struct {
	Image      string `json:"image"`
	ServerType string `json:"serverType"`

	// One of datacenter or location is required

	// +kubebuilder:validation:Optional
	Datacenter *string `json:"datacenter,omitempty"`

	// +kubebuilder:validation:Optional
	Location *string `json:"location,omitempty"`

	// +kubebuilder:default:=x86
	// +kubebuilder:validation:Optional
	Architecture hcloud.Architecture `json:"architecture"`

	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	AutoMount bool `json:"autoMount"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	EnableIPv4 bool `json:"enableIPv4"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	EnableIPv6 bool `json:"enableIPv6"`

	// +kubebuilder:validation:Optional
	FirewallIDs []int64 `json:"firewallIDs"`

	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	NetworkIDs []int64 `json:"networkIDs"`

	// +kubebuilder:validation:Optional
	PlacementGroupID *int64 `json:"placementGroupID,omitempty"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	PowerOn bool `json:"powerOn"` // This is designed to control power state via update

	// +kubebuilder:validation:Optional
	SSHKeys []string `json:"sshKeys"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	StartAfterCreate bool `json:"startAfterCreate"`

	// +kubebuilder:validation:Optional
	UserData string `json:"userData"`

	// +kubebuilder:validation:Optional
	VolumeIDs []int64 `json:"volumeIDs"`
}

// ServerObservation are the observable fields of a Server.
type ServerObservation struct {
	// +kubebuilder:validation:Optional
	ID int64 `json:"id"`

	// +kubebuilder:validation:Optional
	*ServerParameters `json:"param,omitempty"`
}

// A ServerSpec defines the desired state of a Server.
type ServerSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       ServerParameters `json:"forProvider"`
}

// A ServerStatus represents the observed state of a Server.
type ServerStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          ServerObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Server is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hetzner}
type Server struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServerSpec   `json:"spec"`
	Status ServerStatus `json:"status,omitempty"`
}

func (s *Server) IsUpToDate() bool {
	target := s.Spec.ForProvider
	current := s.Status.AtProvider.ServerParameters

	if current == nil {
		// No parameters set
		return false
	}
	if !reflect.DeepEqual(target.Labels, current.Labels) {
		return false
	}
	if target.PowerOn != current.PowerOn {
		return false
	}

	return true
}

// +kubebuilder:object:root=true

// ServerList contains a list of Server
type ServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Server `json:"items"`
}

// Server type metadata.
var (
	ServerKind             = reflect.TypeOf(Server{}).Name()
	ServerGroupKind        = schema.GroupKind{Group: Group, Kind: ServerKind}.String()
	ServerKindAPIVersion   = ServerKind + "." + SchemeGroupVersion.String()
	ServerGroupVersionKind = SchemeGroupVersion.WithKind(ServerKind)
)

func init() {
	SchemeBuilder.Register(&Server{}, &ServerList{})
}
