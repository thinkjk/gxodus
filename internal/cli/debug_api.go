package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/config"
	"github.com/thinkjk/gxodus/internal/downloader"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var (
	debugRpcid       string
	debugArgs        string
	debugVersion     string
	debugUserIdx     int
	debugDownloadUUID string
)

var debugAPICmd = &cobra.Command{
	Use:    "debug-api",
	Short:  "Make a raw batchexecute call (debugging only)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, err := newDebugClient(debugUserIdx)
		if err != nil {
			return err
		}
		raw, err := client.CallRPC(ctx, debugRpcid, debugArgs, debugVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rpc failed: %v\n", err)
			os.Exit(1)
		}
		var pretty interface{}
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Println(string(raw))
			return nil
		}
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
		return nil
	},
}

// debug-tokens: fetch the takeout page, extract XSRF/build label/session ID,
// and print the cookie names. Doesn't make an rpc call — useful for verifying
// the auth state independently.
var debugTokensCmd = &cobra.Command{
	Use:    "debug-tokens",
	Short:  "Fetch the takeout page and report extracted tokens + cookies",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, err := newDebugClient(debugUserIdx)
		if err != nil {
			return err
		}
		// Calling fhjYTc with [] is the cheapest read-rpc; ensureTokens runs
		// as a side-effect and the first useful diagnostic dump happens too.
		if _, err := client.CallRPC(ctx, "fhjYTc", "[]", "generic"); err != nil {
			return fmt.Errorf("debug-tokens read probe failed: %w", err)
		}
		return nil
	},
}

// debug-list: convenience for fhjYTc — pretty-prints the parsed exports.
var debugListCmd = &cobra.Command{
	Use:    "debug-list",
	Short:  "Call fhjYTc and pretty-print exports",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, err := newDebugClient(debugUserIdx)
		if err != nil {
			return err
		}
		exports, err := client.ListExports(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("found %d exports\n", len(exports))
		for i, e := range exports {
			fmt.Printf("--- export %d ---\n", i)
			b, _ := json.MarshalIndent(e, "", "  ")
			fmt.Println(string(b))
		}
		return nil
	},
}

// debug-create: convenience for U5lrKc with each positional arg as a flag.
// Builds the args JSON for you so you don't have to escape braces.
var (
	debugCreateProducts string
	debugCreateFormat   string
	debugCreateFreq     int
	debugCreateSize     int64
	debugCreateFlag     int
	debugCreateTrailing string
)

var debugCreateCmd = &cobra.Command{
	Use:    "debug-create",
	Short:  "Call U5lrKc with simple flags (skips the 76-product default list)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, err := newDebugClient(debugUserIdx)
		if err != nil {
			return err
		}

		// Build the products array from comma-separated --products flag.
		products := [][]string{}
		for _, p := range strings.Split(debugCreateProducts, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				products = append(products, []string{p})
			}
		}
		if len(products) == 0 {
			return fmt.Errorf("--products must include at least one product slug")
		}

		payload := []interface{}{
			"ac.t.st",
			products,
			debugCreateFormat,
			nil,
			debugCreateFreq,
			nil,
			debugCreateSize,
			debugCreateFlag,
			nil, nil, nil,
			debugCreateTrailing,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}

		fmt.Printf("[debug-create] sending: %s\n", string(raw))
		resp, err := client.CallRPC(ctx, "U5lrKc", string(raw), "generic")
		if err != nil {
			fmt.Fprintf(os.Stderr, "rpc failed: %v\n", err)
			os.Exit(1)
		}
		var pretty interface{}
		if err := json.Unmarshal(resp, &pretty); err != nil {
			fmt.Println(string(resp))
			return nil
		}
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
		return nil
	},
}

