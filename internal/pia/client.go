package pia

import (
	"fmt"

	"github.com/unmango/go/option"
	"resty.dev/v3"
)

type Client struct {
	*Options
}

func NewClient(options ...Option) *Client {
	opts := NewDefaultOptions()
	option.ApplyAll(opts, options)

	return &Client{Options: NewDefaultOptions()}
}

func (c *Client) allRegionData() error {
	return fmt.Errorf("TODO")
}

func AuthMiddleware(opts *Options) resty.RequestMiddleware {
	return opts.AuthHandler
}
