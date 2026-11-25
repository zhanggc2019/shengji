//go:build windows && !server

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/zhanggc2019/shengji/internal/buildinfo"
	"golang.org/x/sys/windows/registry"
)

const wmDWMColorizationColorChanged = 0x0320

type desktopSession struct {
	app                *application.App
	window             atomic.Pointer[application.WebviewWindow]
	updateBusy         atomic.Bool
	lastOfferedVersion string
	origin             string
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
		Windows: application.WindowsOptions{
			WebviewUserDataPath:   filepath.Join(root, "webview"),
			AdditionalBrowserArgs: webviewTestArguments(),
			WndProcInterceptor: func(_ uintptr, message uint32, _, _ uintptr) (uintptr, bool) {
				if message == wmDWMColorizationColorChanged {
					go session.updateAccent()
				}
				return 0, false
			},
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: fmt.Sprintf("net.oddmeta.shengji.%x", key[:16]),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				session.showWindow()
			},
		},
	})
	return session, nil
}

func desktopWindowOptions(url string, screen *application.Screen) application.WebviewWindowOptions {
	const defaultWidth, defaultHeight = 920, 640
	options := application.WebviewWindowOptions{
		Title: "声记", URL: url, Width: defaultWidth, Height: defaultHeight, MinWidth: 360, MinHeight: 320,
	}
	if screen != nil && screen.WorkArea.Width > 0 && screen.WorkArea.Height > 0 {
		const screenMargin = 12
		options.Screen = screen
		options.Width = min(options.Width, max(1, screen.WorkArea.Width-2*screenMargin))
		options.Height = min(options.Height, max(1, screen.WorkArea.Height-2*screenMargin))
		if options.Width*defaultHeight < options.Height*defaultWidth {
			heightForWidth := max(options.MinHeight, options.Width*defaultHeight/defaultWidth)
			options.Height = min(options.Height, heightForWidth)
		} else {
			widthForHeight := max(options.MinWidth, options.Height*defaultWidth/defaultHeight)
			options.Width = min(options.Width, widthForHeight)
		}
		options.MinWidth = min(options.MinWidth, options.Width)
		options.MinHeight = min(options.MinHeight, options.Height)
	}
	return options
}

func (session *desktopSession) showWindow() {
	if window := session.window.Load(); window != nil {
		window.Show()
		window.Focus()
	}
}

func windowsAccent() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\DWM`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	color, _, err := key.GetIntegerValue("AccentColor")
	if err != nil {
		return ""
	}
	// Windows stores the accent as an ABGR DWORD.
	return fmt.Sprintf("#%02x%02x%02x", color&0xff, (color>>8)&0xff, (color>>16)&0xff)
}

func (session *desktopSession) updateAccent() {
	if window := session.window.Load(); window != nil {
		window.ExecJS(fmt.Sprintf(`if (location.origin === %q && typeof applyWindowsAccent === "function") applyWindowsAccent(%q);`, session.origin, windowsAccent()))
	}
}

// run keeps the shared local server alive while the native window is hidden.
func (session *desktopSession) run(ctx context.Context, address string, icon []byte) error {
	app := session.app
	if len(icon) > 0 {
		app.SetIcon(icon)
	}
	desktopURL, err := url.Parse(address)
	if err != nil {
		return err
	}
	query := desktopURL.Query()
	query.Set("windows-accent", windowsAccent())
	desktopURL.RawQuery = query.Encode()
	session.origin = address
	app.Event.OnApplicationEvent(events.Windows.SystemThemeChanged, func(*application.ApplicationEvent) { session.updateAccent() })
	// Wails populates the DPI-adjusted screen work areas before this event.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		window := app.Window.NewWithOptions(desktopWindowOptions(desktopURL.String(), app.Screen.GetPrimary()))
		window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			e.Cancel()
			window.Hide()
		})
		session.window.Store(window)
		window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) { session.updateAccent() })
		window.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { session.updateAccent() })
	})
	tray := app.SystemTray.New()
	if len(icon) > 0 {
		tray.SetIcon(icon)
	}
	tray.SetTooltip("声记")
	menu := app.NewMenu()
	menu.Add("显示主窗口").OnClick(func(*application.Context) { session.showWindow() })
	menu.Add("在浏览器中打开").OnClick(func(*application.Context) { _ = app.Browser.OpenURL(address) })
	if buildinfo.UpdateRepository != "" {
		updateCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		menu.Add("检查更新 · v" + buildinfo.Version()).OnClick(func(*application.Context) {
			go session.checkForUpdate(updateCtx, tray, true)
		})
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			go session.watchUpdates(updateCtx, tray)
		})
	}
	menu.AddSeparator()
	menu.Add("退出声记").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	tray.OnClick(session.showWindow)
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
