package pia

import (
	"time"

	"github.com/patrickmn/go-cache"
)

type Cache interface {
	Get(key string) (value any, found bool)
	Set(key string, value any, expires time.Duration)
}

func NewDefaultCache() Cache {
	return cache.New(5*time.Minute, 10*time.Minute)
}
