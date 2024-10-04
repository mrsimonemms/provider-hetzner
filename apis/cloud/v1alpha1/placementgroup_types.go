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

// PlacementGroupParameters are the configurable fields of a PlacementGroup.
type PlacementGroupParameters struct {
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// +kubebuilder:default:=spread
	// +kubebuilder:validation:Enum:=spread
	// +kubebuilder:validation:Optional
	Type hcloud.PlacementGroupType `json:"type"`
}

// PlacementGroupObservation are the observable fields of a PlacementGroup.
type PlacementGroupObservation struct {
	// +kubebuilder:validation:Optional
	ID int64 `json:"id"`

	// +kubebuilder:validation:Optional
	*PlacementGroupParameters `json:"params,omitempty"`
}

// A PlacementGroupSpec defines the desired state of a PlacementGroup.
type PlacementGroupSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       PlacementGroupParameters `json:"forProvider"`
}

// A PlacementGroupStatus represents the observed state of a PlacementGroup.
type PlacementGroupStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          PlacementGroupObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A PlacementGroup is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hetzner}
type PlacementGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlacementGroupSpec   `json:"spec"`
	Status PlacementGroupStatus `json:"status,omitempty"`
}

func (p *PlacementGroup) IsUpToDate() bool {
	target := p.Spec.ForProvider
	current := p.Status.AtProvider.PlacementGroupParameters

	if current == nil {
		// No parameters set
		return false
	}
	if !reflect.DeepEqual(target.Labels, current.Labels) {
		return false
	}

	return true
}

// +kubebuilder:object:root=true

// PlacementGroupList contains a list of PlacementGroup
type PlacementGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlacementGroup `json:"items"`
}

// PlacementGroup type metadata.
var (
	PlacementGroupKind             = reflect.TypeOf(PlacementGroup{}).Name()
	PlacementGroupGroupKind        = schema.GroupKind{Group: Group, Kind: PlacementGroupKind}.String()
	PlacementGroupKindAPIVersion   = PlacementGroupKind + "." + SchemeGroupVersion.String()
	PlacementGroupGroupVersionKind = SchemeGroupVersion.WithKind(PlacementGroupKind)
)

func init() {
	SchemeBuilder.Register(&PlacementGroup{}, &PlacementGroupList{})
}
