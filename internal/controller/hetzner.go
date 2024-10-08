/*
Copyright 2020 The Crossplane Authors.

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

package controller

import (
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/mrsimonemms/provider-hetzner/internal/controller/config"
	"github.com/mrsimonemms/provider-hetzner/internal/controller/firewall"
	"github.com/mrsimonemms/provider-hetzner/internal/controller/network"
	"github.com/mrsimonemms/provider-hetzner/internal/controller/placementgroup"
	"github.com/mrsimonemms/provider-hetzner/internal/controller/server"
	"github.com/mrsimonemms/provider-hetzner/internal/controller/volume"
)

// Setup creates all Hetzner controllers with the supplied logger and adds them to
// the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		config.Setup,
		firewall.Setup,
		network.Setup,
		placementgroup.Setup,
		server.Setup,
		volume.Setup,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}
