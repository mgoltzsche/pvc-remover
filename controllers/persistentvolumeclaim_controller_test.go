package controllers

import (
	"context"

	. "github.com/onsi/ginkgo"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("PersistentVolumeClaimController", func() {
	Context("annotated", func() {
		var (
			pod = &corev1.Pod{}
			pvc = &corev1.PersistentVolumeClaim{}
			pv  = &corev1.PersistentVolume{}
		)
		BeforeEach(func() {
			pvc.Annotations = map[string]string{AnnotationPersistentVolumeClaimNoProtection: Enabled}
			createPVC(pvc, "ephemeral-pvc", storageClassOther.Name)
			createPV(pv, pvc)
			createPod(pod, "pvcdeletionblocker", []string{"true"}, corev1.RestartPolicyNever, pvc)
		})
		AfterEach(func() {
			k8sClient.Delete(context.TODO(), pod)
			k8sClient.Delete(context.TODO(), pvc)
			k8sClient.Delete(context.TODO(), pv)
		})
		It("should remove protective finalizer", func() {
			// TODO: make this test reliable within a kubebuilder environment
			//   (finalizer seems to be ignored there so that the PVC gets
			//   deleted even when the controller doesn't remove the finalizer)
			k8sClient.Delete(context.TODO(), pvc)
			verify(pvc, hasBeenDeleted(pvc))
		})
	})
})
