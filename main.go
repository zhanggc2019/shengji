package main

import (
	"context"
	"embed"
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
	"github.com/zhanggc2019/shengji/internal/minutes"
)

//go:embed web
var content embed.FS

const defaultListen = "0.0.0.0:9011"

// main starts the application, including browser-only and native desktop modes.
func main() {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	if err := run(); err != nil {
		log.Print(err)
		startupError(err)
		os.Exit(1)
	}
}

// dataDir locates the new library independently of any installed Python edition.
func dataDir() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "OddMeta", "OddMinutes Go"), nil
}

// run starts the shared HTTP service and shuts it down after the window or process exits.
func run() error {
	listen := flag.String("listen", defaultListen, "HTTP 监听地址")
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
	var ui *desktopSession
	if !*browser {
		var err error
		ui, err = newDesktopSession(*root)
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
		ASRKey:    os.Getenv("ODDMINUTES_ASR_API_KEY"),
		ASRKeys:   map[string]string{"openai": os.Getenv("ODDMINUTES_ASR_OPENAI_KEY"), "groq": os.Getenv("ODDMINUTES_ASR_GROQ_KEY")},
		NativeASR: os.Getenv("ODDMINUTES_SHERPA_EXE"),
		LLMKey:    os.Getenv("ODDMINUTES_LLM_API_KEY"),
	})
	if err != nil {
		return err
	}
	defer app.Close()
	web, err := fs.Sub(content, "web")
	if err != nil {
		return err
	}
	icon, _ := fs.ReadFile(web, "favicon.ico")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: app.Handler(web, !ip.IsLoopback()), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
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
	if *browser {
		<-ctx.Done()
	} else if err = ui.run(ctx, address, icon); err != nil {
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
