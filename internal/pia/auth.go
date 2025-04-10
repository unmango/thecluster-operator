package pia

import (
	"context"
	"fmt"
	"time"

	"resty.dev/v3"
)

var tokenKey = "PIA_TOKEN"

func AuthMiddleware(opts *Client) resty.RequestMiddleware {
	return opts.AuthHandler
}

type TokenResponse struct {
	Token string `json:"token"`
}

func (c *Client) GetToken(ctx context.Context) (*TokenResponse, error) {
	var result TokenResponse
	rest := resty.NewWithClient(c.http).SetContext(ctx)
	defer rest.Close()

	res, err := rest.R().
		SetFormData(map[string]string{
			"username": c.User,
			"password": c.Pass,
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

func (client *Client) AuthHandler(c *resty.Client, r *resty.Request) error {
	tok, found := client.tryGetToken()
	if !found {
		if res, err := client.GetToken(r.Context()); err != nil {
			return err
		} else {
			tok = res.Token
			client.cache.Set(tokenKey, tok, 24*time.Hour)
		}
	}

	r.SetAuthToken(tok)
	return nil
}

func (c *Client) tryGetToken() (string, bool) {
	if c.Token != "" {
		return c.Token, true
	}
	if tok, ok := c.cache.Get(tokenKey); ok {
		return tok.(string), true
	} else {
		return "", false
	}
}
