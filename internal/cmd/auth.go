package cmd

import (
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/spf13/cobra"
)

// newAuthCmd builds the 'auth' command group, which holds OAuth-based sign-in
// flows that do not fit the simple token-paste model of 'login'. Today its only
// subcommand is the experimental 'auth chatgpt'.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Sign in via OAuth (experimental)",
		Long:  "OAuth-based sign-in flows. Currently only 'auth chatgpt' (experimental).",
	}
	cmd.AddCommand(newAuthChatGPTCmd())
	return cmd
}

// newAuthChatGPTCmd wires the experimental "Sign in with ChatGPT" OAuth (PKCE)
// flow. It opens a browser, runs a loopback callback server, and stores the
// resulting subscription tokens so the 'chatgpt' provider can drive a model with
// the user's own ChatGPT plan.
func newAuthChatGPTCmd() *cobra.Command {
	var status, logout bool
	cmd := &cobra.Command{
		Use:   "chatgpt",
		Short: "Sign in with ChatGPT (EXPERIMENTAL)",
		Long: "Sign in with your ChatGPT account using OAuth (PKCE) so the 'chatgpt'\n" +
			"provider can use your own ChatGPT subscription.\n\n" +
			"EXPERIMENTAL and unsupported: this relies on undocumented OpenAI endpoints\n" +
			"that may change or break without notice, is outside OpenAI's terms for\n" +
			"third-party clients, and is intended for personal single-account use only\n" +
			"(no account pooling).",
		Example: "  bharatcode auth chatgpt\n  bharatcode auth chatgpt --status\n  bharatcode auth chatgpt --logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch {
			case logout:
				if err := llm.LogoutChatGPT(); err != nil {
					return fmt.Errorf("signing out of ChatGPT: %w", err)
				}
				_, _ = fmt.Fprintln(out, "Signed out of ChatGPT.")
				return nil
			case status:
				id, err := llm.ChatGPTStatus()
				if err != nil {
					return err
				}
				printChatGPTStatus(cmd, id)
				return nil
			default:
				_, _ = fmt.Fprintln(out, "Sign in with ChatGPT (EXPERIMENTAL — personal single-account use only).")
				id, err := llm.LoginChatGPT(cmd.Context(), out)
				if err != nil {
					return fmt.Errorf("signing in with ChatGPT: %w", err)
				}
				printChatGPTStatus(cmd, id)
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "show the current ChatGPT sign-in status")
	cmd.Flags().BoolVar(&logout, "logout", false, "remove the stored ChatGPT credentials")
	return cmd
}

// printChatGPTStatus prints the signed-in identity and token expiry.
func printChatGPTStatus(cmd *cobra.Command, id llm.ChatGPTIdentity) {
	out := cmd.OutOrStdout()
	who := id.Email
	if who == "" {
		who = "(unknown account)"
	}
	line := "Signed in to ChatGPT as " + who
	if id.Plan != "" {
		line += " on the " + id.Plan + " plan"
	}
	_, _ = fmt.Fprintln(out, line)
	if !id.ExpiresAt.IsZero() {
		state := "valid"
		if id.Expired {
			state = "expired (will refresh on next use)"
		}
		_, _ = fmt.Fprintf(out, "Access token: %s (expires %s)\n", state, id.ExpiresAt.Format("2006-01-02 15:04 MST"))
	}
}
