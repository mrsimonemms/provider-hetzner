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
)

// VolumeParameters are the configurable fields of a Volume.
type VolumeParameters struct {
	Size int `json:"size"`

	// +kubebuilder:validation:Optional
	Automount bool `json:"automount"`

	// +kubebuilder:default:=ext4
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum:=xfs;ext4
	Format string `json:"format"`

	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	Location *string `json:"location,omitempty"`

	// +kubebuilder:validation:Optional
	ServerID *int64 `json:"serverID"`
}

// VolumeObservation are the observable fields of a Volume.
type VolumeObservation struct {
	// +kubebuilder:validation:Optional
	ID int64 `json:"id"`

	// +kubebuilder:validation:Optional
	*VolumeParameters `json:"params,omitempty"`
}

// A VolumeSpec defines the desired state of a Volume.
type VolumeSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       VolumeParameters `json:"forProvider"`
}

// A VolumeStatus represents the observed state of a Volume.
type VolumeStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          VolumeObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Volume is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hetzner}
type Volume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VolumeSpec   `json:"spec"`
	Status VolumeStatus `json:"status,omitempty"`
}

func (v *Volume) IsUpToDate() bool {
	target := v.Spec.ForProvider
	current := v.Status.AtProvider.VolumeParameters

	if current == nil {
		// No parameters set
		return false
	}
	if !reflect.DeepEqual(target.Labels, current.Labels) {
		return false
	}
	if current.ServerID != target.ServerID {
		// Attach/detach volume
		return false
	}
	if current.Size < target.Size {
		// Increase the volume size
		return false
	}

	return true
}

// +kubebuilder:object:root=true

// VolumeList contains a list of Volume
type VolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Volume `json:"items"`
}

// Volume type metadata.
var (
	VolumeKind             = reflect.TypeOf(Volume{}).Name()
	VolumeGroupKind        = schema.GroupKind{Group: Group, Kind: VolumeKind}.String()
	VolumeKindAPIVersion   = VolumeKind + "." + SchemeGroupVersion.String()
	VolumeGroupVersionKind = SchemeGroupVersion.WithKind(VolumeKind)
)

func init() {
	SchemeBuilder.Register(&Volume{}, &VolumeList{})
}
