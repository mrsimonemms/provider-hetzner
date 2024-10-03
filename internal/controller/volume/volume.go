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

package volume

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
	errNotVolume    = "managed resource is not a Volume custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// Setup adds a controller that reconciles Volume managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.VolumeGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.VolumeGroupVersionKind),
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
		For(&v1alpha1.Volume{}).
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
	cr, ok := mg.(*v1alpha1.Volume)
	if !ok {
		return nil, errors.New(errNotVolume)
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
	cr, ok := mg.(*v1alpha1.Volume)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotVolume)
	}

	volume, _, err := c.hcloud.Client.Volume.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, err
	}
	if volume == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	switch volume.Status {
	case hcloudsdk.VolumeStatusAvailable:
		cr.SetConditions(xpv1.Available())
	case hcloudsdk.VolumeStatusCreating:
		cr.SetConditions(xpv1.Creating())
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.IsUpToDate(),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Volume)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotVolume)
	}

	var location *hcloudsdk.Location
	var server *hcloudsdk.Server

	if id := cr.Spec.ForProvider.Location; id != nil {
		l, _, err := c.hcloud.Client.Location.GetByName(ctx, *id)
		if err != nil {
			return managed.ExternalCreation{}, errors.Wrap(err, "failed to get location")
		}
		if l == nil {
			return managed.ExternalCreation{}, fmt.Errorf("unknown location")
		}
		location = l
	}

	if id := cr.Spec.ForProvider.ServerID; id != nil {
		s, _, err := c.hcloud.Client.Server.GetByID(ctx, *id)
		if err != nil {
			return managed.ExternalCreation{}, errors.Wrap(err, "failed to get server")
		}
		if s == nil {
			return managed.ExternalCreation{}, fmt.Errorf("unknown server")
		}
		server = s
	}

	volume, _, err := c.hcloud.Client.Volume.Create(ctx, hcloudsdk.VolumeCreateOpts{
		Automount: &cr.Spec.ForProvider.Automount,
		Format:    &cr.Spec.ForProvider.Format,
		Labels:    hcloud.ApplyDefaultLabels(cr.Spec.ForProvider.Labels),
		Location:  location,
		Name:      cr.ObjectMeta.Name,
		Server:    server,
		Size:      cr.Spec.ForProvider.Size,
	})
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create volume")
	}

	cr.Status.AtProvider.ID = volume.Volume.ID
	cr.Status.AtProvider.VolumeParameters = &cr.Spec.ForProvider
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to save status")
	}

	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Volume)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotVolume)
	}

	volume, _, err := c.hcloud.Client.Volume.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to get volume")
	}
	if volume == nil {
		return managed.ExternalUpdate{}, fmt.Errorf("unknown volume")
	}

	current := *cr.Status.AtProvider.VolumeParameters // What we have
	target := cr.Spec.ForProvider                     // What we want

	if _, _, err := c.hcloud.Client.Volume.Update(ctx, volume, hcloudsdk.VolumeUpdateOpts{
		Labels: hcloud.ApplyDefaultLabels(target.Labels),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to update server")
	}

	if current.ServerID != target.ServerID {
		if err := c.updateServerAttachment(ctx, volume, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if current.Size < target.Size {
		if err := c.resize(ctx, volume, target.Size); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	cr.Status.AtProvider.VolumeParameters.Labels = target.Labels
	cr.Status.AtProvider.VolumeParameters.ServerID = target.ServerID
	cr.Status.AtProvider.VolumeParameters.Size = target.Size
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to save status")
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Volume)
	if !ok {
		return errors.New(errNotVolume)
	}

	cr.SetConditions(xpv1.Deleting())

	volume := hcloudsdk.Volume{
		ID: cr.Status.AtProvider.ID,
		Server: &hcloudsdk.Server{
			ID: *cr.Status.AtProvider.VolumeParameters.ServerID,
		},
	}

	if err := c.updateServerAttachment(ctx, &volume); err != nil {
		return errors.Wrap(err, "failed to detach volumes before delete")
	}

	_, err := c.hcloud.Client.Volume.Delete(ctx, &volume)
	if err != nil {
		return errors.Wrap(err, "failed to delete volume")
	}

	return nil
}

func (c *external) resize(ctx context.Context, volume *hcloudsdk.Volume, size int) error {
	action, _, err := c.hcloud.Client.Volume.Resize(ctx, volume, size)
	if err != nil {
		return errors.Wrap(err, "failed to trigger resize volume")
	}

	if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
		return errors.Wrap(err, "failed to resize volume")
	}

	return nil
}

func (c *external) updateServerAttachment(ctx context.Context, volume *hcloudsdk.Volume, params ...v1alpha1.VolumeParameters) error {
	if volume.Server != nil {
		action, _, err := c.hcloud.Client.Volume.Detach(ctx, volume)
		if err != nil {
			return errors.Wrap(err, "failed to trigger detach volume")
		}
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "failed to detach volume")
		}
	}

	for _, p := range params {
		if p.ServerID != nil {
			action, _, err := c.hcloud.Client.Volume.AttachWithOpts(ctx, volume, hcloudsdk.VolumeAttachOpts{
				Server: &hcloudsdk.Server{
					ID: *p.ServerID,
				},
				Automount: &p.Automount,
			})
			if err != nil {
				return errors.Wrap(err, "failed to trigger attach volume")
			}
			if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
				return errors.Wrap(err, "failed to attach volume")
			}
		}
	}

	return nil
}
