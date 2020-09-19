package controllers

import (
	. "github.com/onsi/ginkgo"
)

var _ = Describe("PodController", func() {
	Describe("completed pod", func() {
		It("should annotate and delete matching PVC of matching Pod", func() {
			verify(pvcMatching, hasBeenDeleted(pvcMatching))
		})
		It("should not annotate or delete not matching PVC", func() {
			verify(pvcOther, not(hasBeenDeleted(pvcMatchingActive)))
		})
	})
	Describe("running pod", func() {
		It("should not annotate or delete PVC of not matching Pod", func() {
			verify(pvcMatchingActive, not(hasBeenDeleted(pvcMatchingActive)))
		})
	})
	Describe("restarting pod", func() {
		It("should not annotate or delete PVC of not matching Pod", func() {
			verify(pvcRestarting, not(hasBeenDeleted(pvcRestarting)))
		})
	})
})
