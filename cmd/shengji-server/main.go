package main

import (
	"flag"
	"net/http"
	"path/filepath"

	"github.com/zhanggc2019/shengji/internal/agentapi"
	"github.com/zhanggc2019/shengji/internal/launch"
	"github.com/zhanggc2019/shengji/internal/minutes"
)

// main starts the browser service on the loopback interface by default.
func main() {
	inputDir := flag.String("asr-input-dir", "", "MCP 可读取的音频目录，默认使用会议库下的 asr-input")
	launch.Main(launch.Options{
		Listen: "127.0.0.1:9011",
		WrapHTTP: func(app *minutes.App, dataDir string, browser http.Handler, allowLAN bool) (http.Handler, error) {
			path := *inputDir
			if path == "" {
				path = filepath.Join(dataDir, "asr-input")
			}
			return agentapi.WithRoutes(app, path, browser, allowLAN)
		},
	})
}
