package pia_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/unmango/thecluster-operator/internal/pia"
)

var _ = Describe("Auth", func() {
	It("should request a token", Pending, func(ctx context.Context) {
		c := pia.NewClient()

		res, err := c.GetToken(ctx)

		Expect(err).NotTo(HaveOccurred())
		Expect(res.Token).NotTo(BeEmpty())
	})
})
