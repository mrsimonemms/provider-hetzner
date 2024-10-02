package hcloud

import (
	"fmt"
	"strconv"
	"strings"
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

func ToSelector(l map[string]string) string {
	labels := make([]string, 0)

	for k, v := range escapeLabels(l) {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}

	return strings.Join(labels, ",")
}

func escapeLabels(labels map[string]string) map[string]string {
	escaped := make(map[string]string, len(labels))

	for k, v := range labels {
		escaped[k] = v
	}

	return escaped
}
