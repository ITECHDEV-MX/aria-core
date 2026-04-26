package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pdf"
)

// dashboardPDFAdapter wraps *pdf.Client so it satisfies dashboard.PDFClient
// without leaking the concrete pdf.ConvertOptions struct into the dashboard
// package. The adapter only translates between two structurally identical
// types.
type dashboardPDFAdapter struct {
	client *pdf.Client
}

func newDashboardPDFAdapter(c *pdf.Client) dashboard.PDFClient {
	if c == nil {
		return nil
	}
	return &dashboardPDFAdapter{client: c}
}

func (a *dashboardPDFAdapter) ConvertHTML(ctx context.Context, htmlBytes []byte, opts dashboard.PDFConvertOptions) ([]byte, error) {
	return a.client.ConvertHTML(ctx, htmlBytes, pdf.ConvertOptions{
		PaperWidth:        opts.PaperWidth,
		PaperHeight:       opts.PaperHeight,
		MarginTop:         opts.MarginTop,
		MarginBottom:      opts.MarginBottom,
		MarginLeft:        opts.MarginLeft,
		MarginRight:       opts.MarginRight,
		PreferCSSPageSize: opts.PreferCSSPageSize,
		PrintBackground:   opts.PrintBackground,
	})
}
