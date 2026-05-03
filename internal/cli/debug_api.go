package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thinkjk/gxodus/internal/auth"
	"github.com/thinkjk/gxodus/internal/takeoutapi"
)

var (
	debugRpcid   string
	debugArgs    string
	debugVersion string
	debugUserIdx int
)

var debugAPICmd = &cobra.Command{
	Use:    "debug-api",
	Short:  "Make a raw batchexecute call (debugging only)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if !auth.SessionExists() {
			return fmt.Errorf("no saved session — run 'gxodus auth' first")
		}
		cookies, err := auth.LoadSession()
		if err != nil {
			return fmt.Errorf("loading session: %w", err)
		}

		client, err := takeoutapi.NewClient(cookies, debugUserIdx)
		if err != nil {
			return fmt.Errorf("creating client: %w", err)
		}

		raw, err := client.CallRPC(ctx, debugRpcid, debugArgs, debugVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rpc failed: %v\n", err)
			os.Exit(1)
		}

		// Pretty-print the JSON for human reading.
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

func init() {
	debugAPICmd.Flags().StringVar(&debugRpcid, "rpcid", "", "batchexecute rpcid (e.g. fhjYTc)")
	debugAPICmd.Flags().StringVar(&debugArgs, "args", "[]", "rpc args as JSON string")
	debugAPICmd.Flags().StringVar(&debugVersion, "version", "generic", `rpc version, "generic" or "1"`)
	debugAPICmd.Flags().IntVar(&debugUserIdx, "user", 0, "Google account index (0 = primary)")
	_ = debugAPICmd.MarkFlagRequired("rpcid")
	rootCmd.AddCommand(debugAPICmd)
}
