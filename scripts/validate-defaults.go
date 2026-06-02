package main

import (
	"fmt"
	"os"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

func main() {
	cfg := config.Default()
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "Error: Default config returned nil")
		os.Exit(1)
	}
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Default config validation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Success: Embedded default config is valid.")
}
