package pia

import (
	"fmt"
	"time"

	"github.com/unmango/go/option"
	"resty.dev/v3"
)

type Client struct {
	opts *Options
}

func NewClient(options ...Option) *Client {
	opts := NewDefaultOptions()
	option.ApplyAll(opts, options)

	return &Client{opts: opts}
}

func (opts *Options) client() *resty.Client {
	c := resty.NewWithClient(opts.http)
	c.AddRequestMiddleware(opts.AuthHandler)
	return c
}

type DipRequest struct {
	Tokens []string
}

type DipResponse struct {
	Status    string
	Ip        string
	Cn        string
	DipExpire string
	Id        string
}

func (opts *Options) GetDIP(client *resty.Client, request *DipRequest) ([]DipResponse, error) {
	var result []DipResponse
	res, err := client.R().
		SetContentType("application/json").
		SetBody(request).
		SetResult(&result).
		Post("https://www.privateinternetaccess.com/api/client/v2/dedicated_ip")
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("dedicated ip request failed: %s", res.Status())
	}

	return result, nil
}

type Meta struct {
	Ip string
}

type Servers struct {
	Meta []Meta
}

type Region struct {
	Id          string
	Name        string
	PortForward bool
	Servers     Servers
	Geo         any // TODO
}

type RegionResponse struct {
	Regions []Region
}

func (opts *Options) allRegionData(client *resty.Client) (*RegionResponse, error) {
	var result RegionResponse
	res, err := client.R().
		SetResult(&result).
		Get("https://serverlist.piaservers.net/vpninfo/servers/v6")
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("servers request failed: %s", res.Status())
	}

	return &result, err
}

func (opts *Options) ConnTime(client *resty.Client, ip string) (time.Duration, error) {
	res, err := client.R().
		EnableTrace().
		Get(fmt.Sprintf("http://%s:443", ip))
	if err != nil {
		return 0, err
	}
	if res.IsError() {
		return 0, fmt.Errorf("latency request failed: %s", res.Status())
	}

	trace := res.Request.TraceInfo()
	return trace.ConnTime, nil
}
