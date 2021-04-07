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

package instancesnapshot_controller

import (
	"context"
	"fmt"
	"time"

	batch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	crownlabsv1alpha2 "github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
	instancecreation "github.com/netgroup-polito/CrownLabs/operators/pkg/instance-creation"
)

// CheckSelectorLabel checks if the InstanceSnapshot belongs to the whitelisted namespaces where to perform reconciliation.
func (r *InstanceSnapshotReconciler) CheckSelectorLabel(ctx context.Context, isnap *crownlabsv1alpha2.InstanceSnapshot, req ctrl.Request) (bool, error) {
	ns := corev1.Namespace{}
	namespaceName := types.NamespacedName{
		Name:      isnap.Namespace,
		Namespace: "",
	}

	// It performs reconciliation only if the InstanceSnapshot belongs to whitelisted namespaces
	// by checking the existence of keys in the namespace of the InstanceSnapshot.
	if err := r.Get(ctx, namespaceName, &ns); err == nil {
		if !instancecreation.CheckLabels(&ns, r.NamespaceWhitelist.MatchLabels) {
			klog.Infof("Namespace %s does not meet the selector labels", req.Namespace)
			return false, nil
		}
	} else {
		return false, fmt.Errorf("error when retrieving the InstanceSnapshot namespace -> %w", err)
	}

	klog.Info("Namespace " + req.Namespace + " met the selector labels")
	return true, nil
}

// ValidateRequest validates the InstanceSnapshot request, returns an error and if there's the need to try again.
func (r *InstanceSnapshotReconciler) ValidateRequest(ctx context.Context, isnap *crownlabsv1alpha2.InstanceSnapshot) (bool, error) {
	// First it is needed to check if the instance actually exists.
	instanceName := types.NamespacedName{
		Namespace: isnap.Spec.Instance.Namespace,
		Name:      isnap.Spec.Instance.Name,
	}
	instance := &crownlabsv1alpha2.Instance{}

	if err := r.Get(ctx, instanceName, instance); err != nil && errors.IsNotFound(err) {
		// The declared instance does not exist so don't try again.
		return false, fmt.Errorf("instance %s not found in namespace %s. It is not possible to complete the InstanceSnapshot %s",
			instanceName.Name, instanceName.Namespace, isnap.Name)
	} else if err != nil {
		return true, fmt.Errorf("error in retrieving the instance for InstanceSnapshot %s -> %w", isnap.Name, err)
	}

	// Get the template of the instance in order to check if it has the requirements to be snapshotted.
	// In order to create a snapshot of the vm, we need first to check that:
	// - the vm is powered off, since it is not possible to steal the DataVolume if it is still running;
	// - the environment is a persistent vm and not a container.

	templateName := types.NamespacedName{
		Namespace: instance.Spec.Template.Namespace,
		Name:      instance.Spec.Template.Name,
	}
	template := &crownlabsv1alpha2.Template{}

	if err := r.Get(ctx, templateName, template); err != nil && errors.IsNotFound(err) {
		// The declared template does not exist set the phase as failed and don't try again.
		return false, fmt.Errorf("instance %s not found in namespace %s. It is not possible to complete the InstanceSnapshot %s",
			instanceName.Name, instanceName.Namespace, isnap.Name)
	} else if err != nil {
		return true, fmt.Errorf("error in retrieving the template for InstanceSnapshot %s -> %w", isnap.Name, err)
	}

	// Retrieve the environment from the template.
	var env *crownlabsv1alpha2.Environment = nil

	if isnap.Spec.Environment.Name != "" {
		for i := range template.Spec.EnvironmentList {
			if template.Spec.EnvironmentList[i].Name == isnap.Spec.Environment.Name {
				env = &template.Spec.EnvironmentList[i]
				break
			}
		}

		// Check if the specified environment was found.
		if env == nil {
			return false, fmt.Errorf("environment %s not found in template %s. It is not possible to complete the InstanceSnapshot %s",
				isnap.Spec.Environment.Name, template.Name, isnap.Name)
		}
	} else {
		// If the environment is not explicitly declared, take the first one.
		env = &template.Spec.EnvironmentList[0]
	}

	// Check if the environment is a persistent VM.
	if env.EnvironmentType != crownlabsv1alpha2.ClassVM || !env.Persistent {
		return false, fmt.Errorf("environment %s is not a persistent VM. It is not possible to complete the InstanceSnapshot %s",
			env.Name, isnap.Name)
	}

	// Check if the VM is running.
	if instance.Spec.Running || instance.Status.Phase != "VmiOff" {
		return false, fmt.Errorf("the vm is running. It is not possible to complete the InstanceSnapshot %s", isnap.Name)
	}

	return false, nil
}

