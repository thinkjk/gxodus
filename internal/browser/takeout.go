package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

const (
	takeoutURL       = "https://takeout.google.com"
	takeoutManageURL = "https://takeout.google.com/takeout/downloads"
)

// ExportResult contains information about an initiated export.
type ExportResult struct {
	Started time.Time
}

// InitiateExport navigates to Google Takeout and creates a new export.
func InitiateExport(ctx context.Context) (*ExportResult, error) {
	fmt.Println("Navigating to Google Takeout...")

	if err := chromedp.Run(ctx, chromedp.Navigate(takeoutURL)); err != nil {
		return nil, wrapErr(ctx, "navigating to takeout", err)
	}

	// Wait for page to load
	if err := chromedp.Run(ctx, chromedp.Sleep(3*time.Second)); err != nil {
		return nil, wrapErr(ctx, "waiting for page load", err)
	}

	// Check if we got redirected to login
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return nil, wrapErr(ctx, "checking URL", err)
	}

	if strings.Contains(currentURL, "accounts.google.com") {
		return nil, fmt.Errorf("session expired: redirected to login page")
	}

	fmt.Println("On Takeout page. Configuring export...")

	// Scroll to bottom to find the "Next step" button
	// Google Takeout has a list of services then a "Next step" button
	if err := scrollAndClickNextStep(ctx); err != nil {
		return nil, wrapErr(ctx, "clicking next step", err)
	}

	fmt.Println("Configuring export options...")

	// On the export options page, configure:
	// - Delivery method: typically "Send download link via email" is default
	// - Frequency: "Export once"
	// - File type: ZIP
	// - File size: 2GB
	if err := configureExportOptions(ctx); err != nil {
		return nil, wrapErr(ctx, "configuring export options", err)
	}

	fmt.Println("Creating export...")

	// Click "Create export" button
	if err := clickCreateExport(ctx); err != nil {
		return nil, wrapErr(ctx, "creating export", err)
	}

	fmt.Println("Export initiated successfully!")

	return &ExportResult{
		Started: time.Now(),
	}, nil
}

// CheckExportStatus navigates to the downloads page and checks export status.
type ExportStatus struct {
	State        string   // "in_progress", "complete", "failed"
	DownloadURLs []string
}

func CheckExportStatus(ctx context.Context) (*ExportStatus, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate(takeoutManageURL)); err != nil {
		return nil, wrapErr(ctx, "navigating to manage exports", err)
	}

	// Wait for page to load
	if err := chromedp.Run(ctx, chromedp.Sleep(3*time.Second)); err != nil {
		return nil, err
	}

	// Check if redirected to login
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return nil, wrapErr(ctx, "checking URL", err)
	}

	if strings.Contains(currentURL, "accounts.google.com") {
		return nil, fmt.Errorf("session expired: redirected to login page")
	}

	// Look for download buttons/links
	var downloadNodes []*cdp.Node
	err := chromedp.Run(ctx, chromedp.Nodes(`a[href*="download"]`, &downloadNodes, chromedp.ByQueryAll, chromedp.AtLeast(0)))
	if err != nil {
		return nil, wrapErr(ctx, "finding download links", err)
	}

	if len(downloadNodes) > 0 {
		var urls []string
		for _, node := range downloadNodes {
			for _, attr := range node.Attributes {
				// Attributes come in name/value pairs
				if attr == "href" {
					continue
				}
				if strings.Contains(attr, "download") || strings.Contains(attr, "takeout") {
					urls = append(urls, attr)
				}
			}
		}

		// Also try to extract hrefs properly
		for _, node := range downloadNodes {
			var href string
			if err := chromedp.Run(ctx, chromedp.AttributeValue(node.FullXPath(), "href", &href, nil)); err == nil && href != "" {
				urls = append(urls, href)
			}
		}

		if len(urls) > 0 {
			return &ExportStatus{State: "complete", DownloadURLs: urls}, nil
		}
	}

	// Check for "in progress" indicators
	var pageText string
	if err := chromedp.Run(ctx, chromedp.Text("body", &pageText, chromedp.ByQuery)); err != nil {
		return nil, wrapErr(ctx, "reading page text", err)
	}

	pageTextLower := strings.ToLower(pageText)
	if strings.Contains(pageTextLower, "in progress") || strings.Contains(pageTextLower, "being created") {
		return &ExportStatus{State: "in_progress"}, nil
	}

	if strings.Contains(pageTextLower, "no exports") {
		return &ExportStatus{State: "none"}, nil
	}

	// If we can't determine status, take a screenshot for debugging
	_ = Screenshot(ctx, "status-unknown")
	return &ExportStatus{State: "unknown"}, nil
}

func scrollAndClickNextStep(ctx context.Context) error {
	// Scroll to bottom of the page where the "Next step" button typically is
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(1*time.Second),
	); err != nil {
		return err
	}

	// Try various selectors for the "Next step" button
	selectors := []string{
		`button[aria-label="Next step"]`,
		`//button[contains(text(), "Next step")]`,
		`//button[contains(text(), "Next")]`,
		`[data-action="next"]`,
	}

	for _, sel := range selectors {
		var nodes []*cdp.Node
		queryOpt := chromedp.ByQuery
		if strings.HasPrefix(sel, "//") {
			queryOpt = chromedp.BySearch
		}
		err := chromedp.Run(ctx, chromedp.Nodes(sel, &nodes, queryOpt, chromedp.AtLeast(0)))
		if err == nil && len(nodes) > 0 {
			return chromedp.Run(ctx,
				chromedp.Click(sel, queryOpt),
				chromedp.Sleep(2*time.Second),
			)
		}
	}

	_ = Screenshot(ctx, "next-step-not-found")
	return fmt.Errorf("could not find 'Next step' button — Google may have changed the Takeout UI")
}

func configureExportOptions(ctx context.Context) error {
	// The export options page should already have reasonable defaults
	// (export once, ZIP, 2GB). We just wait for it to load and proceed.
	if err := chromedp.Run(ctx, chromedp.Sleep(2*time.Second)); err != nil {
		return err
	}

	return nil
}

func clickCreateExport(ctx context.Context) error {
	selectors := []string{
		`button[aria-label="Create export"]`,
		`//button[contains(text(), "Create export")]`,
		`//button[contains(text(), "Create")]`,
	}

	for _, sel := range selectors {
		var nodes []*cdp.Node
		queryOpt := chromedp.ByQuery
		if strings.HasPrefix(sel, "//") {
			queryOpt = chromedp.BySearch
		}
		err := chromedp.Run(ctx, chromedp.Nodes(sel, &nodes, queryOpt, chromedp.AtLeast(0)))
		if err == nil && len(nodes) > 0 {
			return chromedp.Run(ctx,
				chromedp.Click(sel, queryOpt),
				chromedp.Sleep(3*time.Second),
			)
		}
	}

	_ = Screenshot(ctx, "create-export-not-found")
	return fmt.Errorf("could not find 'Create export' button — Google may have changed the Takeout UI")
}

func wrapErr(ctx context.Context, step string, err error) error {
	_ = Screenshot(ctx, "error-"+strings.ReplaceAll(step, " ", "-"))
	return fmt.Errorf("%s: %w", step, err)
}
