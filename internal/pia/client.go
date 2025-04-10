package pia

import (
	"context"
	"fmt"
	"time"

	"resty.dev/v3"
)

func (c *Client) client(ctx context.Context) *resty.Client {
	return resty.NewWithClient(c.http).
		AddRequestMiddleware(c.AuthHandler).
		SetContext(ctx)
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

func (c *Client) GetDIP(ctx context.Context, request *DipRequest) ([]DipResponse, error) {
	var result []DipResponse
	rest := c.client(ctx)
	defer rest.Close()

	res, err := rest.R().
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

type Server struct {
	Ip  string `json:"ip"`
	Cn  string `json:"cn"`
	Van bool   `json:"van,omitempty"`
}

type Region struct {
	Id          string              `json:"id"`
	Name        string              `json:"name"`
	Country     string              `json:"country"`
	AutoRegion  bool                `json:"auto_region"`
	Dns         string              `json:"dns"`
	PortForward bool                `json:"port_forward"`
	Geo         bool                `json:"geo"`
	Offline     bool                `json:"offline"`
	Servers     map[string][]Server `json:"servers"`
}

type Group struct {
	Name  string `json:"name"`
	Ports []int  `json:"ports"`
}

type ServersResponse struct {
	Groups  map[string][]Group `json:"groups"`
	Regions []Region           `json:"regions"`
}

func (c *Client) Servers(ctx context.Context) (*ServersResponse, error) {
	var result ServersResponse
	rest := resty.NewWithClient(c.http).SetContext(ctx)
	defer rest.Close()

	res, err := rest.R().
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

func (c *Client) ConnTime(ctx context.Context, ip string) (time.Duration, error) {
	rest := resty.New().SetContext(ctx)
	defer rest.Close()

	res, err := rest.R().
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
