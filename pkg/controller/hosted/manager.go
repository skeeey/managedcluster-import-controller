// Copyright (c) Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package hosted

import (
	"strings"

	"github.com/stolostron/managedcluster-import-controller/pkg/constants"
	"github.com/stolostron/managedcluster-import-controller/pkg/helpers"
	"github.com/stolostron/managedcluster-import-controller/pkg/source"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	workv1 "open-cluster-management.io/api/work/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	runtimesource "sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "hosted-manifestwork-controller"

// Add creates a new manifestwork controller and adds it to the Manager.
// The Manager will set fields on the Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager, clientHolder *helpers.ClientHolder, informerHolder *source.InformerHolder) (string, error) {
	c, err := controller.New(controllerName, mgr, controller.Options{
		Reconciler: &ReconcileHosted{
			clientHolder:   clientHolder,
			informerHolder: informerHolder,
			scheme:         mgr.GetScheme(),
			recorder:       helpers.NewEventRecorder(clientHolder.KubeClient, controllerName),
		},
		MaxConcurrentReconciles: helpers.GetMaxConcurrentReconciles(),
	})
	if err != nil {
		return controllerName, err
	}

	if err := c.Watch(
		source.NewHostedWorkSource(informerHolder.HostedWorkInformer),
		&source.ManagedClusterResourceEventHandler{
			MapFunc: func(o client.Object) reconcile.Request {
				managedClusterName := o.GetNamespace()
				workName := o.GetName()
				if strings.HasSuffix(workName, constants.HostedKlusterletManifestworkSuffix) {
					managedClusterName = strings.TrimSuffix(workName, "-"+constants.HostedKlusterletManifestworkSuffix)
				}
				if strings.HasSuffix(workName, constants.HostedManagedKubeconfigManifestworkSuffix) {
					managedClusterName = strings.TrimSuffix(workName, "-"+constants.HostedManagedKubeconfigManifestworkSuffix)
				}
				return reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: managedClusterName,
						Name:      managedClusterName,
					},
				}
			},
		},
		predicate.Predicate(predicate.Funcs{
			GenericFunc: func(e event.GenericEvent) bool { return false },
			CreateFunc:  func(e event.CreateEvent) bool { return true },
			DeleteFunc:  func(e event.DeleteEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				workName := e.ObjectNew.GetName()
				// for update event, only watch hosted mode manifest works
				if !strings.HasSuffix(workName, constants.HostedKlusterletManifestworkSuffix) ||
					!strings.HasSuffix(workName, constants.HostedManagedKubeconfigManifestworkSuffix) {
					return false
				}

				new, okNew := e.ObjectNew.(*workv1.ManifestWork)
				old, okOld := e.ObjectOld.(*workv1.ManifestWork)
				if okNew && okOld {
					return !helpers.ManifestsEqual(new.Spec.Workload.Manifests, old.Spec.Workload.Manifests)
				}

				return false
			},
		}),
	); err != nil {
		return controllerName, err
	}

	if err := c.Watch(
		&runtimesource.Kind{Type: &clusterv1.ManagedCluster{}},
		&handler.EnqueueRequestForObject{},
		predicate.Predicate(predicate.Funcs{
			GenericFunc: func(e event.GenericEvent) bool { return isHostedModeObject(e.Object) },
			DeleteFunc:  func(e event.DeleteEvent) bool { return isHostedModeObject(e.Object) },
			CreateFunc:  func(e event.CreateEvent) bool { return isHostedModeObject(e.Object) },
			UpdateFunc:  func(e event.UpdateEvent) bool { return isHostedModeObject(e.ObjectNew) },
		})); err != nil {
		return controllerName, err
	}

	if err := c.Watch(
		source.NewImportSecretSource(informerHolder.ImportSecretInformer),
		&source.ManagedClusterResourceEventHandler{},
		predicate.Predicate(predicate.Funcs{
			GenericFunc: func(e event.GenericEvent) bool { return false },
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			CreateFunc: func(e event.CreateEvent) bool {
				// only handle the hosted mode import secret
				return isHostedModeObject(e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				new, okNew := e.ObjectNew.(*corev1.Secret)
				old, okOld := e.ObjectOld.(*corev1.Secret)
				if okNew && okOld {
					return !equality.Semantic.DeepEqual(old.Data, new.Data)
				}

				return false
			},
		}),
	); err != nil {
		return controllerName, err
	}

	// watch the auto-import secrets
	if err := c.Watch(
		source.NewAutoImportSecretSource(informerHolder.AutoImportSecretInformer),
		&source.ManagedClusterResourceEventHandler{},
		predicate.Predicate(predicate.Funcs{
			GenericFunc: func(e event.GenericEvent) bool { return false },
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			CreateFunc:  func(e event.CreateEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				new, okNew := e.ObjectNew.(*corev1.Secret)
				old, okOld := e.ObjectOld.(*corev1.Secret)
				if okNew && okOld {
					return !equality.Semantic.DeepEqual(old.Data, new.Data)
				}
				return false
			},
		}),
	); err != nil {
		return controllerName, err
	}

	return controllerName, nil
}

func isHostedModeObject(object client.Object) bool {
	return strings.EqualFold(object.GetAnnotations()[constants.KlusterletDeployModeAnnotation], constants.KlusterletDeployModeHosted)
}
