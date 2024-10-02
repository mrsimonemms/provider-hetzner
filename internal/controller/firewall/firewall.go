/*
Copyright 2022 The Crossplane Authors.

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

package firewall

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	hcloudsdk "github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/mrsimonemms/provider-hetzner/apis/cloud/v1alpha1"
	apisv1alpha1 "github.com/mrsimonemms/provider-hetzner/apis/v1alpha1"
	"github.com/mrsimonemms/provider-hetzner/internal/features"
	"github.com/mrsimonemms/provider-hetzner/pkg/hcloud"
)

const (
	errNotFirewall  = "managed resource is not a Firewall custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// Setup adds a controller that reconciles Firewall managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.FirewallGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.FirewallGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: hcloud.NewClient,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Firewall{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(creds string) (*hcloud.Client, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Firewall)
	if !ok {
		return nil, errors.New(errNotFirewall)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	svc, err := c.newServiceFn(string(data))
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &external{
		kube:   c.kube,
		hcloud: svc,
	}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	kube   client.Client
	hcloud *hcloud.Client
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Firewall)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotFirewall)
	}

	firewall, _, err := c.hcloud.Client.Firewall.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, err
	}
	if firewall == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.IsUpToDate(),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Firewall)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotFirewall)
	}

	cr.Status.SetConditions(xpv1.Creating())

	rules, err := getFirewallRules(cr.Spec.ForProvider.Rules)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to convert firewall rules")
	}

	applyTo := make([]hcloudsdk.FirewallResource, 0)
	for _, a := range cr.Spec.ForProvider.ApplyTo {
		applyTo = append(applyTo, a.ToFirewallResource())
	}

	firewall, _, err := c.hcloud.Client.Firewall.Create(ctx, hcloudsdk.FirewallCreateOpts{
		Name:    cr.ObjectMeta.Name,
		ApplyTo: applyTo,
		Labels:  hcloud.ApplyDefaultLabels(cr.Spec.ForProvider.Labels),
		Rules:   rules,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create firewall")
	}

	cr.Status.AtProvider.ID = firewall.Firewall.ID
	cr.Status.AtProvider.FirewallParameters = &cr.Spec.ForProvider
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "error saving status")
	}

	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Firewall)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotFirewall)
	}

	target := cr.Spec.ForProvider // What we want

	rules, err := getFirewallRules(target.Rules)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to convert firewall rules")
	}

	firewall, _, err := c.hcloud.Client.Firewall.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to find firewall")
	}
	if firewall == nil {
		return managed.ExternalUpdate{}, fmt.Errorf("firewall not found")
	}

	if _, _, err := c.hcloud.Client.Firewall.Update(ctx, firewall, hcloudsdk.FirewallUpdateOpts{
		Labels: hcloud.ApplyDefaultLabels(target.Labels),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to update firewall")
	}

	if err := c.removeResources(ctx, firewall, firewall.AppliedTo); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to remove resources")
	}

	if err := c.applyResources(ctx, firewall, target.ApplyTo); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to apply resources")
	}

	if err := c.setRules(ctx, firewall, rules); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to set rules")
	}

	cr.Status.AtProvider.FirewallParameters = target.DeepCopy()
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to update status")
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Firewall)
	if !ok {
		return errors.New(errNotFirewall)
	}

	cr.SetConditions(xpv1.Deleting())

	firewall, _, err := c.hcloud.Client.Firewall.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return errors.Wrap(err, "failed to get firewall to delete")
	}
	if firewall == nil {
		return fmt.Errorf("no firewal to delete")
	}

	if err := c.removeResources(ctx, firewall, firewall.AppliedTo); err != nil {
		return errors.Wrap(err, "failed to remove resources")
	}

	if _, err := c.hcloud.Client.Firewall.Delete(ctx, firewall); err != nil {
		return errors.Wrap(err, "failed to delete firewall")
	}

	return nil
}

func (c *external) applyResources(ctx context.Context, firewall *hcloudsdk.Firewall, resources []v1alpha1.FirewallApplyTo) error {
	applyTo := make([]hcloudsdk.FirewallResource, 0)
	for _, a := range resources {
		applyTo = append(applyTo, a.ToFirewallResource())
	}

	applyActions, _, err := c.hcloud.Client.Firewall.ApplyResources(ctx, firewall, applyTo)
	if err != nil {
		return errors.Wrap(err, "failed to apply resources")
	}

	for _, action := range applyActions {
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "error completing action")
		}
	}

	return nil
}

func (c *external) removeResources(ctx context.Context, firewall *hcloudsdk.Firewall, resources []hcloudsdk.FirewallResource) error {
	removeActions, _, err := c.hcloud.Client.Firewall.RemoveResources(ctx, firewall, resources)
	if err != nil {
		return err
	}

	for _, action := range removeActions {
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "error completing action")
		}
	}

	return nil
}

func (c *external) setRules(ctx context.Context, firewall *hcloudsdk.Firewall, rules []hcloudsdk.FirewallRule) error {
	setActions, _, err := c.hcloud.Client.Firewall.SetRules(ctx, firewall, hcloudsdk.FirewallSetRulesOpts{
		Rules: rules,
	})
	if err != nil {
		return errors.Wrap(err, "failed to set rules")
	}

	for _, action := range setActions {
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "error completing action")
		}
	}

	return nil
}

func getFirewallRules(input []v1alpha1.FirewallRules) ([]hcloudsdk.FirewallRule, error) {
	rules := make([]hcloudsdk.FirewallRule, 0)
	for _, rule := range input {
		r, err := rule.ToFirewallRule()
		if err != nil {
			return nil, err
		}

		rules = append(rules, *r)
	}

	return rules, nil
}
