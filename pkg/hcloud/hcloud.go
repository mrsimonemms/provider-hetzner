package hcloud

import (
	"context"
	"fmt"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Client is used to interact with the Hetzner API
type Client struct {
	Client *hcloud.Client
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
