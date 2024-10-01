package hcloud

import (
	"strconv"
	"time"
)

const (
	GeneratedDateTime = "crossplane.io/generated-at"
	ProviderLabel     = "crossplane.io/provider"
	Provider          = "provider-hetzner"
)

func ApplyDefaultLabels(input ...map[string]string) map[string]string {
	labels := map[string]string{
		ProviderLabel:     Provider,
		GeneratedDateTime: strconv.FormatInt(time.Now().Unix(), 10),
	}

	// @todo(sje): consider doing some sanitising of keys/values https://docs.hetzner.cloud/#labels
	for _, i := range input {
		for k, v := range i {
			labels[k] = v
		}
	}

	return labels
}
