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

package instance_controller

import (
	"context"
	"fmt"
	batch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	crownlabsv1alpha2 "github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
)

// InstanceSnapshotReconciler reconciles a InstanceSnapshot object.
type InstanceSnapshotReconciler struct {
	client.Client
	EventsRecorder record.EventRecorder
	Log            logr.Logger
	Scheme         *runtime.Scheme
}

// Reconcile reconciles the status of the InstanceSnapshot resource.
func (r *InstanceSnapshotReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("instancesnapshot", req.NamespacedName)

	isnap := &crownlabsv1alpha2.InstanceSnapshot{}

	if err := r.Get(ctx, req.NamespacedName, isnap); client.IgnoreNotFound(err) != nil {
		klog.Errorf("Error when getting InstanceSnapshot %s before starting reconcile -> %s", isnap.Name, err)
		return ctrl.Result{}, err
	} else if err != nil {
		klog.Infof("InstanceSnapshot %s already deleted", isnap.Name)
		return ctrl.Result{}, nil
	}

	klog.Info("Start InstanceSnapshot reconciliation")

	// Get the job to be created
	snapjob := &batch.Job{}
	r.GetSnapshottingJob(isnap, snapjob)

	// Check the current status of the InstanceSnapshot by checking
	// the state of its assigned job
	jobName := types.NamespacedName{
		Namespace: snapjob.Namespace,
		Name:      snapjob.Name,
	}
	found := &batch.Job{}
	err := r.Get(ctx, jobName, found)

	switch {
	case err != nil && errors.IsNotFound(err):
		r.EventsRecorder.Event(isnap, "Normal", "Validating", "Start validation of the request")
		isnap.Status.Phase = crownlabsv1alpha2.Pending
		err1 := r.Status().Update(ctx, isnap)
		if err1 != nil {
			klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err1)
			return ctrl.Result{}, err1
		}
		retry, err1 := r.ValidateRequest(isnap)
		if err1 != nil {
			// Print the validation error in the log and check if we need to
			// set the operation as failed, or if we need to try again.
			klog.Error(err1)
			if retry {
				return ctrl.Result{}, err1
			}

			// Set the status as failed
			isnap.Status.Phase = crownlabsv1alpha2.Failed
			uerr := r.Status().Update(ctx, isnap)
			if uerr != nil {
				klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, uerr)
				return ctrl.Result{}, uerr
			}

			// Add the event and stop reconciliation since the request is not valid
			r.EventsRecorder.Event(isnap, "Warning", "ValidationError", fmt.Sprintf("%s", err1))
			return ctrl.Result{}, nil
		}
		err1 = ctrl.SetControllerReference(isnap, snapjob, r.Scheme)
		if err1 != nil {
			klog.Error("Unable to set ownership")
			return ctrl.Result{}, err1
		}
		err1 = r.Create(ctx, snapjob)
		if err1 != nil {
			// It was not possible to create the job
			klog.Errorf("Error when creating the job for %s -> %s", isnap.Name, err1)
			return ctrl.Result{}, err1
		}
		isnap.Status.Phase = crownlabsv1alpha2.Processing
		err1 = r.Status().Update(ctx, isnap)
		if err1 != nil {
			klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err1)
			return ctrl.Result{}, err1
		}
		klog.Infof("Job %s for snapshot creation started", snapjob.Name)
		r.EventsRecorder.Event(isnap, "Normal", "Creation", fmt.Sprintf("Job %s for snapshot creation started", snapjob.Name))
	case err != nil:
		klog.Errorf("Unable to retrieve the job of InstanceSnapshot %s -> %s", isnap.Name, err)
		return ctrl.Result{}, err
	default:
		completed, jstatus := r.GetJobStatus(found)
		if completed {
			if jstatus == batch.JobComplete {
				// Awesome, our job completed and the image has been uploaded to the registry
				isnap.Status.Phase = crownlabsv1alpha2.Completed

				err = r.Status().Update(ctx, isnap)
				if err != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err)
					return ctrl.Result{}, err
				}

				extime := found.Status.CompletionTime.Sub(found.Status.StartTime.Time)
				klog.Infof("Image %s created and uploaded in %s", isnap.Spec.ImageName, extime)
				r.EventsRecorder.Event(isnap, "Normal", "Created", fmt.Sprintf("Image %s created and uploaded in %s", isnap.Spec.ImageName, extime))
			} else {
				// Unfortunately the creation of the snapshot failed since the job failed
				isnap.Status.Phase = crownlabsv1alpha2.Failed
				err = r.Status().Update(ctx, isnap)
				if err != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err)
					return ctrl.Result{}, err
				}

				klog.Info("The creation job failed")
				r.EventsRecorder.Event(isnap, "Warning", "CreationFailed", "The creation job failed")
			}
		}
	}

	return ctrl.Result{}, nil
}

