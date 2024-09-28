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

func ApplyDefaultLabels(input map[string]string) map[string]string {
	labels := map[string]string{
		ProviderLabel:     Provider,
		GeneratedDateTime: strconv.FormatInt(time.Now().Unix(), 10),
	}

	// @todo(sje): consider doing some sanitising of keys/values https://docs.hetzner.cloud/#labels
	for k, v := range input {
		labels[k] = v
	}

	return labels
}
