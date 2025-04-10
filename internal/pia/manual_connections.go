package pia

import (
	"context"
	"fmt"
	"time"
)

func GetDip(ctx context.Context, dipToken string, options ...Option) (string, error) {
	c := NewClient(options...)

	resp, err := c.GetDIP(ctx, &DipRequest{
		Tokens: []string{dipToken},
	})
	if err != nil {
		return "", err
	}
	if len(resp) < 1 {
		return "", fmt.Errorf("no ips in response")
	}

	// TODO: https://github.com/pia-foss/manual-connections/blob/e956c57849a38f912e654e0357f5ae456dfd1742/get_dip.sh#L100
	return resp[0].Ip, nil
}

type Latency struct {
	Time       time.Duration
	ServerIp   string
	RegionName string
}

func GetRegion(ctx context.Context, options ...Option) (string, error) {
	c := NewClient(options...)

	resp, err := c.Servers(ctx)
	if err != nil {
		return "", err
	}

	var latencies []Latency
	for _, r := range resp.Regions {
		if len(r.Servers.Meta) == 0 {
			continue
		}

		ip := r.Servers.Meta[0].Ip
		time, err := c.ConnTime(ctx, ip)
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
