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

package server

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
	errNotServer    = "managed resource is not a Server custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

func getConnectionDetails(server hcloudsdk.ServerCreateResult) managed.ConnectionDetails {
	conn := managed.ConnectionDetails{
		xpv1.ResourceCredentialsSecretEndpointKey: []byte(server.Server.PublicNet.IPv4.IP.String()),
		xpv1.ResourceCredentialsSecretUserKey:     []byte("root"),
		xpv1.ResourceCredentialsSecretPortKey:     []byte("22"),
	}
	if password := server.RootPassword; password != "" {
		conn[xpv1.ResourceCredentialsSecretPasswordKey] = []byte(password)
	}

	return conn
}

// Setup adds a controller that reconciles Server managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.ServerGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.ServerGroupVersionKind),
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
		For(&v1alpha1.Server{}).
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
	cr, ok := mg.(*v1alpha1.Server)
	if !ok {
		return nil, errors.New(errNotServer)
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
	cr, ok := mg.(*v1alpha1.Server)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotServer)
	}

	server, _, err := c.hcloud.Client.Server.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, err
	}
	if server == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	if server.Status == hcloudsdk.ServerStatusRunning || server.Status == hcloudsdk.ServerStatusOff {
		// Running or off
		cr.SetConditions(xpv1.Available())
	} else {
		cr.SetConditions(xpv1.Creating())
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.IsUpToDate(),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) { //nolint:gocyclo
	cr, ok := mg.(*v1alpha1.Server)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotServer)
	}

	// Find datacenter or location
	datacenter, location, err := c.hcloud.GetDatacenterOrLocation(ctx, cr.Spec.ForProvider.Datacenter, cr.Spec.ForProvider.Location)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to get datacenter or location")
	}

	// Find firewalls
	firewalls, err := c.getFirewalls(ctx, cr.Spec.ForProvider.FirewallIDs)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	// Find image
	image, _, err := c.hcloud.Client.Image.GetByNameAndArchitecture(ctx, cr.Spec.ForProvider.Image, cr.Spec.ForProvider.Architecture)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to get image")
	}
	if image == nil {
		return managed.ExternalCreation{}, fmt.Errorf("unknown image")
	}

	// Find networks
	networks, err := c.getNetworks(ctx, cr.Spec.ForProvider.NetworkIDs)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	// Find placement group
	var placementGroup *hcloudsdk.PlacementGroup
	if placementGroupId := cr.Spec.ForProvider.PlacementGroupID; placementGroupId != nil {
		group, _, err := c.hcloud.Client.PlacementGroup.GetByID(ctx, *placementGroupId)
		if err != nil {
			return managed.ExternalCreation{}, errors.Wrap(err, "failed to query placement group")
		}
		if group == nil {
			return managed.ExternalCreation{}, fmt.Errorf("no placement group found")
		}
		placementGroup = group
	}

	// Find serverType
	serverType, _, err := c.hcloud.Client.ServerType.GetByName(ctx, cr.Spec.ForProvider.ServerType)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to get server type")
	}
	if serverType == nil {
		return managed.ExternalCreation{}, fmt.Errorf("unknown server type")
	}

	//  Find volumes
	volumes, err := c.getVolumes(ctx, cr.Spec.ForProvider.VolumeIDs)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	// Ensure SSH keys
	sshKeys, err := c.hcloud.UpsertSSHKeys(ctx, cr.Spec.ForProvider.SSHKeys...)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to upsert ssh key")
	}

	cr.Status.SetConditions(xpv1.Creating())

	server, _, err := c.hcloud.Client.Server.Create(ctx, hcloudsdk.ServerCreateOpts{
		Automount:      &cr.Spec.ForProvider.AutoMount,
		Name:           cr.ObjectMeta.Name,
		Datacenter:     datacenter,
		Firewalls:      firewalls,
		Image:          image,
		Labels:         hcloud.ApplyDefaultLabels(cr.Spec.ForProvider.Labels),
		Location:       location,
		Networks:       networks,
		PlacementGroup: placementGroup,
		PublicNet: &hcloudsdk.ServerCreatePublicNet{
			EnableIPv4: cr.Spec.ForProvider.EnableIPv4,
			EnableIPv6: cr.Spec.ForProvider.EnableIPv6,
		},
		ServerType:       serverType,
		SSHKeys:          sshKeys,
		StartAfterCreate: &cr.Spec.ForProvider.StartAfterCreate,
		UserData:         cr.Spec.ForProvider.UserData,
		Volumes:          volumes,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create server")
	}

	cr.Status.AtProvider.ID = server.Server.ID
	cr.Status.AtProvider.ServerParameters = &cr.Spec.ForProvider
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to save create status")
	}

	return managed.ExternalCreation{
		ConnectionDetails: getConnectionDetails(server),
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Server)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotServer)
	}

	server, _, err := c.hcloud.Client.Server.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to get server")
	}

	current := *cr.Status.AtProvider.ServerParameters // What we have
	target := cr.Spec.ForProvider                     // What we want

	if _, _, err := c.hcloud.Client.Server.Update(ctx, server, hcloudsdk.ServerUpdateOpts{
		Labels: hcloud.ApplyDefaultLabels(target.Labels),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to update server")
	}

	if current.PowerOn != target.PowerOn {
		var action *hcloudsdk.Action
		var err error
		if target.PowerOn {
			action, _, err = c.hcloud.Client.Server.Poweron(ctx, server)
		} else {
			action, _, err = c.hcloud.Client.Server.Poweroff(ctx, server)
		}
		if err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "failed to change power state")
		}

		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "error waiting for server to change power state")
		}
	}

	cr.Status.AtProvider.ServerParameters.Labels = target.Labels
	cr.Status.AtProvider.ServerParameters.PowerOn = target.PowerOn
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to save status")
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Server)
	if !ok {
		return errors.New(errNotServer)
	}

	cr.SetConditions(xpv1.Deleting())

	_, _, err := c.hcloud.Client.Server.DeleteWithResult(ctx, &hcloudsdk.Server{
		ID: cr.Status.AtProvider.ID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to trigger server delete")
	}

	return nil
}

