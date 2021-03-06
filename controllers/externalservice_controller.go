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

package controllers

import (
	"bytes"
	"context"

	"github.com/go-logr/logr"
	egressv1 "github.com/monzo/egress-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const namespace = "egress-operator-system"

// ExternalServiceReconciler reconciles a ExternalService object
type ExternalServiceReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=egress.monzo.com,resources=externalservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=egress.monzo.com,resources=externalservices/status,verbs=get;update;patch

func (r *ExternalServiceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("externalservice", req.NamespacedName)

	es := &egressv1.ExternalService{}
	if err := r.Get(ctx, req.NamespacedName, es); err != nil {
		if apierrs.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch ExternalService")
		return ctrl.Result{}, err
	}

	req.Namespace = namespace

	desiredConfigMap, configHash, err := configmap(es)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileConfigMap(ctx, req, es, desiredConfigMap); err != nil {
		log.Error(err, "unable to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	if err := r.reconcileDeployment(ctx, req, es, configHash); err != nil {
		log.Error(err, "unable to reconcile Deployment")
		return ctrl.Result{}, err
	}

	if err := r.reconcileAutoscaler(ctx, req, es); err != nil {
		log.Error(err, "unable to reconcile HorizontalPodAutoscaler")
		return ctrl.Result{}, err
	}

	if err := r.reconcileNetworkPolicy(ctx, req, es); err != nil {
		log.Error(err, "unable to reconcile NetworkPolicy")
		return ctrl.Result{}, err
	}

	if err := r.reconcileService(ctx, req, es); err != nil {
		log.Error(err, "unable to reconcile Service")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func labels(es *egressv1.ExternalService) map[string]string {
	return map[string]string{
		"app":                      "egress-gateway",
		"egress.monzo.com/gateway": es.Name,
	}
}

func annotations(es *egressv1.ExternalService) map[string]string {
	return map[string]string{
		"egress.monzo.com/dns-name": es.Spec.DnsName,
	}
}

func labelsToSelect(es *egressv1.ExternalService) map[string]string {
	return map[string]string{
		"egress.monzo.com/gateway": es.Name,
	}
}

func (r *ExternalServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&egressv1.ExternalService{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func ignoreNotFound(err error) error {
	if apierrs.IsNotFound(err) {
		return nil
	}
	return err
}

var emptyPatch = []byte("{}")

func (r *ExternalServiceReconciler) patchIfNecessary(ctx context.Context, obj runtime.Object, patch client.Patch, opts ...client.PatchOption) error {
	data, err := patch.Data(obj)
	if err != nil {
		return err
	}

	if bytes.Equal(data, emptyPatch) {
		return nil
	}

	r.Log.WithValues("patch", string(data), "kind", obj.GetObjectKind().GroupVersionKind().String()).Info("Patching object")

	return r.Client.Patch(ctx, obj, patch, opts...)
}

func copyKey(from, to map[string]string, key string) {
	if v, ok := from[key]; ok {
		to[key] = v
	}
}
