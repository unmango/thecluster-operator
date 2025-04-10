package pia

import (
	"context"
	"fmt"

	"resty.dev/v3"
)

func AuthMiddleware(opts *Options) resty.RequestMiddleware {
	return opts.AuthHandler
}

type TokenResponse struct {
	Token string `json:"token"`
}

func (opts *Options) GetToken(client *resty.Client) (*TokenResponse, error) {
	var result TokenResponse
	res, err := client.R().
		SetFormData(map[string]string{
			"username": opts.User,
			"password": opts.Pass,
		}).
		SetResult(&result).
		Post("https://www.privateinternetaccess.com/api/client/v2/token")
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("token request failed: %s", res.Status())
	}

	return &result, nil
}

func (c *Client) GetToken(ctx context.Context) (string, error) {
	rest := resty.NewWithClient(c.opts.http).SetContext(ctx)
	defer rest.Close()

	if res, err := c.opts.GetToken(rest); err != nil {
		return "", err
	} else {
		return res.Token, nil
	}
}

func (opts *Options) AuthHandler(c *resty.Client, r *resty.Request) error {
	if opts.Token != "" {
		r.SetAuthToken(opts.Token)
		return nil
	}
	if res, err := opts.GetToken(c); err != nil {
		return err
	} else {
		r.SetAuthToken(res.Token)
	}

	return nil
}
