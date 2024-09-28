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

package network

import (
	"context"
	"net"

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
	errNotNetwork   = "managed resource is not a Network custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient     = "cannot create new Service"
	errCreateNetwork = "cannot create new network"
	errUpdateFailed  = "cannot update network"
	errDeleteFailed  = "error deleting network"

	errIPRangeParseFailed          = "iprange cannot be parsed as a cidr"
	errSubnetIPRangeParseFailed    = "subnet.iprange cannot be parsed as a cidr"
	errRouteDestinationCannotParse = "route.destination cannot be parsed as a cidr"
	errSaveStatus                  = "failed to save status"
)

// Setup adds a controller that reconciles Network managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.NetworkGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.NetworkGroupVersionKind),
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
		For(&v1alpha1.Network{}).
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
	cr, ok := mg.(*v1alpha1.Network)
	if !ok {
		return nil, errors.New(errNotNetwork)
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
	cr, ok := mg.(*v1alpha1.Network)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotNetwork)
	}

	network, _, err := c.hcloud.Client.Network.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, err
	}
	if network == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.IsUpToDate(),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Network)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotNetwork)
	}
	cr.Status.SetConditions(xpv1.Creating())

	_, ipRange, err := net.ParseCIDR(cr.Spec.ForProvider.IPRange)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errIPRangeParseFailed)
	}

	subnets := make([]hcloudsdk.NetworkSubnet, 0)
	for _, subnet := range cr.Spec.ForProvider.Subnets {
		_, ipRange, err := net.ParseCIDR(subnet.IPRange)
		if err != nil {
			return managed.ExternalCreation{}, errors.Wrap(err, errSubnetIPRangeParseFailed)
		}

		subnets = append(subnets, hcloudsdk.NetworkSubnet{
			Type:        subnet.Type,
			IPRange:     ipRange,
			NetworkZone: subnet.NetworkZone,
			VSwitchID:   subnet.VSwitchID,
		})
	}

	routes := make([]hcloudsdk.NetworkRoute, 0)
	for _, route := range cr.Spec.ForProvider.Routes {
		_, destination, err := net.ParseCIDR(route.Destination)
		if err != nil {
			return managed.ExternalCreation{}, errors.Wrap(err, errRouteDestinationCannotParse)
		}

		routes = append(routes, hcloudsdk.NetworkRoute{
			Destination: destination,
			Gateway:     net.ParseIP(route.Gateway),
		})
	}

	network, _, err := c.hcloud.Client.Network.Create(ctx, hcloudsdk.NetworkCreateOpts{
		Name:                  cr.ObjectMeta.Name,
		IPRange:               ipRange,
		Subnets:               subnets,
		Routes:                routes,
		Labels:                hcloud.ApplyDefaultLabels(cr.Spec.ForProvider.Labels),
		ExposeRoutesToVSwitch: cr.Spec.ForProvider.ExposeRoutesToVSwitch,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateNetwork)
	}

	cr.Status.AtProvider.ID = network.ID
	cr.Status.AtProvider.NetworkParameters = &cr.Spec.ForProvider
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errSaveStatus)
	}

	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Network)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotNetwork)
	}

	network, _, err := c.hcloud.Client.Network.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to get network")
	}

	current := *cr.Status.AtProvider.NetworkParameters // What we have
	target := cr.Spec.ForProvider                      // What we want

	// Update the network
	if _, _, err := c.hcloud.Client.Network.Update(ctx, network, hcloudsdk.NetworkUpdateOpts{
		ExposeRoutesToVSwitch: &target.ExposeRoutesToVSwitch,
		Labels:                hcloud.ApplyDefaultLabels(target.Labels),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to perform network update")
	}

	// Update the IP range
	if target.IPRange != current.IPRange {
		_, ipRange, err := net.ParseCIDR(cr.Spec.ForProvider.IPRange)
		if err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errIPRangeParseFailed)
		}

		action, _, err := c.hcloud.Client.Network.ChangeIPRange(ctx, network, hcloudsdk.NetworkChangeIPRangeOpts{
			IPRange: ipRange,
		})
		if err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "failed to create change ip range action")
		}

		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "failed to change ip range")
		}
	}

	// @todo(sje): allow updating of routes/subnets
	// Until then, don't allow them to be updated on the status

	cr.Status.AtProvider.NetworkParameters = target.DeepCopy()
	cr.Status.AtProvider.NetworkParameters.Routes = current.Routes
	cr.Status.AtProvider.NetworkParameters.Subnets = current.Subnets
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errSaveStatus)
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Network)
	if !ok {
		return errors.New(errNotNetwork)
	}

	cr.SetConditions(xpv1.Deleting())

	_, err := c.hcloud.Client.Network.Delete(ctx, &hcloudsdk.Network{
		ID: cr.Status.AtProvider.ID,
	})
	if err != nil {
		return errors.Wrap(err, errDeleteFailed)
	}

	return nil
}
