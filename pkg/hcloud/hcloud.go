package hcloud

import (
	"context"
	"crypto/md5" //nolint:gosec
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pkg/errors"
)

// Client is used to interact with the Hetzner API
type Client struct {
	Client *hcloud.Client
}

func (c *Client) UpsertSSHKeys(ctx context.Context, publicKeys ...string) ([]*hcloud.SSHKey, error) {
	sshKeys := make([]*hcloud.SSHKey, 0)
	for _, key := range publicKeys {
		sshKey, err := c.UpsertSSHKey(ctx, key)
		if err != nil {
			return nil, err
		}

		sshKeys = append(sshKeys, sshKey)
	}

	return sshKeys, nil
}

func (c *Client) UpsertSSHKey(ctx context.Context, publicKey string) (*hcloud.SSHKey, error) {
	fingerprint, err := generateSSHKeyFingerprint(publicKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate fingerprint for public ssh key")
	}

	sshKey, _, err := c.Client.SSHKey.GetByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, err
	}

	if sshKey != nil {
		return sshKey, nil
	}

	// Upload the key
	uploadedSSHKey, _, err := c.Client.SSHKey.Create(ctx, hcloud.SSHKeyCreateOpts{
		Name:      uuid.NewString(),
		PublicKey: publicKey,
		Labels:    ApplyDefaultLabels(),
	})
	if err != nil {
		return nil, err
	}

	return uploadedSSHKey, nil
}

func (c *Client) GetDatacenterOrLocation(ctx context.Context, datacenter, location *string) (*hcloud.Datacenter, *hcloud.Location, error) {
	if datacenter != nil {
		datacenterType, _, err := c.Client.Datacenter.GetByName(ctx, *datacenter)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to get datacenter")
		}
		if datacenterType == nil {
			return nil, nil, fmt.Errorf("unknown datacenter")
		}
		return datacenterType, nil, nil

	}
	if location != nil {
		locationType, _, err := c.Client.Location.GetByName(ctx, *location)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to get location")
		}
		if locationType == nil {
			return nil, nil, fmt.Errorf("unknown location")
		}
		return nil, locationType, nil
	}

	return nil, nil, fmt.Errorf("datacenter and location not set")
}

// WaitForActionCompletion
//
// Wait until Hetzner has provisioned the resource. Useful for
// when there are async calls which are acccepted and you need
// to know when the physical resource is created.
func (c *Client) WaitForActionCompletion(ctx context.Context, action *hcloud.Action, timeout ...time.Duration) error {
	if action == nil {
		return nil
	}

	if len(timeout) == 0 {
		timeout = []time.Duration{
			time.Minute,
		}
	}

	startTime := time.Now()
	timeoutTime := startTime.Add(timeout[0])

	for {
		time.Sleep(time.Second)

		now := time.Now()

		if now.After(timeoutTime) {
			return fmt.Errorf("action timed out")
		}

		status, _, err := c.Client.Action.GetByID(ctx, action.ID)
		if err != nil {
			return err
		}

		if status.Status == hcloud.ActionStatusError {
			return fmt.Errorf("%s: %s", status.ErrorCode, status.ErrorMessage)
		}

		if status.Status == hcloud.ActionStatusSuccess {
			break
		}
	}

	return nil
}

func NewClient(token string) (*Client, error) {
	return &Client{
		Client: hcloud.NewClient(hcloud.WithToken(token)),
	}, nil
}

func generateSSHKeyFingerprint(publicKey string) (fingerprint string, err error) {
	parts := strings.Fields(publicKey)
	if len(parts) < 2 {
		err = fmt.Errorf("bad ssh key")
		return
	}

	k, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return
	}

	fp := md5.Sum([]byte(k)) //nolint:gosec,unconvert
	for i, b := range fp {
		fingerprint += fmt.Sprintf("%02x", b)
		if i < len(fp)-1 {
			fingerprint += ":"
		}
	}

	return
}
