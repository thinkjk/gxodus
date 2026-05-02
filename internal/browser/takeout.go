package browser

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"github.com/thinkjk/gxodus/internal/config"
)

const (
	takeoutURL       = "https://takeout.google.com"
	takeoutManageURL = "https://takeout.google.com/takeout/downloads"
)

// ExportResult contains information about an initiated export.
type ExportResult struct {
	Started time.Time
}

type ExportOptions struct {
	FileSize string // e.g. "1GB", "2GB", "4GB", "10GB", "50GB"
}

// InitiateExport navigates to Google Takeout and creates a new export.
func InitiateExport(ctx context.Context, opts ExportOptions) (*ExportResult, error) {
	fmt.Println("Navigating to Google Takeout...")

	if err := chromedp.Run(ctx, chromedp.Navigate(takeoutURL)); err != nil {
		return nil, wrapErr(ctx, "navigating to takeout", err)
	}

	if err := chromedp.Run(ctx, chromedp.Sleep(3*time.Second)); err != nil {
		return nil, wrapErr(ctx, "waiting for page load", err)
	}

	logPageState(ctx, "after takeout navigate")

	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err != nil {
		return nil, wrapErr(ctx, "checking URL", err)
	}
	if strings.Contains(currentURL, "accounts.google.com") {
		return nil, fmt.Errorf("session expired: redirected to login page")
	}

	fmt.Println("On Takeout page. Configuring export...")

	if err := scrollAndClickNextStep(ctx); err != nil {
		return nil, wrapErr(ctx, "clicking next step", err)
	}

	logPageState(ctx, "after next-step click")

	fmt.Println("Configuring export options...")

	if err := configureExportOptions(ctx, opts.FileSize); err != nil {
		return nil, wrapErr(ctx, "configuring export options", err)
	}

	fmt.Println("Creating export...")

	if err := clickCreateExport(ctx); err != nil {
		return nil, wrapErr(ctx, "creating export", err)
	}

	logPageState(ctx, "after create-export click")

	fmt.Println("Export initiated successfully!")

	return &ExportResult{
		Started: time.Now(),
	}, nil
}

// logPageState prints the current URL and page title for debugging. Best-effort:
// chromedp errors are swallowed so we never derail the actual flow over diagnostics.
func logPageState(ctx context.Context, label string) {
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var url, title string
	_ = chromedp.Run(dctx,
		chromedp.Location(&url),
		chromedp.Title(&title),
	)
	fmt.Printf("[diag] %s: url=%q title=%q\n", label, url, title)
}

// dumpButtons enumerates every clickable element on the page along with its
// text, aria-label, and role. Use when a selector miss leaves us blind to what
// Google actually rendered.
func dumpButtons(ctx context.Context, label string) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	const script = `
		(() => {
			const out = [];
			const seen = new Set();
			const sels = ['button', '[role="button"]', 'a[role="button"]', 'div[role="button"]', '[jsname]'];
			for (const sel of sels) {
				for (const el of document.querySelectorAll(sel)) {
					if (seen.has(el)) continue;
					seen.add(el);
					const r = el.getBoundingClientRect();
					if (r.width === 0 && r.height === 0) continue;
					out.push({
						tag: el.tagName.toLowerCase(),
						text: (el.innerText || el.textContent || '').trim().slice(0, 80),
						aria: el.getAttribute('aria-label') || '',
						role: el.getAttribute('role') || '',
						jsname: el.getAttribute('jsname') || '',
						id: el.id || '',
						cls: (el.className || '').toString().slice(0, 60),
					});
				}
			}
			return JSON.stringify(out);
		})()
	`
	var result string
	if err := chromedp.Run(dctx, chromedp.Evaluate(script, &result)); err != nil {
		fmt.Printf("[diag] %s: button enumeration failed: %v\n", label, err)
		return
	}
	fmt.Printf("[diag] %s: visible-buttons-json=%s\n", label, result)
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
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(1*time.Second),
	); err != nil {
		return err
	}

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
			fmt.Printf("[diag] next-step button matched selector %q (%d nodes)\n", sel, len(nodes))
			return chromedp.Run(ctx,
				chromedp.Click(sel, queryOpt),
				chromedp.Sleep(2*time.Second),
			)
		}
		fmt.Printf("[diag] next-step selector %q: 0 matches\n", sel)
	}

	logPageState(ctx, "next-step-not-found")
	dumpButtons(ctx, "next-step-not-found")
	return fmt.Errorf("could not find 'Next step' button — Google may have changed the Takeout UI (see screenshot in $CFG/debug)")
}

