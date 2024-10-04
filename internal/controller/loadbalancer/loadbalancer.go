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

package loadbalancer

import (
	"context"
	"fmt"
	"io"
	"reflect"

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
	errNotLoadBalancer = "managed resource is not a LoadBalancer custom resource"
	errTrackPCUsage    = "cannot track ProviderConfig usage"
	errGetPC           = "cannot get ProviderConfig"
	errGetCreds        = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// Setup adds a controller that reconciles LoadBalancer managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.LoadBalancerGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.LoadBalancerGroupVersionKind),
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
		For(&v1alpha1.LoadBalancer{}).
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
	cr, ok := mg.(*v1alpha1.LoadBalancer)
	if !ok {
		return nil, errors.New(errNotLoadBalancer)
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
	cr, ok := mg.(*v1alpha1.LoadBalancer)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotLoadBalancer)
	}

	loadBalancer, _, err := c.hcloud.Client.LoadBalancer.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, err
	}
	if loadBalancer == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.IsUpToDate(),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.LoadBalancer)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotLoadBalancer)
	}
	cr.Status.SetConditions(xpv1.Creating())

	var network *hcloudsdk.Network
	if networkId := cr.Spec.ForProvider.NetworkID; networkId != nil {
		network = &hcloudsdk.Network{
			ID: *networkId,
		}
	}

	var location *hcloudsdk.Location
	if name := cr.Spec.ForProvider.Location; name != nil {
		location = &hcloudsdk.Location{
			Name: *name,
		}
	}

	result, res, err := c.hcloud.Client.LoadBalancer.Create(ctx, hcloudsdk.LoadBalancerCreateOpts{
		Name: cr.ObjectMeta.Name,
		LoadBalancerType: &hcloudsdk.LoadBalancerType{
			Name: cr.Spec.ForProvider.Type,
		},
		Algorithm: &hcloudsdk.LoadBalancerAlgorithm{
			Type: cr.Spec.ForProvider.Algorithm,
		},
		Labels:          hcloud.ApplyDefaultLabels(cr.Spec.ForProvider.Labels),
		Location:        location,
		Network:         network,
		PublicInterface: &cr.Spec.ForProvider.PublicInterface,
		NetworkZone:     cr.Spec.ForProvider.NetworkZone,
		Services:        getServices(cr.Spec.ForProvider.Services),
		Targets:         getTargets(cr.Spec.ForProvider.Targets, network),
	})
	if err != nil {
		fmt.Printf("%+v\n", getServices(cr.Spec.ForProvider.Services)[0].HTTP)
		body, err := io.ReadAll(res.Body)
		fmt.Println(err)
		fmt.Println(string(body))
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create load balancer")
	}

	cr.Status.AtProvider.ID = result.LoadBalancer.ID
	cr.Status.AtProvider.LoadBalancerParameters = &cr.Spec.ForProvider
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to save load balancer status")
	}

	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.LoadBalancer)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotLoadBalancer)
	}

	loadBalancer, _, err := c.hcloud.Client.LoadBalancer.GetByID(ctx, cr.Status.AtProvider.ID)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to get placement group")
	}

	current := cr.Status.AtProvider.LoadBalancerParameters
	target := cr.Spec.ForProvider

	// Update the load balancer
	if _, _, err := c.hcloud.Client.LoadBalancer.Update(ctx, loadBalancer, hcloudsdk.LoadBalancerUpdateOpts{
		Labels: hcloud.ApplyDefaultLabels(target.Labels),
	}); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to perform load balancer update")
	}

	if target.Type != current.Type {
		if err := c.updateType(ctx, loadBalancer, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if target.PublicInterface != current.PublicInterface {
		if err := c.updatePublicInterface(ctx, loadBalancer, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if target.Algorithm != current.Algorithm {
		if err := c.changeAlgorithm(ctx, loadBalancer, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if target.NetworkID != current.NetworkID {
		if err := c.changeNetwork(ctx, loadBalancer, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if !reflect.DeepEqual(target.Services, current.Services) {
		if err := c.updateServices(ctx, loadBalancer, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if !reflect.DeepEqual(target.Targets, current.Targets) {
		if err := c.updateTargets(ctx, loadBalancer, target); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	cr.Status.AtProvider.LoadBalancerParameters = &target
	if err := c.kube.Status().Update(ctx, cr); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "failed to save load balancer status")
	}

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.LoadBalancer)
	if !ok {
		return errors.New(errNotLoadBalancer)
	}

	cr.SetConditions(xpv1.Deleting())

	if _, err := c.hcloud.Client.LoadBalancer.Delete(ctx, &hcloudsdk.LoadBalancer{
		ID: cr.Status.AtProvider.ID,
	}); err != nil {
		return errors.Wrap(err, "failed to delete load balancer")
	}

	return nil
}

func (c *external) changeAlgorithm(ctx context.Context, loadBalancer *hcloudsdk.LoadBalancer, target v1alpha1.LoadBalancerParameters) error {
	action, _, err := c.hcloud.Client.LoadBalancer.ChangeAlgorithm(ctx, loadBalancer, hcloudsdk.LoadBalancerChangeAlgorithmOpts{
		Type: target.Algorithm,
	})
	if err != nil {
		return errors.Wrap(err, "failed to start change algorithm")
	}
	if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
		return errors.Wrap(err, "failed to change algorithm")
	}

	return nil
}

func (c *external) changeNetwork(ctx context.Context, loadBalancer *hcloudsdk.LoadBalancer, target v1alpha1.LoadBalancerParameters) error {
	for _, n := range loadBalancer.PrivateNet {
		action, _, err := c.hcloud.Client.LoadBalancer.DetachFromNetwork(ctx, loadBalancer, hcloudsdk.LoadBalancerDetachFromNetworkOpts{
			Network: n.Network,
		})
		if err != nil {
			return errors.Wrap(err, "failed to trigger removal from network")
		}
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "failed to remove from network")
		}
	}

	if target.NetworkID == nil {
		return nil
	}

	action, _, err := c.hcloud.Client.LoadBalancer.AttachToNetwork(ctx, loadBalancer, hcloudsdk.LoadBalancerAttachToNetworkOpts{
		Network: &hcloudsdk.Network{
			ID: *target.NetworkID,
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to trigger removal from network")
	}
	if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
		return errors.Wrap(err, "failed to remove from network")
	}

	return nil
}

func (c *external) updatePublicInterface(ctx context.Context, loadBalancer *hcloudsdk.LoadBalancer, target v1alpha1.LoadBalancerParameters) error {
	var action *hcloudsdk.Action
	var err error

	if target.PublicInterface {
		action, _, err = c.hcloud.Client.LoadBalancer.EnablePublicInterface(ctx, loadBalancer)
	} else {
		action, _, err = c.hcloud.Client.LoadBalancer.DisablePublicInterface(ctx, loadBalancer)
	}
	if err != nil {
		return errors.Wrap(err, "failed to trigger public interface update")
	}
	if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
		return errors.Wrap(err, "failed to update public interface")
	}

	return nil
}

func (c *external) updateServices(ctx context.Context, loadBalancer *hcloudsdk.LoadBalancer, target v1alpha1.LoadBalancerParameters) error {
	for _, s := range loadBalancer.Services {
		action, _, err := c.hcloud.Client.LoadBalancer.DeleteService(ctx, loadBalancer, s.ListenPort)
		if err != nil {
			return errors.Wrap(err, "failed to trigger service deletion")
		}
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "failed to delete service")
		}
	}

	for _, s := range target.Services {
		healthCheck := &hcloudsdk.LoadBalancerAddServiceOptsHealthCheck{
			Protocol: s.HealthCheck.Protocol,
			Port:     s.HealthCheck.Port,
			Interval: &s.HealthCheck.Interval.Duration,
			Timeout:  &s.HealthCheck.Timeout.Duration,
			Retries:  s.HealthCheck.Retries,
		}

		if http := s.HealthCheck.HTTP; http != nil {
			healthCheck.HTTP = &hcloudsdk.LoadBalancerAddServiceOptsHealthCheckHTTP{
				Path:        http.Path,
				Domain:      http.Domain,
				Response:    http.Response,
				StatusCodes: http.StatusCodes,
				TLS:         http.TLS,
			}
		}

		action, _, err := c.hcloud.Client.LoadBalancer.AddService(ctx, loadBalancer, hcloudsdk.LoadBalancerAddServiceOpts{
			Protocol:        s.Protocol,
			ListenPort:      &s.ListenPort,
			DestinationPort: &s.DestinationPort,
			Proxyprotocol:   &s.ProxyProtocol,
			HealthCheck:     healthCheck,
		})
		if err != nil {
			return errors.Wrap(err, "failed to start add service")
		}
		if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
			return errors.Wrap(err, "failed to add service")
		}
	}

	return nil
}

func (c *external) updateTargets(ctx context.Context, loadBalancer *hcloudsdk.LoadBalancer, target v1alpha1.LoadBalancerParameters) error {
	return nil
}

func (c *external) updateType(ctx context.Context, loadBalancer *hcloudsdk.LoadBalancer, target v1alpha1.LoadBalancerParameters) error {
	action, _, err := c.hcloud.Client.LoadBalancer.ChangeType(ctx, loadBalancer, hcloudsdk.LoadBalancerChangeTypeOpts{
		LoadBalancerType: &hcloudsdk.LoadBalancerType{
			Name: target.Type,
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to start update load balancer type")
	}
	if err := c.hcloud.WaitForActionCompletion(ctx, action); err != nil {
		return errors.Wrap(err, "failed to update load balancer type")
	}

	return nil
}

func getServices(input []v1alpha1.LoadBalancerService) []hcloudsdk.LoadBalancerCreateOptsService {
	services := make([]hcloudsdk.LoadBalancerCreateOptsService, 0)
	for _, s := range input {
		var http *hcloudsdk.LoadBalancerCreateOptsServiceHTTP
		if s.HTTP != nil {
			certs := make([]*hcloudsdk.Certificate, 0)
			for _, c := range s.HTTP.CertificateIDs {
				certs = append(certs, &hcloudsdk.Certificate{
					ID: c,
				})
			}

			http = &hcloudsdk.LoadBalancerCreateOptsServiceHTTP{
				CookieName:     s.HTTP.CookieName,
				Certificates:   certs,
				CookieLifetime: &s.HTTP.CookieLifetime.Duration,
				RedirectHTTP:   s.HTTP.RedirectHTTP,
				StickySessions: s.HTTP.StickySessions,
			}
		}

		healthCheck := &hcloudsdk.LoadBalancerCreateOptsServiceHealthCheck{
			Protocol: s.HealthCheck.Protocol,
			Port:     s.HealthCheck.Port,
			Interval: &s.HealthCheck.Interval.Duration,
			Timeout:  &s.HealthCheck.Timeout.Duration,
			Retries:  s.HealthCheck.Retries,
		}

		if http := s.HealthCheck.HTTP; http != nil {
			healthCheck.HTTP = &hcloudsdk.LoadBalancerCreateOptsServiceHealthCheckHTTP{
				Path:        http.Path,
				Domain:      http.Domain,
				Response:    http.Response,
				StatusCodes: http.StatusCodes,
				TLS:         http.TLS,
			}
		}

		service := hcloudsdk.LoadBalancerCreateOptsService{
			DestinationPort: &s.DestinationPort,
			HealthCheck:     healthCheck,
			HTTP:            http,
			ListenPort:      &s.ListenPort,
			Protocol:        s.Protocol,
			Proxyprotocol:   &s.ProxyProtocol,
		}

		services = append(services, service)
	}

	return services
}

func getTargets(input []v1alpha1.LoadBalancerTarget, network *hcloudsdk.Network) []hcloudsdk.LoadBalancerCreateOptsTarget {
	targets := make([]hcloudsdk.LoadBalancerCreateOptsTarget, 0)
	for _, t := range input {
		target := hcloudsdk.LoadBalancerCreateOptsTarget{
			Type: t.Type,
		}

		if network != nil && t.UsePrivateIP {
			target.UsePrivateIP = hcloudsdk.Ptr(true)
		}

		if label := t.Labels; label != nil {
			target.LabelSelector = hcloudsdk.LoadBalancerCreateOptsTargetLabelSelector{
				Selector: hcloud.ToSelector(*t.Labels),
			}
		}

		if ip := t.IP; ip != nil {
			target.IP = hcloudsdk.LoadBalancerCreateOptsTargetIP{
				IP: *ip,
			}
		}

		if serverID := t.ServerID; serverID != nil {
			target.Server = hcloudsdk.LoadBalancerCreateOptsTargetServer{
				Server: &hcloudsdk.Server{
					ID: *serverID,
				},
			}
		}

		targets = append(targets, target)
	}

	return targets
}
