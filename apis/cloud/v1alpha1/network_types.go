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

// NetworkParameters are the configurable fields of a Network.
type NetworkParameters struct {
	ConfigurableField string `json:"configurableField"`
}

// NetworkObservation are the observable fields of a Network.
type NetworkObservation struct {
	ObservableField string `json:"observableField,omitempty"`
}

// A NetworkSpec defines the desired state of a Network.
type NetworkSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       NetworkParameters `json:"forProvider"`
}

// A NetworkStatus represents the observed state of a Network.
type NetworkStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          NetworkObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Network is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hetzner}
type Network struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkSpec   `json:"spec"`
	Status NetworkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkList contains a list of Network
type NetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Network `json:"items"`
}

// Network type metadata.
var (
	NetworkKind             = reflect.TypeOf(Network{}).Name()
	NetworkGroupKind        = schema.GroupKind{Group: Group, Kind: NetworkKind}.String()
	NetworkKindAPIVersion   = NetworkKind + "." + SchemeGroupVersion.String()
	NetworkGroupVersionKind = SchemeGroupVersion.WithKind(NetworkKind)
)

func init() {
	SchemeBuilder.Register(&Network{}, &NetworkList{})
}
