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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	batch "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crownlabsv1alpha2 "github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
)

// InstanceSnapshotReconciler reconciles a InstanceSnapshot object
type InstanceSnapshotReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=crownlabs.polito.it,resources=instancesnapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crownlabs.polito.it,resources=instancesnapshots/status,verbs=get;update;patch

func (r *InstanceSnapshotReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("instancesnapshot", req.NamespacedName)

	isnap := &crownlabsv1alpha2.InstanceSnapshot{}

	if err := r.Get(ctx, req.NamespacedName, isnap); client.IgnoreNotFound(err) != nil{
		klog.Errorf("Error when getting InstanceSnapshot %s before starting reconcile -> %s", isnap.Name, err)
		return ctrl.Result{}, err
	} else if err != nil {
		klog.Infof("InstanceSnapshot %s already deleted", isnap.Name)
		return ctrl.Result{}, nil
	}

	klog.Info("Start InstanceSnapshot reconciliation")

	// Get the job to be created
	snapjob := r.GetSnapshottingJob(isnap)
	err := ctrl.SetControllerReference(snapjob, snapjob, r.Scheme)
	if err != nil {
		// Try again assignment
		return ctrl.Result{}, err
	}

	// Check the current status of the InstanceSnapshot by checking
	// the state of its assigned job
	jobName := types.NamespacedName{
		Namespace: snapjob.Namespace,
		Name:      snapjob.Name,
	}
	found := &batch.Job{}
	err = r.Get(ctx, jobName, found)

	if err != nil && errors.IsNotFound(err) {
		// Job not created yet
		// Update the current status of creation of the snapshot
		isnap.Status.Phase = "Pending"
		cerr := r.Update(ctx, isnap)
		if cerr != nil {
			klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, cerr)
			return ctrl.Result{}, cerr
		}

		// Check the validity of the request before creating the job
		// First it is needed to check if the instance actually exists
		instanceName := types.NamespacedName{
			Namespace: isnap.Spec.Instance.Namespace,
			Name:      isnap.Spec.Instance.Name,
		}
		instance := &crownlabsv1alpha2.Instance{}
		cerr = r.Get(ctx, instanceName, instance)
		if cerr != nil && errors.IsNotFound(cerr) {
			// The declared instance does not exist, set the phase as failed
			// and don't try again
			klog.Errorf("Instance %s not found in namespace %s. It is not possible to complete the InstanceSnapshot %s",
				instanceName.Name, instanceName.Namespace, isnap.Name)

			// Set the status as failed
			isnap.Status.Phase = "Failed"
			uerr := r.Update(ctx, isnap)
			if uerr != nil {
				klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, cerr)
				return ctrl.Result{}, uerr
			}

			return ctrl.Result{}, nil
		} else if cerr != nil {
			klog.Errorf("Error in retrieving the instance for InstanceSnapshot %s -> %s", isnap.Name, cerr)
			return ctrl.Result{}, cerr
		}

		// Get the template of the instance in order to check if it has the requirements to be snapshotted
		templateName := types.NamespacedName{
			Namespace: instance.Spec.Template.Namespace,
			Name:      instance.Spec.Template.Name,
		}
		template := &crownlabsv1alpha2.Template{}
		cerr = r.Get(ctx, templateName, template)
		if cerr != nil && errors.IsNotFound(cerr) {
			// The declared template does not exist set the phase as failed
			// and don't try again
			klog.Errorf("Instance %s not found in namespace %s. It is not possible to complete the InstanceSnapshot %s",
				instanceName.Name, instanceName.Namespace, isnap.Name)

			// Set the status as failed
			isnap.Status.Phase = "Failed"
			uerr := r.Update(ctx, isnap)
			if uerr != nil {
				klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, cerr)
				return ctrl.Result{}, uerr
			}

			return ctrl.Result{}, nil
		} else if cerr != nil {
			klog.Errorf("Error in retrieving the template for InstanceSnapshot %s -> %s", isnap.Name, cerr)
			return ctrl.Result{}, cerr
		}

		// In order to create a snapshot of the vm, we need first to check that:
		// - the vm is powered off, since it is not possible to steal the DataVolume if it is still running;
		// - the environment is actually a vm and not a container
		var env *crownlabsv1alpha2.Environment = nil

		if isnap.Spec.EnvName != "" {
			for i := range template.Spec.EnvironmentList{
				if template.Spec.EnvironmentList[i].Name == isnap.Spec.EnvName {
					env = &template.Spec.EnvironmentList[i]
					break
				}
			}

			// Check if the specified environment was found
			if env == nil {
				klog.Errorf("Environment %s not found in template %s. It is not possible to complete the InstanceSnapshot %s",
					isnap.Spec.EnvName, template.Name, isnap.Name)

				// Set the status as failed
				isnap.Status.Phase = "Failed"
				uerr := r.Update(ctx, isnap)
				if uerr != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, cerr)
					return ctrl.Result{}, uerr
				}
				return ctrl.Result{}, nil
			}
		} else {
			env = &template.Spec.EnvironmentList[0]
		}

		// Check if the environment is a VM
		if env.EnvironmentType != crownlabsv1alpha2.ClassVM {
			klog.Errorf("Environment %s is not a VM. It is not possible to complete the InstanceSnapshot %s",
				env.Name, isnap.Name)

			// Set the status as failed
			isnap.Status.Phase = "Failed"
			uerr := r.Update(ctx, isnap)
			if uerr != nil {
				klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, cerr)
				return ctrl.Result{}, uerr
			}
			return ctrl.Result{}, nil
		}

		// Once all the checks have been completed, it is possible to proceed
		// with the job creation
		cerr = r.Create(ctx, snapjob)
		if cerr != nil {
			// It was not possible to create the job
			klog.Errorf("Error when creating the job for %s -> %s", isnap.Name, err)
			return ctrl.Result{}, err
		}
	} else if err != nil {
		// It was not possible to retrieve the job
		klog.Errorf("Unable to retrieve the job of InstanceSnapshot %s -> %s", isnap.Name, err)
		return ctrl.Result{}, err
	} else {
		// Check the current status of the job
		completed, jstatus := r.GetJobStatus(found)
		if completed {
			if jstatus == batch.JobComplete {
				// Awesome, our job completed and the image has been uploaded to the registry
				isnap.Status.Phase = "Completed"
				err = r.Update(ctx, isnap)
				if err != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err)
					return ctrl.Result{}, err
				}
			} else {
				// Unfortunately the creation of the snapshot failed since the job failed
				isnap.Status.Phase = "Failed"
				err = r.Update(ctx, isnap)
				if err != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err)
					return ctrl.Result{}, err
				}
			}
		}
	}


	return ctrl.Result{}, nil
}


// This function gets a Job and returns its status
func (r *InstanceSnapshotReconciler) GetJobStatus(job *batch.Job) (bool, batch.JobConditionType) {
	for _, c := range job.Status.Conditions {
		// If the status corresponding to Success or failed is true, it means that the job completed
		if c.Status == corev1.ConditionTrue && (c.Type == batch.JobFailed || c.Type == batch.JobComplete) {
			return true, c.Type
		}
	}

	return false, ""
}

func (r *InstanceSnapshotReconciler) GetSnapshottingJob(isnap *crownlabsv1alpha2.InstanceSnapshot) *batch.Job {
	return &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isnap.Name+"-job",
			Namespace: isnap.Namespace,
		},
		Spec: batch.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "job-test",
							Image: "alpine",
							Command: [] string{"/bin/sh", "-c", "sleep 30"},
						},
					},
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}
}

func (r *InstanceSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// The generation changed predicate allow to avoid updates on the status changes of the InstanceSnapshot
		For(&crownlabsv1alpha2.InstanceSnapshot{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&batch.Job{}).
		Complete(r)
}
