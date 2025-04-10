package pia_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/unmango/thecluster-operator/internal/pia"
)

var _ = Describe("Client", func() {
	It("should list servers", func(ctx context.Context) {
		c := pia.NewClient()

		res, err := c.Servers(ctx)

		Expect(err).NotTo(HaveOccurred())
		Expect(len(res.Regions)).To(BeNumerically(">", 1))
	})
})
