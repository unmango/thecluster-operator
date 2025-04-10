package pia

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/unmango/go/option"
)

var (
	DefaultTokenLocation   = "/opt/piavpn-manual/token"
	DefaultPreferredRegion = "none"
)

type Client struct {
	log   *slog.Logger
	cache Cache

	http            *http.Client
	User, Pass      string
	Token           string
	PreferredRegion string
}

type Option func(*Client)

func NewClient(options ...Option) *Client {
	c := &Client{
		cache: NewDefaultCache(),
		log:   slog.Default(),
		http:  http.DefaultClient,

		User:  os.Getenv("PIA_USER"),
		Pass:  os.Getenv("PIA_PASS"),
		Token: os.Getenv("PIA_TOKEN"),

		PreferredRegion: os.Getenv("PREFERRED_REGION"),
	}
	option.ApplyAll(c, options)

	return c
}

func WithUsername(user string) Option {
	return func(o *Client) {
		o.User = user
	}
}

func WithPassword(pass string) Option {
	return func(o *Client) {
		o.Pass = pass
	}
}

func WithClient(client *http.Client) Option {
	return func(o *Client) {
		o.http = client
	}
}

func WithLogger(log slog.Handler) Option {
	return func(o *Client) {
		o.log = slog.New(log)
	}
}
