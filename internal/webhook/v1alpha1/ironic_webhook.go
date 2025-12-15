/*
Copyright 2023.

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

package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	metal3api "github.com/metal3-io/ironic-standalone-operator/api/v1alpha1"
	validation "github.com/metal3-io/ironic-standalone-operator/pkg/ironic"
)

// log is for logging in this package.
var ironiclog = logf.Log.WithName("webhooks").WithName("Ironic")

func SetupIronicWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&metal3api.Ironic{}).
		WithValidator(&IronicCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ironic-metal3-io-v1alpha1-ironic,mutating=false,failurePolicy=fail,sideEffects=None,groups=ironic.metal3.io,resources=ironics,verbs=create;update,versions=v1alpha1,name=validate-ironic.ironic.metal3.io,admissionReviewVersions=v1

type IronicCustomValidator struct{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (r *IronicCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	ironic, ok := obj.(*metal3api.Ironic)
	if !ok {
		return nil, fmt.Errorf("expected an Ironic, got %T", obj)
	}

	ironiclog.Info("validate create", "name", ironic.Name)
	return nil, validation.ValidateIronic(&ironic.Spec, nil)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (r *IronicCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	ironic, ok := newObj.(*metal3api.Ironic)
	if !ok {
		return nil, fmt.Errorf("expected an Ironic for newObj, got %T", newObj)
	}

	oldIronic, ok := oldObj.(*metal3api.Ironic)
	if !ok {
		return nil, fmt.Errorf("expected an Ironic for oldObj, got %T", oldObj)
	}

	ironiclog.Info("validate update", "name", ironic.Name)
	return nil, validation.ValidateIronic(&ironic.Spec, &oldIronic.Spec)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (r *IronicCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
