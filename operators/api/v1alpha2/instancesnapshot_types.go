/*


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

package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// InstanceSnapshotSpec defines the desired state of InstanceSnapshot
type InstanceSnapshotSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Instance is the reference to the persistent VM instance to be snapshotted
	// the instance should not be running, otherwise it won't be possible to
	// steal the volume and extract its content
	// +kubebuilder:validation:Required
	Instance GenericRef `json:"instance.crownlabs.polito.it/InstanceRef"`

	// A template contains a list of environments, this generalize the concept of template and allow to spawn
	// different vm or containers from the same templaate.
	// However, at the moment this functionality has not been implemented and for each template there is one single environment.
	// The EnvName field represent the name of the environment to be snapshotted, in order to make it compatible
	// with future upgrades. If not specified, the first available environment is taken.
	EnvName string `json:"environment-name, omitempty"`

	// ImageName is the name of the image to pushed in the docker registry
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength.=1
	ImageName string `json:"image-name"`
}

// InstanceSnapshotStatus defines the observed state of InstanceSnapshot
type InstanceSnapshotStatus struct {
	// Phase represent current state of the creation of the vm instance snapshot
	// it could be:
	// - pending: if the snapshot is waiting to be created
	// - processing: if the snapshot is under processing
	// - failed: is an error occurred and it was not possible to create the snapshot
	Phase string `json:"phase"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="isnap"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="ImageName",type=string,JSONPath=`.spec.image-name`

// InstanceSnapshot is the Schema for the instancesnapshots API
type InstanceSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstanceSnapshotSpec   `json:"spec,omitempty"`
	Status InstanceSnapshotStatus `json:"status,omitempty"`
}

func (i InstanceSnapshot) DeepCopyObject() runtime.Object {
	panic("implement me")
}

// +kubebuilder:object:root=true

// InstanceSnapshotList contains a list of InstanceSnapshot
type InstanceSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InstanceSnapshot `json:"items"`
}

func (i InstanceSnapshotList) DeepCopyObject() runtime.Object {
	panic("implement me")
}

func init() {
	SchemeBuilder.Register(&InstanceSnapshot{}, &InstanceSnapshotList{})
}