func configureExportOptions(ctx context.Context, fileSize string) error {
	// Wait for the options page to load
	if err := chromedp.Run(ctx, chromedp.Sleep(2*time.Second)); err != nil {
		return err
	}

	// Select file size if specified and different from default
	if fileSize != "" && fileSize != "2GB" {
		// Google Takeout has a dropdown for file size with options like 1GB, 2GB, 4GB, 10GB, 50GB
		// Try to find and click the file size dropdown, then select the desired size
		sizeSelectors := []string{
			`//span[contains(text(), "GB")]`,
			`[data-value*="GB"]`,
		}

		for _, sel := range sizeSelectors {
			var nodes []*cdp.Node
			queryOpt := chromedp.ByQuery
			if strings.HasPrefix(sel, "//") {
				queryOpt = chromedp.BySearch
			}
			err := chromedp.Run(ctx, chromedp.Nodes(sel, &nodes, queryOpt, chromedp.AtLeast(0)))
			if err == nil && len(nodes) > 0 {
				// Click the dropdown to open it
				if err := chromedp.Run(ctx,
					chromedp.Click(sel, queryOpt),
					chromedp.Sleep(1*time.Second),
				); err != nil {
					continue
				}

				// Now select the desired size
				sizeText := fileSize
				optionSelectors := []string{
					fmt.Sprintf(`//li[contains(text(), "%s")]`, sizeText),
					fmt.Sprintf(`//div[contains(text(), "%s")]`, sizeText),
					fmt.Sprintf(`//option[contains(text(), "%s")]`, sizeText),
					fmt.Sprintf(`[data-value="%s"]`, sizeText),
				}

				for _, optSel := range optionSelectors {
					var optNodes []*cdp.Node
					optQueryOpt := chromedp.ByQuery
					if strings.HasPrefix(optSel, "//") {
						optQueryOpt = chromedp.BySearch
					}
					err := chromedp.Run(ctx, chromedp.Nodes(optSel, &optNodes, optQueryOpt, chromedp.AtLeast(0)))
					if err == nil && len(optNodes) > 0 {
						chromedp.Run(ctx,
							chromedp.Click(optSel, optQueryOpt),
							chromedp.Sleep(1*time.Second),
						)
						fmt.Printf("Selected file size: %s\n", fileSize)
						return nil
					}
				}
				break
			}
		}

		fmt.Printf("Warning: could not set file size to %s, using Takeout default\n", fileSize)
	}

	return nil
}

func clickCreateExport(ctx context.Context) error {
	logPageState(ctx, "before create-export search")

	// Scroll to bottom in case the button is below the fold on this page.
	_ = chromedp.Run(ctx,
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(1*time.Second),
	)

	// Google Takeout's "Create export" is a Material Design button: the visible
	// text lives in <span jsname="V67aGc">Create export</span> wrapped in a
	// clickable button-role ancestor. Try the most specific matches first.
	selectors := []string{
		`//span[@jsname="V67aGc"]/ancestor::button[1]`,
		`//span[@jsname="V67aGc"]/ancestor::*[@role="button"][1]`,
		`//button[.//span[normalize-space(text())="Create export"]]`,
		`//*[@role="button"][.//span[normalize-space(text())="Create export"]]`,
		`//span[@jsname="V67aGc"]`, // last resort: click the span itself, hoping the event bubbles
	}

	for _, sel := range selectors {
		var nodes []*cdp.Node
		err := chromedp.Run(ctx, chromedp.Nodes(sel, &nodes, chromedp.BySearch, chromedp.AtLeast(0)))
		if err == nil && len(nodes) > 0 {
			fmt.Printf("[diag] create-export matched selector %q (%d nodes)\n", sel, len(nodes))
			return chromedp.Run(ctx,
				chromedp.Click(sel, chromedp.BySearch),
				chromedp.Sleep(3*time.Second),
			)
		}
		fmt.Printf("[diag] create-export selector %q: 0 matches (err=%v)\n", sel, err)
	}

	// Last-resort JS click: walk up from the span to the nearest clickable
	// ancestor and call .click() directly. Bypasses chromedp's coordinate-based
	// click in case the element is offscreen or covered.
	var jsClicked bool
	jsErr := chromedp.Run(ctx, chromedp.Evaluate(`
		(() => {
			const span = document.querySelector('span[jsname="V67aGc"]');
			if (!span) return false;
			const target = span.closest('button, [role="button"]') || span.parentElement;
			if (!target) return false;
			target.scrollIntoView({block: 'center'});
			target.click();
			return true;
		})()
	`, &jsClicked))
	if jsErr == nil && jsClicked {
		fmt.Println("[diag] create-export clicked via JS .click() fallback")
		return chromedp.Run(ctx, chromedp.Sleep(3*time.Second))
	}
	fmt.Printf("[diag] create-export JS fallback failed: err=%v clicked=%v\n", jsErr, jsClicked)

	logPageState(ctx, "create-export-not-found")
	dumpButtons(ctx, "create-export-not-found")
	return fmt.Errorf("could not find 'Create export' button — Google may have changed the Takeout UI (see screenshot in $CFG/debug)")
}

