//go:build !windows

package minutes

import "context"

// WindowsCapture reports unavailable device capture on browser servers outside Windows.
func WindowsCapture(_ context.Context, _ CaptureOptions, _ chan<- error, _ func([]int16, []int16) error) error {
	return errNoCapture
}
