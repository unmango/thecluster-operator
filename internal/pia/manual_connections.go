package pia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/unmango/go/option"
)

var (
	DefaultTokenLocation   = "/opt/piavpn-manual/token"
	DefaultPreferredRegion = "none"
)

type Options struct {
	Client          *http.Client
	User, Pass      string
	Token           string
	PreferredRegion string
}

type Option func(*Options)

func NewDefaultOptions() *Options {
	return &Options{
		Client: http.DefaultClient,
		User:   os.Getenv("PIA_USER"),
		Pass:   os.Getenv("PIA_PASS"),
		Token:  os.Getenv("PIA_TOKEN"),

		PreferredRegion: os.Getenv("PREFERRED_REGION"),
	}
}

func WithUser(user string) Option {
	return func(o *Options) {
		o.User = user
	}
}

func WithPass(pass string) Option {
	return func(o *Options) {
		o.Pass = pass
	}
}

func WithClient(client *http.Client) Option {
	return func(o *Options) {
		o.Client = client
	}
}

func (opts *Options) generateTokenResponse(ctx context.Context) (*http.Response, error) {
	form := url.Values{
		"username": []string{opts.User},
		"password": []string{opts.Pass},
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://www.privateinternetaccess.com/api/client/v2/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return opts.Client.Do(req)
}

func GetToken(ctx context.Context, options ...Option) (string, error) {
	opts := NewDefaultOptions()
	option.ApplyAll(opts, options)

	resp, err := opts.generateTokenResponse(ctx)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed: %s", resp.Status)
	}

	var content struct{ Token string }
	if err = json.NewDecoder(resp.Body).Decode(&content); err != nil {
		return "", err
	}

	return content.Token, nil
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

func (opts *Options) generateDIPResponse(ctx context.Context, dipToken string) (*http.Response, error) {
	json, err := json.Marshal(DipRequest{Tokens: []string{dipToken}})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://www.privateinternetaccess.com/api/client/v2/dedicated_ip",
		bytes.NewReader(json),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", opts.Token))
	return opts.Client.Do(req)
}

func GetDip(ctx context.Context, dipToken string, options ...Option) (string, error) {
	opts := NewDefaultOptions()
	option.ApplyAll(opts, options)

	resp, err := opts.generateDIPResponse(ctx, dipToken)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dedicated ip request failed: %s", resp.Status)
	}

	var dipResp []DipResponse
	if err = json.NewDecoder(resp.Body).Decode(&dipResp); err != nil {
		return "", err
	}
	if len(dipResp) < 1 {
		return "", fmt.Errorf("no ips in response")
	}

	// TODO: https://github.com/pia-foss/manual-connections/blob/e956c57849a38f912e654e0357f5ae456dfd1742/get_dip.sh#L100
	return dipResp[0].Ip, nil
}

type Meta struct {
	Ip string
}

type Server struct {
	Meta []Meta
}

type Region struct {
	Id          string
	Name        string
	PortForward bool
	Servers     []Server
	Geo         any // TODO
}

type RegionResponse struct {
	Regions []Region
}

func (opts *Options) allRegionData(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://serverlist.piaservers.net/vpninfo/servers/v6", nil,
	)
	if err != nil {
		return nil, err
	}

	return opts.Client.Do(req)
}

func GetRegion(ctx context.Context) (string, error) {
	return "", nil
}

func timeRequest(req *http.Request) (time.Duration, error) {
	trace := &httptrace.ClientTrace{}
	start := time.Now()
	return time.Since(start), nil
}
