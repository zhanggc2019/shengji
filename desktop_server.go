//go:build !windows || server

package main

import "context"

type desktopSession struct{}

// newDesktopSession keeps the same startup flow for the browser-only build.
func newDesktopSession(_ string) (*desktopSession, error) { return &desktopSession{}, nil }

// run leaves the headless build running until the local service is stopped.
func (*desktopSession) run(ctx context.Context, _ string, _ []byte) error {
	<-ctx.Done()
	return nil
}

// startupError leaves headless diagnostics in the standard log.
func startupError(_ error) {}
