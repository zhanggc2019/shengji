//go:build windows && !server

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type desktopSession struct {
	app    *application.App
	window atomic.Pointer[application.WebviewWindow]
}

// newDesktopSession registers reactivation before opening the listener or SQLite library.
func newDesktopSession(dataDir string) (*desktopSession, error) {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(strings.ToLower(root)))
	session := &desktopSession{}
	session.app = application.New(application.Options{
		Name: "声记", Description: "本地会议录音与纪要",
		Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(root, "webview"), AdditionalBrowserArgs: webviewTestArguments()},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: fmt.Sprintf("net.oddmeta.shengji.%x", key[:16]),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if window := session.window.Load(); window != nil {
					window.Show()
					window.Focus()
				}
			},
		},
	})
	return session, nil
}

// run keeps the shared local server alive while the native window is hidden.
func (session *desktopSession) run(ctx context.Context, url string, icon []byte) error {
	app := session.app
	if len(icon) > 0 {
		app.SetIcon(icon)
	}
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "声记", URL: url, Width: 1230, Height: 820, MinWidth: 760, MinHeight: 580,
	})
	session.window.Store(window)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		window.Hide()
	})
	tray := app.SystemTray.New()
	if len(icon) > 0 {
		tray.SetIcon(icon)
	}
	tray.SetTooltip("声记")
	menu := app.NewMenu()
	menu.Add("显示主窗口").OnClick(func(*application.Context) { window.Show(); window.Focus() })
	menu.Add("在浏览器中打开").OnClick(func(*application.Context) { _ = app.Browser.OpenURL(url) })
	menu.AddSeparator()
	menu.Add("退出声记").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.OnClick(func() { window.Show(); window.Focus() })
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			app.Quit()
		case <-done:
		}
	}()
	return app.Run()
}

// startupError displays the failure if a packaged executable has no console.
func startupError(err error) {
	title, _ := syscall.UTF16PtrFromString("声记")
	message, conversionErr := syscall.UTF16PtrFromString(err.Error())
	if conversionErr == nil {
		syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
	}
}
