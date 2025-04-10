package pia

import (
	"log/slog"
	"net/http"
	"os"
)

var (
	DefaultTokenLocation   = "/opt/piavpn-manual/token"
	DefaultPreferredRegion = "none"
)

type Options struct {
	log *slog.Logger

	http            *http.Client
	User, Pass      string
	Token           string
	PreferredRegion string
}

type Option func(*Options)

func NewDefaultOptions() *Options {
	return &Options{
		log:   slog.Default(),
		http:  http.DefaultClient,
		User:  os.Getenv("PIA_USER"),
		Pass:  os.Getenv("PIA_PASS"),
		Token: os.Getenv("PIA_TOKEN"),

		PreferredRegion: os.Getenv("PREFERRED_REGION"),
	}
}

func WithUsername(user string) Option {
	return func(o *Options) {
		o.User = user
	}
}

func WithPassword(pass string) Option {
	return func(o *Options) {
		o.Pass = pass
	}
}

func WithClient(client *http.Client) Option {
	return func(o *Options) {
		o.http = client
	}
}

func WithLogger(log slog.Handler) Option {
	return func(o *Options) {
		o.log = slog.New(log)
	}
}