func (c *external) getFirewalls(ctx context.Context, firewallIds []int64) ([]*hcloudsdk.ServerCreateFirewall, error) {
	firewalls := []*hcloudsdk.ServerCreateFirewall{}
	for _, firewall := range firewallIds {
		f, _, err := c.hcloud.Client.Firewall.GetByID(ctx, firewall)
		if err != nil {
			return nil, errors.Wrap(err, "error getting firewall")
		}
		if f == nil {
			return nil, fmt.Errorf("unknown firewall")
		}
		firewalls = append(firewalls, &hcloudsdk.ServerCreateFirewall{
			Firewall: *f,
		})
	}

	return firewalls, nil
}

func (c *external) getNetworks(ctx context.Context, networkIDs []int64) ([]*hcloudsdk.Network, error) {
	networks := []*hcloudsdk.Network{}
	for _, network := range networkIDs {
		n, _, err := c.hcloud.Client.Network.GetByID(ctx, network)
		if err != nil {
			return nil, errors.Wrap(err, "error getting network")
		}
		if n == nil {
			return nil, fmt.Errorf("unknown network")
		}
		networks = append(networks, n)
	}

	return networks, nil
}

func (c *external) getVolumes(ctx context.Context, volumeIds []int64) ([]*hcloudsdk.Volume, error) {
	volumes := []*hcloudsdk.Volume{}
	for _, volume := range volumeIds {
		v, _, err := c.hcloud.Client.Volume.GetByID(ctx, volume)
		if err != nil {
			return nil, errors.Wrap(err, "error getting volume")
		}
		if v == nil {
			return nil, fmt.Errorf("unknown volume")
		}
		volumes = append(volumes, v)
	}

	return volumes, nil
}
