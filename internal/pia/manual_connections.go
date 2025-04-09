package pia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"os"
	"time"

	"github.com/unmango/go/option"
	"resty.dev/v3"
)

var (
	DefaultTokenLocation   = "/opt/piavpn-manual/token"
	DefaultPreferredRegion = "none"
)

type Options struct {
	log *slog.Logger

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

func (opts *Options) client() *resty.Client {
	return resty.NewWithClient(opts.Client)
}

func (opts *Options) tokenRequest(client *resty.Client) *resty.Request {
	return client.R().
		SetURL("https://www.privateinternetaccess.com/api/client/v2/token").
		SetFormData(map[string]string{
			"username": opts.User,
			"password": opts.Pass,
		})
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

func (opts *Options) allRegionData(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://serverlist.piaservers.net/vpninfo/servers/v6", nil,
	)
	if err != nil {
		return nil, err
	}

	return opts.Client.Do(req)
}

func serverLatency(ctx context.Context, serverIp string) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://%s:443", serverIp), nil,
	)
	if err != nil {
		return 0, err
	}

	return timeRequest(req)
}

type Latency struct {
	Time       time.Duration
	ServerIp   string
	RegionName string
}

func GetRegion(ctx context.Context, options ...Option) (string, error) {
	opts := NewDefaultOptions()
	option.ApplyAll(opts, options)

	resp, err := opts.allRegionData(ctx)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("region data request failed: %s", resp.Status)
	}

	var regionResp RegionResponse
	if err = json.NewDecoder(resp.Body).Decode(&regionResp); err != nil {
		return "", err
	}

	var latencies []Latency
	for _, r := range regionResp.Regions {
		if len(r.Servers.Meta) == 0 {
			continue
		}

		ip := r.Servers.Meta[0].Ip
		time, err := serverLatency(ctx, ip)
		if err != nil { // Log and ignore?
			return "", err
		}

		latencies = append(latencies, Latency{
			Time:       time,
			ServerIp:   ip,
			RegionName: r.Name,
		})
	}

	return "", nil
}

func timeRequest(req *http.Request) (time.Duration, error) {
	var start time.Time
	var connTime time.Duration
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			connTime = time.Since(start)
		},
	}

	req = req.WithContext(
		httptrace.WithClientTrace(req.Context(), trace),
	)
	start = time.Now()
	if _, err := http.DefaultTransport.RoundTrip(req); err != nil {
		return 0, err
	}

	return connTime, nil
}