var debugDownloadCmd = &cobra.Command{
	Use:    "debug-download",
	Short:  "Skip create+poll and download a known-complete export by UUID",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if !auth.SessionExists() {
			return fmt.Errorf("no saved session — run 'gxodus auth' first")
		}
		cookies, err := auth.LoadSession()
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}

		client, err := takeoutapi.NewClient(cookies, debugUserIdx)
		if err != nil {
			return err
		}
		exp, err := client.GetExport(ctx, debugDownloadUUID)
		if err != nil {
			return fmt.Errorf("looking up export: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("export %s not found", debugDownloadUUID)
		}
		if exp.Status != takeoutapi.StatusComplete {
			return fmt.Errorf("export %s not complete (status=%v)", debugDownloadUUID, exp.Status)
		}
		if len(exp.DownloadURLs) == 0 {
			return fmt.Errorf("export %s has no download URLs", debugDownloadUUID)
		}

		fmt.Printf("Downloading %d archive(s) for %s to %s\n",
			len(exp.DownloadURLs), exp.UUID, cfg.ResolveOutputDir())

		res, err := downloader.Download(ctx, exp.DownloadURLs, cfg.ResolveOutputDir(), cookies, cfg.Notify)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		fmt.Printf("Downloaded %d file(s), total %d bytes:\n", len(res.Files), res.TotalSize)
		for _, p := range res.Files {
			fmt.Printf("  %s\n", p)
		}
		return nil
	},
}

// newDebugClient loads the saved session and constructs a client. Shared by
// every debug-* command so they all behave identically wrt session loading.
func newDebugClient(userIdx int) (*takeoutapi.Client, error) {
	if !auth.SessionExists() {
		return nil, fmt.Errorf("no saved session — run 'gxodus auth' first")
	}
	cookies, err := auth.LoadSession()
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}
	return takeoutapi.NewClient(cookies, userIdx)
}

func init() {
	// debug-api
	debugAPICmd.Flags().StringVar(&debugRpcid, "rpcid", "", "batchexecute rpcid (e.g. fhjYTc)")
	debugAPICmd.Flags().StringVar(&debugArgs, "args", "[]", "rpc args as JSON string")
	debugAPICmd.Flags().StringVar(&debugVersion, "version", "generic", `rpc version, "generic" or "1"`)
	debugAPICmd.Flags().IntVar(&debugUserIdx, "user", 0, "Google account index (0 = primary)")
	_ = debugAPICmd.MarkFlagRequired("rpcid")
	rootCmd.AddCommand(debugAPICmd)

	// debug-tokens
	debugTokensCmd.Flags().IntVar(&debugUserIdx, "user", 0, "Google account index (0 = primary)")
	rootCmd.AddCommand(debugTokensCmd)

	// debug-list
	debugListCmd.Flags().IntVar(&debugUserIdx, "user", 0, "Google account index (0 = primary)")
	rootCmd.AddCommand(debugListCmd)

	// debug-create — defaults match the values from the 2026-05-02 spike capture
	// (1 GB so a real export is small enough to ignore if accidentally created).
	debugCreateCmd.Flags().StringVar(&debugCreateProducts, "products", "drive", "comma-separated product slugs")
	debugCreateCmd.Flags().StringVar(&debugCreateFormat, "format", "ZIP", `archive format: "ZIP" | "TGZ"`)
	debugCreateCmd.Flags().IntVar(&debugCreateFreq, "freq", 5, "frequency code (5 = once per spike)")
	debugCreateCmd.Flags().Int64Var(&debugCreateSize, "size", 1<<30, "archive split size in bytes (default 1 GiB)")
	debugCreateCmd.Flags().IntVar(&debugCreateFlag, "flag", 1, `the unknown "1" positional flag`)
	debugCreateCmd.Flags().StringVar(&debugCreateTrailing, "trailing", "2", `the unknown trailing positional value`)
	debugCreateCmd.Flags().IntVar(&debugUserIdx, "user", 0, "Google account index (0 = primary)")
	rootCmd.AddCommand(debugCreateCmd)

	// debug-download
	debugDownloadCmd.Flags().StringVar(&debugDownloadUUID, "uuid", "", "UUID of an existing complete export")
	_ = debugDownloadCmd.MarkFlagRequired("uuid")
	rootCmd.AddCommand(debugDownloadCmd)
}
