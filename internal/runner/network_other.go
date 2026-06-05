//go:build !windows

package runner

import "github.com/meshmux/meshmux/internal/config"

func postStartNetwork(cfg *config.Config) error {
	return nil
}
