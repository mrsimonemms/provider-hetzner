package hcloud_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mrsimonemms/provider-hetzner/pkg/hcloud"
)

type ExampleJSON struct {
	Name     string           `json:"name"`
	Duration *hcloud.Duration `json:"duration"`
}

func TestDurationMarshalJSON(t *testing.T) {
	tests := []struct {
		Name   string
		Input  ExampleJSON
		Output string
		Err    error
	}{
		{
			Name: "simple",
			Input: ExampleJSON{
				Name: "simple",
				Duration: &hcloud.Duration{
					Duration: time.Minute,
				},
			},
			Output: `{"name":"simple","duration":"1m0s"}`,
			Err:    nil,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert := assert.New(t)

			b, err := json.Marshal(test.Input)

			assert.Equal(test.Err, err)
			assert.Equal(test.Output, string(b))
		})
	}
}

func TestDurationUnmarshalJSON(t *testing.T) {
	tests := []struct {
		Name   string
		Input  string
		Output ExampleJSON
		Err    error
	}{
		{
			Name:  "simple",
			Input: `{"name":"simple","duration":"1m"}`,
			Output: ExampleJSON{
				Name: "simple",
				Duration: &hcloud.Duration{
					Duration: time.Minute,
				},
			},
			Err: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert := assert.New(t)

			var s ExampleJSON
			err := json.Unmarshal([]byte(test.Input), &s)

			assert.Equal(test.Err, err)
			assert.Equal(test.Output, s)
		})
	}
}