func fileTypeDisplayText(v string) (string, error) {
	switch v {
	case "", "zip":
		return ".zip", nil
	case "tgz":
		return ".tgz", nil
	default:
		return "", fmt.Errorf("unknown file_type %q (use zip|tgz)", v)
	}
}

func fileSizeDisplayText(v string) (string, error) {
	switch v {
	case "", "2GB":
		return "2 GB", nil
	case "1GB":
		return "1 GB", nil
	case "4GB":
		return "4 GB", nil
	case "10GB":
		return "10 GB", nil
	case "50GB":
		return "50 GB", nil
	default:
		return "", fmt.Errorf("unknown file_size %q (use 1GB|2GB|4GB|10GB|50GB)", v)
	}
}

func frequencyRadioValue(v string) (string, error) {
	switch v {
	case "", "once":
		return "1", nil
	case "every_2_months":
		return "2", nil
	default:
		return "", fmt.Errorf("unknown frequency %q (use once|every_2_months)", v)
	}
}

func wrapErr(ctx context.Context, step string, err error) error {
	name := "error-" + strings.ReplaceAll(step, " ", "-")
	if shotErr := Screenshot(ctx, name); shotErr != nil {
		fmt.Fprintf(os.Stderr, "[diag] could not save screenshot %q: %v\n", name, shotErr)
	} else {
		fmt.Fprintf(os.Stderr, "[diag] screenshot saved as %s-*.png in %s/debug\n", name, config.ConfigDir())
	}
	logPageState(ctx, "wrapErr/"+step)
	return fmt.Errorf("%s: %w", step, err)
}

// selectActivityLog ticks the "Access Log Activity" checkbox on the Takeout
// data-selection page. Idempotent: no-op if already checked. Confirmed via DOM
// dump 2026-05-01 — the input is the only element with this exact aria-label.
func selectActivityLog(ctx context.Context) error {
	const sel = `input[aria-label="Select Access Log Activity"]`
	const checkScript = `(() => {
		const el = document.querySelector('input[aria-label="Select Access Log Activity"]');
		return el ? el.checked : null;
	})()`

	var checked any
	if err := chromedp.Run(ctx, chromedp.Evaluate(checkScript, &checked)); err != nil {
		return fmt.Errorf("checking access log activity state: %w", err)
	}
	if checked == nil {
		return fmt.Errorf("access log activity checkbox not found on page")
	}
	if b, ok := checked.(bool); ok && b {
		fmt.Println("[diag] Access Log Activity already selected")
		return nil
	}

	fmt.Println("[diag] selecting Access Log Activity (off by default)")
	return chromedp.Run(ctx,
		chromedp.Click(sel, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	)
}

// selectComboboxOption opens a Material Design combobox and clicks the option
// matching optionText. Confirmed via DOM dump 2026-05-01 — Takeout's option
// elements use role="option" with the visible text as innerText.
func selectComboboxOption(ctx context.Context, comboSelector, optionText string) error {
	if err := chromedp.Run(ctx,
		chromedp.Click(comboSelector, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		return fmt.Errorf("opening combobox %q: %w", comboSelector, err)
	}

	optSel := fmt.Sprintf(`//*[@role="option"][normalize-space()=%q]`, optionText)
	if err := chromedp.Run(ctx,
		chromedp.Click(optSel, chromedp.BySearch),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		return fmt.Errorf("selecting option %q in %s: %w", optionText, comboSelector, err)
	}
	fmt.Printf("[diag] selected %q in combobox %s\n", optionText, comboSelector)
	return nil
}

func setFileType(ctx context.Context, value string) error {
	text, err := fileTypeDisplayText(value)
	if err != nil {
		return err
	}
	return selectComboboxOption(ctx, `div[role="combobox"][aria-label="File type select"]`, text)
}

func setFileSize(ctx context.Context, value string) error {
	text, err := fileSizeDisplayText(value)
	if err != nil {
		return err
	}
	return selectComboboxOption(ctx, `div[role="combobox"][aria-label="File size select"]`, text)
}

func setFrequency(ctx context.Context, value string) error {
	radioVal, err := frequencyRadioValue(value)
	if err != nil {
		return err
	}
	sel := fmt.Sprintf(`input[type="radio"][name="scheduleoptions"][value="%s"]`, radioVal)
	if err := chromedp.Run(ctx,
		chromedp.Click(sel, chromedp.ByQuery),
		chromedp.Sleep(300*time.Millisecond),
	); err != nil {
		return fmt.Errorf("clicking frequency radio %q: %w", radioVal, err)
	}
	fmt.Printf("[diag] set frequency radio value=%s (config=%s)\n", radioVal, value)
	return nil
}