// GetJobStatus sets a Job and returns its status.
func (r *InstanceSnapshotReconciler) GetJobStatus(job *batch.Job) (bool, batch.JobConditionType) {
	for _, c := range job.Status.Conditions {
		// If the status corresponding to Success or failed is true, it means that the job completed.
		if c.Status == corev1.ConditionTrue && (c.Type == batch.JobFailed || c.Type == batch.JobComplete) {
			return true, c.Type
		}
	}

	// Job did not complete.
	return false, ""
}

// GetSnapshottingJob generates the job to be created.
func (r *InstanceSnapshotReconciler) GetSnapshottingJob(isnap *crownlabsv1alpha2.InstanceSnapshot, job *batch.Job) {
	var backoff int32 = 2
	imagedir := isnap.Spec.Instance.Namespace
	imagetag := fmt.Sprint(time.Now().Format("02012006t150405"))
	volumename := isnap.Spec.Instance.Name

	// Define volumes.

	// Define VM VolumeSource.
	persistentvolsrc := corev1.PersistentVolumeClaimVolumeSource{
		ClaimName: volumename,
	}
	vmvolume := corev1.VolumeSource{
		PersistentVolumeClaim: &persistentvolsrc,
	}

	// Define temp VolumeSource.
	emptyvolsrc := corev1.EmptyDirVolumeSource{}
	tmpvol := corev1.VolumeSource{
		EmptyDir: &emptyvolsrc,
	}

	// Define secret VolumeSource.
	secretvolsrc := corev1.SecretVolumeSource{
		SecretName: "kaniko-secret",
		Items: []corev1.KeyToPath{
			{
				Key:  ".dockerconfigjson",
				Path: "config.json",
			},
		},
	}
	secretvol := corev1.VolumeSource{
		Secret: &secretvolsrc,
	}

	volumes := []corev1.Volume{
		{
			Name:         volumename,
			VolumeSource: vmvolume,
		},
		{
			Name:         "tmp-vol",
			VolumeSource: tmpvol,
		},
		{
			Name:         "kaniko-secret",
			VolumeSource: secretvol,
		},
	}

	// Define containers.

	// Define Docker pusher container.
	pushcontainer := corev1.Container{
		Name:  "docker-pusher",
		Image: "gcr.io/kaniko-project/executor:latest",
		Args: []string{"--dockerfile=/workspace/Dockerfile",
			fmt.Sprintf("--destination=registry.internal.crownlabs.polito.it/%s/%s:%s", imagedir, isnap.Spec.ImageName, imagetag)},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "tmp-vol",
				MountPath: "/workspace",
			},
			{
				Name:      "kaniko-secret",
				MountPath: "/kaniko/.docker/",
			},
		},
	}

	// Define image exporter container.
	exportcontainer := corev1.Container{
		Name: "img-generator",
		// TODO replace image
		Image: "claudiolor/img-exporter",
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volumename,
				MountPath: "/data",
			},
			{
				Name:      "tmp-vol",
				MountPath: "/img-tmp",
			},
		},
	}

	*job = batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isnap.Name,
			Namespace: isnap.Namespace,
		},
		Spec: batch.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						pushcontainer,
					},
					InitContainers: []corev1.Container{
						exportcontainer,
					},
					Volumes:       volumes,
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}
}
