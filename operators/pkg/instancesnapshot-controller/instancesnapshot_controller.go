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

// Package instancesnapshot_controller groups the functionalities related to the creation of a persistent VM snapshot.
package instancesnapshot_controller

import (
	"context"
	"fmt"

	batch "k8s.io/api/batch/v1"
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

	crownlabsv1alpha2 "github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"
)

// InstanceSnapshotReconciler reconciles a InstanceSnapshot object.
type InstanceSnapshotReconciler struct {
	client.Client
	EventsRecorder     record.EventRecorder
	Scheme             *runtime.Scheme
	NamespaceWhitelist metav1.LabelSelector
}

// Reconcile reconciles the status of the InstanceSnapshot resource.
func (r *InstanceSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	isnap := &crownlabsv1alpha2.InstanceSnapshot{}

	if err := r.Get(ctx, req.NamespacedName, isnap); client.IgnoreNotFound(err) != nil {
		klog.Errorf("Error when getting InstanceSnapshot %s before starting reconcile -> %s", isnap.Name, err)
		return ctrl.Result{}, err
	} else if err != nil {
		klog.Infof("InstanceSnapshot %s already deleted", isnap.Name)
		return ctrl.Result{}, nil
	}

	// Check the selector label, in order to know whether to perform or not reconciliation.
	if proceed, err := r.CheckSelectorLabel(ctx, isnap, req); !proceed {
		// If there was an error while checking, show the error and try again.
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	klog.Info("Start InstanceSnapshot reconciliation")

	// Get the job to be created
	snapjob := &batch.Job{}
	r.GetSnapshottingJob(isnap, snapjob)

	// Check the current status of the InstanceSnapshot by checking
	// the state of its assigned job.
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
		if err1 := r.Status().Update(ctx, isnap); err1 != nil {
			klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err1)
			return ctrl.Result{}, err1
		}

		if retry, err1 := r.ValidateRequest(ctx, isnap); err1 != nil {
			// Print the validation error in the log and check if there is the need to
			// set the operation as failed, or to try again.
			klog.Error(err1)
			if retry {
				return ctrl.Result{}, err1
			}

			// Set the status as failed
			isnap.Status.Phase = crownlabsv1alpha2.Failed
			if uerr := r.Status().Update(ctx, isnap); uerr != nil {
				klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, uerr)
				return ctrl.Result{}, uerr
			}

			// Add the event and stop reconciliation since the request is not valid.
			r.EventsRecorder.Event(isnap, "Warning", "ValidationError", fmt.Sprintf("%s", err1))
			return ctrl.Result{}, nil
		}

		// Set the owner reference in order to delete the job when the InstanceSnapshot is deleted.
		if err1 := ctrl.SetControllerReference(isnap, snapjob, r.Scheme); err1 != nil {
			klog.Error("Unable to set ownership")
			return ctrl.Result{}, err1
		}

		if err1 := r.Create(ctx, snapjob); err1 != nil {
			// It was not possible to create the job
			klog.Errorf("Error when creating the job for %s -> %s", isnap.Name, err1)
			return ctrl.Result{}, err1
		}

		isnap.Status.Phase = crownlabsv1alpha2.Processing
		if err1 := r.Status().Update(ctx, isnap); err1 != nil {
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
				// The job is completed and the image has been uploaded to the registry
				isnap.Status.Phase = crownlabsv1alpha2.Completed

				if err = r.Status().Update(ctx, isnap); err != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err)
					return ctrl.Result{}, err
				}

				extime := found.Status.CompletionTime.Sub(found.Status.StartTime.Time)
				klog.Infof("Image %s created and uploaded in %s", isnap.Spec.ImageName, extime)
				r.EventsRecorder.Event(isnap, "Normal", "Created", fmt.Sprintf("Image %s created and uploaded in %s", isnap.Spec.ImageName, extime))
			} else {
				// The creation of the snapshot failed since the job failed
				isnap.Status.Phase = crownlabsv1alpha2.Failed
				if err = r.Status().Update(ctx, isnap); err != nil {
					klog.Errorf("Error when updating status of InstanceSnapshot %s -> %s", isnap.Name, err)
					return ctrl.Result{}, err
				}

				klog.Infof("Image %s could not be created", isnap.Spec.ImageName)
				r.EventsRecorder.Event(isnap, "Warning", "CreationFailed", "The creation job failed")
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers a new controller for InstanceSnapshot resources.
func (r *InstanceSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// The generation changed predicate allow to avoid updates on the status changes of the InstanceSnapshot
		For(&crownlabsv1alpha2.InstanceSnapshot{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&batch.Job{}).
		Complete(r)
}
