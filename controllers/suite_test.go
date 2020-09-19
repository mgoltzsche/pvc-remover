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
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg                     *rest.Config
	k8sClient               client.Client
	testEnv                 *envtest.Environment
	tearDownCh              = make(chan struct{}, 1)
	testNamespace           string
	namespaceResource       = &corev1.Namespace{}
	storageClassPodMatching = &storagev1.StorageClass{}
	storageClassOther       = &storagev1.StorageClass{}
	podCompleted            = &corev1.Pod{}
	podRunning              = &corev1.Pod{}
	podRestarting           = &corev1.Pod{}
	pvcMatchingActive       = &corev1.PersistentVolumeClaim{}
	pvcMatching             = &corev1.PersistentVolumeClaim{}
	pvcOther                = &corev1.PersistentVolumeClaim{}
	pvcRestarting           = &corev1.PersistentVolumeClaim{}
	pvMatchingActive        = &corev1.PersistentVolume{}
	pvMatching              = &corev1.PersistentVolume{}
	pvOther                 = &corev1.PersistentVolume{}
	pvRestarting            = &corev1.PersistentVolume{}
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.LoggerTo(GinkgoWriter, true))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = corev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = corev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	// Set up test Namespace and StorageClass
	Eventually(func() error {
		testNamespace = fmt.Sprintf("test-namespace-%d", rand.Int63())
		namespaceResource.Name = testNamespace
		return k8sClient.Create(context.TODO(), namespaceResource)
	}, "10s", "1s").ShouldNot(HaveOccurred())
	createStorageClass(storageClassPodMatching, "-match")
	createStorageClass(storageClassOther, "-other")

	// Set up controller manager
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme.Scheme,
		LeaderElection:     false,
		MetricsBindAddress: "127.0.0.1:0",
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(mgr).ToNot(BeNil())
	go func() {
		defer GinkgoRecover()
		err = mgr.Start(tearDownCh)
		Expect(err).ToNot(HaveOccurred())
	}()

	// Set up controllers
	podAnnotationSelector := labels.SelectorFromSet(map[string]string{AnnotationPersistentVolumeClaimNoProtection: Enabled})
	podPredicate := predicate.NewPredicateFuncs(func(meta metav1.Object, o runtime.Object) bool {
		return podAnnotationSelector.Matches(labels.Set(meta.GetAnnotations()))
	})
	err = (&PodReconciler{
		Client:               mgr.GetClient(),
		Log:                  ctrl.Log.WithName("controllers").WithName("Pod"),
		Scheme:               mgr.GetScheme(),
		PodPredicate:         podPredicate,
		StorageClassSelector: map[string]struct{}{storageClassPodMatching.Name: struct{}{}},
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())
	err = (&PersistentVolumeClaimReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("PersistentVolumeClaim"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	// Set up test data
	createPVC(pvcMatching, "matching-pvc", storageClassPodMatching.Name)
	createPVC(pvcOther, "other-pvc", storageClassOther.Name)
	createPV(pvMatching, pvcMatching)
	createPV(pvOther, pvcOther)
	createPod(podCompleted, "completed", []string{"true"}, corev1.RestartPolicyNever, pvcOther, pvcMatching)
	setPodPhase(podCompleted, corev1.PodSucceeded)

	createPVC(pvcMatchingActive, "matching-active-pvc", storageClassPodMatching.Name)
	createPV(pvMatchingActive, pvcMatchingActive)
	createPod(podRunning, "running", []string{"/bin/sleep", "10000"}, corev1.RestartPolicyNever, pvcMatchingActive)

	createPVC(pvcRestarting, "restarting-pvc", storageClassPodMatching.Name)
	createPV(pvRestarting, pvcRestarting)
	createPod(podRestarting, "restarting", []string{"true"}, corev1.RestartPolicyAlways, pvcRestarting)
	setPodPhase(podRestarting, corev1.PodSucceeded)

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	podDelErr := k8sClient.Delete(context.TODO(), podCompleted)
	for _, o := range []runtime.Object{pvMatchingActive, pvMatching, pvOther, pvRestarting} {
		k8sClient.Delete(context.TODO(), o)
	}
	k8sClient.Delete(context.TODO(), namespaceResource)
	k8sClient.Delete(context.TODO(), storageClassOther)
	k8sClient.Delete(context.TODO(), storageClassPodMatching)
	close(tearDownCh)
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
	Expect(podDelErr).ShouldNot(HaveOccurred())
})

func createStorageClass(sc *storagev1.StorageClass, suffix string) {
	sc.Name = testNamespace + "-sc" + suffix
	sc.Provisioner = testNamespace + "-pv-provisioner"
	err := k8sClient.Create(context.TODO(), sc)
	Expect(err).ShouldNot(HaveOccurred())
}

func createPVC(pvc *corev1.PersistentVolumeClaim, name string, scName string) {
	pvc.Name = name
	pvc.Namespace = testNamespace
	pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	pvc.Spec.StorageClassName = &scName
	pvc.Spec.Resources.Requests = map[corev1.ResourceName]resource.Quantity{corev1.ResourceStorage: resource.MustParse("1G")}
	pvc.Finalizers = []string{finalizerPVCProtection}
	err := k8sClient.Create(context.TODO(), pvc)
	Expect(err).ShouldNot(HaveOccurred())
}

func createPV(pv *corev1.PersistentVolume, pvc *corev1.PersistentVolumeClaim) {
	pv.Name = testNamespace + "-" + pvc.Name
	pv.Namespace = testNamespace
	pv.Spec.StorageClassName = *pvc.Spec.StorageClassName
	pv.Spec.AccessModes = pvc.Spec.AccessModes
	pv.Spec.HostPath = &corev1.HostPathVolumeSource{
		Path: "/tmp", // some existing path
	}
	pv.Spec.Capacity = pvc.Spec.Resources.Requests
	apiVersion, kind := pvc.GroupVersionKind().ToAPIVersionAndKind()
	pv.Spec.ClaimRef = &corev1.ObjectReference{
		APIVersion:      apiVersion,
		Kind:            kind,
		Name:            pvc.Name,
		Namespace:       pvc.Namespace,
		UID:             pvc.UID,
		ResourceVersion: pvc.ResourceVersion,
	}
	err := k8sClient.Create(context.TODO(), pv)
	Expect(err).ShouldNot(HaveOccurred())
}

func createPod(pod *corev1.Pod, name string, args []string, restartPolicy corev1.RestartPolicy, pvcs ...*corev1.PersistentVolumeClaim) {
	pod.Name = name
	pod.Namespace = testNamespace
	pod.Annotations = map[string]string{AnnotationPersistentVolumeClaimNoProtection: Enabled}
	pod.Spec.RestartPolicy = restartPolicy
	c := corev1.Container{
		Name:  "fakecontainer",
		Image: "alpine:3.12",
		Args:  args,
	}
	for _, pvc := range pvcs {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: pvc.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvc.Name,
					ReadOnly:  false,
				},
			},
		})
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      pvc.Name,
			MountPath: "/" + pvc.Name,
		})
	}
	pod.Spec.Containers = []corev1.Container{c}
	err := k8sClient.Create(context.TODO(), pod)
	Expect(err).ShouldNot(HaveOccurred())
}

