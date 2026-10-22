package proxy

import (
	"context"
	"fmt"
	"time"
)

func CloseJob(close func()) {
	if close != nil {
		close()
	}
}

// ProbeTargets requires one independently reachable destination, not all of them.
func ProbeTargets(ctx context.Context, bridge *Bridge, targets []string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make(chan error, len(targets))
	for _, target := range targets {
		go func(target string) { results <- bridge.Probe(ctx, target, timeout) }(target)
	}
	var last error
	for range targets {
		select {
		case err := <-results:
			if err == nil {
				return nil
			}
			last = err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("all forwarding probes failed: %v", last)
}
