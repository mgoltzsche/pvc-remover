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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	AnnotationPersistentVolumeClaimNoProtection = "pvc-remover.mgoltzsche.github.com/no-pvc-protection"
	Enabled                                     = "true"
	finalizerPVCProtection                      = "kubernetes.io/pvc-protection"
)

// PersistentVolumeClaimReconciler reconciles a PersistentVolumeClaim object
type PersistentVolumeClaimReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/status,verbs=get;update;patch

func (r *PersistentVolumeClaimReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("persistentvolumeclaim", req.NamespacedName)

	log.Info("Reconciling PersistentVolumeClaim")

	// Get PersistentVolumeClaim
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Client.Get(ctx, req.NamespacedName, pvc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Remove finalizer when PVC deletion is pending
	if pvc.GetDeletionTimestamp() != nil {
		if removeFinalizer(pvc, finalizerPVCProtection) {
			log.Info("Removing finalizer of deleted PVC")
			err = r.Update(ctx, pvc)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return ctrl.Result{}, nil
				}
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *PersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}, builder.WithPredicates(
			predicate.And(
				hasAnnotation(AnnotationPersistentVolumeClaimNoProtection, Enabled),
				hasDeletionTimestamp(),
			),
		)).
		Complete(r)
}

func hasAnnotation(annotation, value string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(meta metav1.Object, _ runtime.Object) bool {
		a := meta.GetAnnotations()
		return a != nil && a[annotation] == value
	})
}

func hasDeletionTimestamp() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(meta metav1.Object, _ runtime.Object) bool {
		return meta.GetDeletionTimestamp() != nil
	})
}

func removeFinalizer(meta metav1.Object, finalizer string) bool {
	finalizers := meta.GetFinalizers()
	filtered := make([]string, 0, len(finalizers))
	for _, f := range finalizers {
		if f != finalizer {
			filtered = append(filtered, f)
		}
	}
	meta.SetFinalizers(filtered)
	return len(finalizers) > len(filtered)
}
