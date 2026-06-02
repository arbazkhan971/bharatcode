package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const keyringService = "bharatcode"

type keyringBackend interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

type unavailableKeyring struct{}

var keyring keyringBackend = unavailableKeyring{}

func (unavailableKeyring) Get(service, account string) (string, error) {
	_ = service
	_ = account
	return "", fmt.Errorf("production keyring is not configured")
}

func (unavailableKeyring) Set(service, account, secret string) error {
	_ = service
	_ = account
	_ = secret
	return fmt.Errorf("production keyring is not configured")
}

func (unavailableKeyring) Delete(service, account string) error {
	_ = service
	_ = account
	return nil
}

func newLoginCmd() *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:     "login <provider>",
		Short:   "Store a provider token",
		Example: "  bharatcode login openai --token $OPENAI_API_KEY",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := args[0]
			if token == "" {
				value, err := readSecret(cmd, "Token")
				if err != nil {
					return err
				}
				token = value
			}
			if err := keyring.Set(keyringService, provider, token); err != nil {
				return fmt.Errorf("keyring unavailable: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s\n", provider)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "token to store")
	return cmd
}
