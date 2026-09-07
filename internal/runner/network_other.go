//go:build !windows

package runner

import (
	"context"
	"github.com/meshmux/meshmux/internal/config"
)

func postStartNetwork(_ context.Context, cfg *config.Config) error {
	return nil
}
