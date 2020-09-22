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

package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mgoltzsche/pvc-remover/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(corev1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var watchNamespace string
	var metricsAddr string
	var enableLeaderElection bool
	podAnnotationSelectors := selectorsFlag{LabelsFn: getAnnotations}
	podLabelSelectors := selectorsFlag{LabelsFn: getLabels}
	storageClassSelector := map[string]struct{}{}
	flag.StringVar(&watchNamespace, "namespace", "", "The namespace that is watched")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Var(&podAnnotationSelectors, "pod-annotation", "Annotation selector used to filter Pods (optional)")
	flag.Var(&podLabelSelectors, "pod-label", "Label selector used to filter Pods (optional)")
	flag.Var(setFlag(storageClassSelector), "storage-class", "Names of ephemeral StorageClasses whose PVs can be deleted without risk on Pod completion")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	if watchNamespace == "" {
		watchNamespace = os.Getenv("WATCH_NAMESPACE")
	}

	if len(storageClassSelector) == 0 {
		setupLog.Error(nil, "CLI option --storage-class was not specified (must restrict PVC removal to ephemeral storage classes)")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		Namespace:          watchNamespace,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "9fd56ca1.pvc-remover.mgoltzsche.github.com",
	})
	if err != nil {
		setupLog.Error(err, "Unable to start manager")
		os.Exit(1)
	}

	setupLog.Info(fmt.Sprintf("Matching Pods with (annotations(%s) or labels(%s)) and PVCs with storageClass(%s)", podAnnotationSelectors.String(), podLabelSelectors.String(), storageClassNames(storageClassSelector)))

	podPredicate := podAnnotationSelectors.Predicate()
	if podLabelPredicate := podLabelSelectors.Predicate(); podLabelPredicate != nil {
		if podPredicate == nil {
			podPredicate = podLabelPredicate
		} else {
			podPredicate = predicate.Or(podPredicate, podLabelPredicate)
		}
	}

	if err = (&controllers.PodReconciler{
		Client:               mgr.GetClient(),
		Log:                  ctrl.Log.WithName("controllers").WithName("Pod"),
		Scheme:               mgr.GetScheme(),
		PodPredicate:         podPredicate,
		StorageClassSelector: storageClassSelector,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	if err = (&controllers.PersistentVolumeClaimReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("PersistentVolumeClaim"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Unable to create controller", "controller", "PersistentVolumeClaim")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Problem running manager")
		os.Exit(1)
	}
}

func storageClassNames(sc map[string]struct{}) string {
	l := make([]string, 0, len(sc))
	for k := range sc {
		l = append(l, k)
	}
	sort.Strings(l)
	return strings.Join(l, ",")
}

func getAnnotations(o metav1.Object) map[string]string { return o.GetAnnotations() }

func getLabels(o metav1.Object) map[string]string { return o.GetLabels() }

type selectorsFlag struct {
	LabelsFn  func(o metav1.Object) map[string]string
	selectors []labels.Selector
}

func (f *selectorsFlag) String() string {
	l := make([]string, len(f.selectors))
	for i, s := range f.selectors {
		l[i] = s.String()
	}
	return strings.Join(l, " or ")
}

func (f *selectorsFlag) Set(expr string) error {
	if expr == "" {
		return fmt.Errorf("no selector specified")
	}
	selector, err := labels.Parse(expr)
	if err != nil {
		return err
	}
	f.selectors = append(f.selectors, selector)
	return nil
}

func (f *selectorsFlag) Predicate() predicate.Predicate {
	if len(f.selectors) == 0 {
		return nil
	}
	predicates := make([]predicate.Predicate, len(f.selectors))
	for i, s := range f.selectors {
		predicates[i] = selectorPredicate(s, f.LabelsFn)
	}
	return predicate.Or(predicates...)
}

func selectorPredicate(selector labels.Selector, labelsFn func(metav1.Object) map[string]string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(meta metav1.Object, o runtime.Object) bool {
		return selector.Matches(labels.Set(labelsFn(meta)))
	})
}

type setFlag map[string]struct{}

func (s setFlag) String() string {
	return ""
}

func (s setFlag) Set(v string) error {
	for _, e := range strings.Split(v, ",") {
		if e = strings.TrimSpace(e); e != "" {
			(s)[e] = struct{}{}
		}
	}
	return nil
}