func setPodPhase(pod *corev1.Pod, phase corev1.PodPhase) {
	Eventually(func() (err error) {
		defer GinkgoRecover()
		if pod.Status.Phase != phase {
			pod.Status.Phase = phase
			err = k8sClient.Status().Update(context.TODO(), pod)
		}
		return err
	}, "15s", "1s").ShouldNot(HaveOccurred())
}

type k8sResource interface {
	runtime.Object
	metav1.Object
}

func verify(o k8sResource, fn func(error) error) {
	Eventually(func() error {
		defer GinkgoRecover()
		name := types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}
		return fn(k8sClient.Get(context.TODO(), name, o))
	}, "10s", "1s").ShouldNot(HaveOccurred())
}

func hasBeenDeleted(pvc *corev1.PersistentVolumeClaim) func(error) error {
	return func(err error) error {
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if pvc.Annotations[AnnotationPersistentVolumeClaimNoProtection] != Enabled {
			err = fmt.Errorf("PVC has not been annotated. %s", err)
		}
		if pvc.GetDeletionTimestamp() == nil {
			err = fmt.Errorf("PVC has no deletion timestamp")
		}
		return err
	}
}

func not(fn func(error) error) func(error) error {
	time.Sleep(5)
	return func(err error) error {
		err = fn(err)
		if err == nil {
			return fmt.Errorf("expected error to be returned")
		}
		return nil
	}
}
