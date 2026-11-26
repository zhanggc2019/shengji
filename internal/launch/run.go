package launch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhanggc2019/shengji/internal/configenv"
	"github.com/zhanggc2019/shengji/internal/minutes"
	"github.com/zhanggc2019/shengji/internal/webui"
)

// Desktop opens the application window until the user exits.
type Desktop func(context.Context, string, []byte) error

// Options selects the command's listener and optional desktop lifecycle.
type Options struct {
	Listen     string
	NewDesktop func(string) (Desktop, error)
	ShowError  func(error)
	WrapHTTP   func(app *minutes.App, dataDir string, browser http.Handler, allowLAN bool) (http.Handler, error)
}

// Main starts the shared application and reports startup failures.
func Main(options Options) {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	if err := run(options); err != nil {
		log.Print(err)
		if options.ShowError != nil {
			options.ShowError(err)
		}
		os.Exit(1)
	}
}

// dataDir returns the default library location shared by Shengji Desktop and Server.
func dataDir() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "Shengji"), nil
}

// run starts the shared HTTP service and shuts it down after the window or process exits.
func run(options Options) error {
	listen := flag.String("listen", options.Listen, "HTTP 监听地址")
	root := flag.String("data-dir", "", "会议数据库及音频目录")
	browser := flag.Bool("browser-only", false, "仅运行浏览器服务")
	flag.Parse()
	if *root == "" {
		var err error
		*root, err = dataDir()
		if err != nil {
			return err
		}
	}
	var ui Desktop
	if !*browser && options.NewDesktop != nil {
		var err error
		ui, err = options.NewDesktop(*root)
		if err != nil {
			return err
		}
	}
	explicitListen := false
	flag.Visit(func(option *flag.Flag) {
		if option.Name == "listen" {
			explicitListen = true
		}
	})
	conn, err := listenService(*listen, !explicitListen)
	if err != nil {
		return err
	}
	defer conn.Close()
	bound := conn.Addr().(*net.TCPAddr)
	ip := bound.AddrPort().Addr().Unmap()
	if !ip.IsLoopback() && !ip.IsUnspecified() && !minutes.IsLANAddress(ip) {
		return errors.New("服务地址须使用回环地址或局域网地址")
	}
	app, err := minutes.Open(*root, minutes.WindowsCapture, &minutes.HTTPAI{
		ASRKeys:   map[string]string{"openai": configenv.Get("SHENGJI_ASR_OPENAI_KEY"), "groq": configenv.Get("SHENGJI_ASR_GROQ_KEY")},
		NativeASR: configenv.Get("SHENGJI_SHERPA_EXE"),
		LLMKey:    configenv.Get("SHENGJI_LLM_API_KEY"),
	})
	if err != nil {
		return err
	}
	defer app.Close()
	web := webui.Files()
	icon, _ := fs.ReadFile(web, "favicon.ico")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	allowLAN := !ip.IsLoopback()
	handler := app.Handler(web, allowLAN)
	if options.WrapHTTP != nil {
		handler, err = options.WrapHTTP(app, *root, handler, allowLAN)
		if err != nil {
			return err
		}
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	defer server.Close()
	failed := make(chan error, 1)
	go func() {
		if err := server.Serve(conn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
			cancel()
		}
	}()
	address := browserURL(bound)
	fmt.Println(address)
	if ip.IsUnspecified() {
		fmt.Printf("局域网访问使用本机的局域网 IP 和端口 %d\n", bound.Port)
	}
	if ui == nil {
		<-ctx.Done()
	} else if err = ui(ctx, address, icon); err != nil {
		return err
	}
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
	select {
	case err = <-failed:
		return err
	default:
		return nil
	}
}

// listenService keeps an explicitly selected port and uses an available port when the default is occupied.
func listenService(address string, allowFallback bool) (net.Listener, error) {
	host, port, splitErr := net.SplitHostPort(address)
	network := "tcp"
	if host == "0.0.0.0" {
		network = "tcp4"
	}
	listener, err := net.Listen(network, address)
	if err == nil {
		return listener, nil
	}
	const wsaAddrInUse = syscall.Errno(10048)
	occupied := errors.Is(err, syscall.EADDRINUSE) || runtime.GOOS == "windows" && errors.Is(err, wsaAddrInUse)
	if occupied && splitErr == nil {
		if !allowFallback {
			return nil, fmt.Errorf("端口 %s 已被占用，请退出占用它的程序，或通过 --listen 换一个端口", port)
		}
		listener, err = net.Listen(network, net.JoinHostPort(host, "0"))
		if err == nil {
			return listener, nil
		}
	}
	return nil, fmt.Errorf("无法启动本地服务 %s: %w", address, err)
}

// browserURL gives Wails a concrete destination and preserves the loopback origin on default startup.
func browserURL(bound *net.TCPAddr) string {
	target := *bound
	if target.IP.IsUnspecified() {
		target.IP = net.IPv6loopback
		if bound.IP.To4() != nil {
			target.IP = net.IPv4(127, 0, 0, 1)
		}
	}
	return (&url.URL{Scheme: "http", Host: target.String()}).String()
}
