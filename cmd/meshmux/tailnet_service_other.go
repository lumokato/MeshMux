//go:build !darwin

package main

import "fmt"

func macTailnetService([]string) error { return fmt.Errorf("tailnet-service is macOS only") }