// ValidateRequest validates the InstanceSnapshot request, returns an error and if it is needed to try again.
func (r *InstanceSnapshotReconciler) ValidateRequest(isnap *crownlabsv1alpha2.InstanceSnapshot) (bool, error) {
	ctx := context.Background()

	// First it is needed to check if the instance actually exists
	instanceName := types.NamespacedName{
		Namespace: isnap.Spec.Instance.Namespace,
		Name:      isnap.Spec.Instance.Name,
	}
	instance := &crownlabsv1alpha2.Instance{}
	err := r.Get(ctx, instanceName, instance)
	if err != nil && errors.IsNotFound(err) {
		// The declared instance does not exist and don't try again
		return false, fmt.Errorf("instance %s not found in namespace %s. It is not possible to complete the InstanceSnapshot %s",
			instanceName.Name, instanceName.Namespace, isnap.Name)
	} else if err != nil {
		return true, fmt.Errorf("error in retrieving the instance for InstanceSnapshot %s -> %w", isnap.Name, err)
	}

	// Get the template of the instance in order to check if it has the requirements to be snapshotted
	// In order to create a snapshot of the vm, we need first to check that:
	// - the vm is powered off, since it is not possible to steal the DataVolume if it is still running;
	// - the environment is actually a vm and not a container.

	templateName := types.NamespacedName{
		Namespace: instance.Spec.Template.Namespace,
		Name:      instance.Spec.Template.Name,
	}
	template := &crownlabsv1alpha2.Template{}
	err = r.Get(ctx, templateName, template)
	if err != nil && errors.IsNotFound(err) {
		// The declared template does not exist set the phase as failed
		// and don't try again
		return false, fmt.Errorf("instance %s not found in namespace %s. It is not possible to complete the InstanceSnapshot %s",
			instanceName.Name, instanceName.Namespace, isnap.Name)
	} else if err != nil {
		return true, fmt.Errorf("error in retrieving the template for InstanceSnapshot %s -> %w", isnap.Name, err)
	}

	var env *crownlabsv1alpha2.Environment = nil

	if isnap.Spec.Environment.Name != "" {
		for i := range template.Spec.EnvironmentList {
			if template.Spec.EnvironmentList[i].Name == isnap.Spec.Environment.Name {
				env = &template.Spec.EnvironmentList[i]
				break
			}
		}

		// Check if the specified environment was found
		if env == nil {
			return false, fmt.Errorf("environment %s not found in template %s. It is not possible to complete the InstanceSnapshot %s",
				isnap.Spec.Environment.Name, template.Name, isnap.Name)
		}
	} else {
		env = &template.Spec.EnvironmentList[0]
	}

	// Check if the environment is a persistent VM
	if env.EnvironmentType != crownlabsv1alpha2.ClassVM {
		return false, fmt.Errorf("environment %s is not a VM. It is not possible to complete the InstanceSnapshot %s",
			env.Name, isnap.Name)
	}

	if !env.Persistent {
		return false, fmt.Errorf("environment %s is not a persistent VM. It is not possible to complete the InstanceSnapshot %s",
			env.Name, isnap.Name)
	}

	// Finally check if the VM is running
	if instance.Spec.Running {
		return false, fmt.Errorf("the vm is running. It is not possible to complete the InstanceSnapshot %s", isnap.Name)
	}

	// Check if the instance is not running
	/*vmi := &virt.VirtualMachineInstance{}
	// Prepare instance name
	name := strings.ReplaceAll(instance.Name, ".", "-")
	vmiName := types.NamespacedName{
		Name:      name,
		Namespace: instance.Namespace,
	}

	err = r.Get(ctx, vmiName, vmi)
	if err == nil {
		return fmt.Errorf("the vm is running. It is not possible to complete the InstanceSnapshot %s", isnap.Name), false
	}else if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("error in retrieving the vmi for InstanceSnapshot %s -> %s", isnap.Name, err), true
	}*/

	return false, nil
}

// GetJobStatus sets a Job and returns its status.
func (r *InstanceSnapshotReconciler) GetJobStatus(job *batch.Job) (bool, batch.JobConditionType) {
	for _, c := range job.Status.Conditions {
		// If the status corresponding to Success or failed is true, it means that the job completed
		if c.Status == corev1.ConditionTrue && (c.Type == batch.JobFailed || c.Type == batch.JobComplete) {
			return true, c.Type
		}
	}

	return false, ""
}

// GetSnapshottingJob generates the job to be created.
func (r *InstanceSnapshotReconciler) GetSnapshottingJob(isnap *crownlabsv1alpha2.InstanceSnapshot, job *batch.Job) {
	*job = batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isnap.Name + "-job",
			Namespace: isnap.Namespace,
		},
		Spec: batch.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "job-test",
							Image:   "alpine",
							Command: []string{"/bin/sh", "-c", "sleep 2"},
						},
					},
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}
}

// SetupWithManager registers a new controller for InstanceSnapshot resources.
func (r *InstanceSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// The generation changed predicate allow to avoid updates on the status changes of the InstanceSnapshot
		For(&crownlabsv1alpha2.InstanceSnapshot{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&batch.Job{}).
		Complete(r)
}
