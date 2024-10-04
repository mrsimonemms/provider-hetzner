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

package placementgroup

import (
	"context"

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
	errNotPlacementGroup = "managed resource is not a PlacementGroup custom resource"
	errTrackPCUsage      = "cannot track ProviderConfig usage"
	errGetPC             = "cannot get ProviderConfig"
	errGetCreds          = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// Setup adds a controller that reconciles PlacementGroup managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.PlacementGroupGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.PlacementGroupGroupVersionKind),
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
		For(&v1alpha1.PlacementGroup{}).
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
	cr, ok := mg.(*v1alpha1.PlacementGroup)
	if !ok {
		return nil, errors.New(errNotPlacementGroup)
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
	cr, ok := mg.(*v1alpha1.PlacementGroup)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotPlacementGroup)
	}

	placementGroup, _, err := c.hcloud.Client.PlacementGroup.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, err
	}
	if placementGroup == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.IsUpToDate(),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.PlacementGroup)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotPlacementGroup)
	}

	cr.Status.SetConditions(xpv1.Creating())

	placementGroup, _, err := c.hcloud.Client.PlacementGroup.Create(ctx, hcloudsdk.PlacementGroupCreateOpts{
		Name:   cr.ObjectMeta.Name,
		Labels: hcloud.ApplyDefaultLabels(cr.Spec.ForProvider.Labels),
		Type:   cr.Spec.ForProvider.Type,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create placement group")
	}

	cr.Status.AtProvider.ID = placementGroup.PlacementGroup.ID
	cr.Status.AtProvider.PlacementGroupParameters = &cr.Spec.ForProvider
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to save status")
	}

	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.PlacementGroup)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotPlacementGroup)
	}

	placementGroup, _, err := c.hcloud.Client.PlacementGroup.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to get placement group")
	}

	target := cr.Spec.ForProvider

	// Update the network
	if _, _, err := c.hcloud.Client.PlacementGroup.Update(ctx, placementGroup, hcloudsdk.PlacementGroupUpdateOpts{
		Labels: hcloud.ApplyDefaultLabels(target.Labels),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to perform placement group update")
	}

	cr.Status.AtProvider.PlacementGroupParameters.Labels = target.Labels
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to save status")
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.PlacementGroup)
	if !ok {
		return errors.New(errNotPlacementGroup)
	}

	cr.SetConditions(xpv1.Deleting())

	_, err := c.hcloud.Client.PlacementGroup.Delete(ctx, &hcloudsdk.PlacementGroup{
		ID: cr.Status.AtProvider.ID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete placement group")
	}

	return nil
}
