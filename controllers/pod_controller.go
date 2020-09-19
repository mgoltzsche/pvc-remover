/*
Copyright 2020 Max Goltzsche.

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

package controllers

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var errPVCDelete = errors.New("pvc deletion failed")

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	StorageClassSelector map[string]struct{}
	PodPredicate         predicate.Predicate
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaim,verbs=get;list;watch;update;delete

func (r *PodReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("pod", req.NamespacedName)

	log.Info("Reconciling Pod")

	// Get Pod
	pod := &corev1.Pod{}
	err := r.Client.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Delete referenced PersistentVolumeClaims
	for _, pvcName := range writeablePVCs(pod) {
		pvcLog := log.WithValues("pvc", pvcName)
		pvc := &corev1.PersistentVolumeClaim{}
		e := r.Get(ctx, pvcName, pvc)
		if e != nil {
			if !apierrors.IsNotFound(e) {
				pvcLog.Error(e, "failed to get PVC")
				err = errPVCDelete
			}
			continue
		}
		if sc := pvc.Spec.StorageClassName; sc == nil ||
			!r.storageClassMatches(*sc) ||
			!hasAccessMode(pvc, corev1.ReadWriteOnce) {
			// Do not touch PVCs of other storage classes or other accessModes
			continue
		}
		if setAnnotation(pvc, AnnotationPersistentVolumeClaimNoProtection, Enabled) {
			e = r.Update(ctx, pvc)
			if e != nil {
				if !apierrors.IsNotFound(e) {
					pvcLog.Error(e, "Failed to set PVC annotation")
					err = errPVCDelete
				}
				continue
			}
		}
		if pvc.GetDeletionTimestamp() == nil {
			pvcLog.Info("Deleting PVC")
			e = r.Delete(ctx, pvc)
			if e != nil {
				if !apierrors.IsNotFound(e) {
					pvcLog.Error(e, "Failed to delete PVC")
					err = errPVCDelete
				}
				continue
			}
		}
	}

	return ctrl.Result{}, err
}

func hasAccessMode(pvc *corev1.PersistentVolumeClaim, mode corev1.PersistentVolumeAccessMode) bool {
	if len(pvc.Spec.AccessModes) != 1 {
		return false
	}
	return pvc.Spec.AccessModes[0] == mode
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if len(r.StorageClassSelector) == 0 {
		return errors.New("StorageClassSelector not specified")
	}
	var podPredicate predicate.Predicate
	podPredicate = predicate.NewPredicateFuncs(func(meta metav1.Object, o runtime.Object) bool {
		pod := o.(*corev1.Pod)
		podFailed := pod.Status.Phase == corev1.PodFailed
		podSucceeded := pod.Status.Phase == corev1.PodSucceeded
		finished := pod.Spec.RestartPolicy == corev1.RestartPolicyNever && (podSucceeded || podFailed) ||
			pod.Spec.RestartPolicy == corev1.RestartPolicyOnFailure && podSucceeded
		return finished && len(writeablePVCs(pod)) > 0
	})
	if r.PodPredicate != nil {
		podPredicate = predicate.And(r.PodPredicate, podPredicate)
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(podPredicate)).
		Complete(r)
}

func (r *PodReconciler) storageClassMatches(storageClassName string) bool {
	_, ok := r.StorageClassSelector[storageClassName]
	return ok
}

func writeablePVCs(pod *corev1.Pod) (pvcs []types.NamespacedName) {
	for _, v := range pod.Spec.Volumes {
		if pvc := v.PersistentVolumeClaim; pvc != nil && !pvc.ReadOnly {
			pvcs = append(pvcs, types.NamespacedName{Name: pvc.ClaimName, Namespace: pod.Namespace})
		}
	}
	return
}

func setAnnotation(meta metav1.Object, key, value string) bool {
	a := meta.GetAnnotations()
	if a == nil {
		a = map[string]string{}
	}
	if a[key] != value {
		a[key] = value
		meta.SetAnnotations(a)
		return true
	}
	return false
}
