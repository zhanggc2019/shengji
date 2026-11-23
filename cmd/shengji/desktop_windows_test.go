//go:build windows && !server

package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestDesktopWindowFitsWorkArea(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		workWidth, workHeight int
		width, height         int
		minWidth, minHeight   int
	}{
		{"large", 1920, 1040, 920, 640, 360, 320},
		{"scaled full HD", 1280, 664, 920, 640, 360, 320},
		{"scaled laptop", 683, 470, 641, 446, 360, 320},
		{"narrow work area", 600, 900, 576, 400, 360, 320},
		{"below minimum", 340, 300, 316, 276, 316, 276},
	} {
		t.Run(tc.name, func(t *testing.T) {
			screen := &application.Screen{WorkArea: application.Rect{Width: tc.workWidth, Height: tc.workHeight}}
			options := desktopWindowOptions("http://127.0.0.1:9011", screen)
			if options.Screen != screen || options.Width != tc.width || options.Height != tc.height ||
				options.MinWidth != tc.minWidth || options.MinHeight != tc.minHeight {
				t.Fatalf("got %dx%d, minimum %dx%d, screen %p", options.Width, options.Height, options.MinWidth, options.MinHeight, options.Screen)
			}
		})
	}
}
