//go:build !darwin

package runner

import (
	"context"
	"errors"
)

func platformTailscaleSocket() string { return "" }
func platformTailscaleCLI() string    { return "" }
func MacTailscaleInstalled() bool     { return false }
func MacTailscaleService(context.Context, string) (string, error) {
	return "", errors.New("macOS only")
}
func ValidateMacTailscaleBundle(string) error { return nil }
