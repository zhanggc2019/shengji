# 声记

声记支持录制、转录、回听和整理会议纪要。项目使用 Go 1.27.2、Gin 1.12.0、Wails 3.1.0、SQLite 和原生 HTML/CSS/JS。Windows 桌面窗口和浏览器使用同一个会议库、录音会话及任务队列。

## 启动与打包

开发环境运行桌面窗口

```powershell
go run ./cmd/shengji
```

仅在浏览器或 Agent 中使用时运行 `go run ./cmd/shengji-server`。Server 版默认监听 `127.0.0.1:9011`，本机打开 `http://127.0.0.1:9011`。如需向局域网开放工作台、ASR API 和 MCP，传入 `--listen 0.0.0.0:9011`。桌面版默认监听 `0.0.0.0:9011`，可通过托盘菜单「在浏览器中打开」。

重复启动同一会议库的桌面程序时，会唤醒已有窗口。默认端口已占用时自动使用可用端口，以启动输出或托盘打开的地址为准。显式传入 `--listen` 时使用指定端口。

局域网客户端可读写同一个会议库，并可控制服务主机的录音。只在本机使用时以 `--listen 127.0.0.1:9011` 启动。可用 `--data-dir <目录>` 指定库，默认目录为 `%LOCALAPPDATA%\OddMeta\OddMinutes Go`，升级与卸载保留该目录。Windows 桌面工作台默认跟随系统外观和强调色，浏览器默认浅色并使用松石绿回退色。可通过右上角切换浅色或深色，选择保存在各自的浏览器或 WebView 配置中。

Windows 打包工具为 Wails 3.1.0 CLI 和 NSIS。离线构建另用 MinGW-w64 GCC，安装后的组件由安装包提供。

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
.\packaging\build.ps1 -Installer
.\packaging\build.ps1 -Installer -Offline
```

- `bin/Shengji-amd64-installer.exe` 安装桌面版，包含 WebView2 引导器。
- `bin/Shengji-amd64-offline-installer.exe` 另包含 Sherpa-ONNX 本机工作进程、原生库和许可文本。
- `bin/Shengji-arm64-installer.exe` 安装 Windows ARM64 桌面版，可用 `-Installer -Architecture arm64` 构建。
- `bin/Shengji-server-amd64.exe` 是浏览器服务独立构建。
- Windows 桌面窗口需要 WebView2。录音、回听和常见音频文件导入无需 Python、Node.js 或 FFmpeg。安装包当前未签名。

### GitHub 发布与桌面更新

GitHub Actions 在 PR 和推送到 `main` 时运行 Go 测试和静态检查。只有推送 `vMAJOR.MINOR.PATCH` 格式的版本标签（如 `v0.0.2`）才会触发打包和发布。检查通过后，工作流从标签读取版本号，构建三种 Windows 安装包并发布到 [GitHub Releases](https://github.com/zhanggc2019/shengji/releases)。本地打包可以用 `-Version 0.0.2` 指定版本，用 `-UpdateRepository owner/repo` 指定公开发布仓库。

发布时，在要发布的提交上创建标签，例如 `git tag v0.0.2`，然后执行 `git push origin v0.0.2`。

安装版启动后会检查更新，持续运行时每 6 小时重新检查。托盘菜单的「检查更新」显示当前版本，也可以手动查询。更新会选择相同架构和安装类型的包，下载后核对文件大小和 GitHub 提供的 SHA-256。确认「退出并安装」后，应用会保存正在录制的音频并退出，安装程序等旧进程结束再替换文件。工作台中尚未保存的文字请先保存。离线版检查和下载更新时需要连接 GitHub。源码开发版和独立 Server 版可通过重新构建或下载新版本更新。

## 录音、转录与纪要

Windows 版采集默认麦克风以及可选的默认输出设备回环。将音频按同一时钟混合为 16 kHz 单声道 PCM WAV，边录边写，启动时修复上次中断的录音。浏览器支持音视频导入、按转录时间跳转、发言人和文本编辑、全文搜索，以及 TXT、Markdown、JSON 和音频下载。新建或导入时可选「会议」或「纯转录」。纯转录保留文本、时间戳及回听，工作区和导出省略发言人标签。

「导入音视频」一次可选择最多 20 个文件，每个文件最多 500 MiB，各创建一条会议。多文件默认用文件名作为会议名称，失败项可单独重试。WAV、MP3、M4A、OGG、Opus、FLAC 与 WebM 音频直接入库。MP4、MOV、M4V、MKV、AVI 视频提取第一条音轨，保存为 16 kHz 单声道 PCM WAV 供回听和转录。

视频提取使用服务主机上的 FFmpeg。「导入音视频」会先检测，缺少 FFmpeg 时可展开「FFmpeg 安装指引」，音频仍可以先导入。配置完成后点击「重新检测」即可继续导入已选视频。[FFmpeg 官方下载](https://ffmpeg.org/download.html)

Windows 下载对应架构的静态版并解压，将 `bin\ffmpeg.exe` 放到声记程序所在目录。也可将 FFmpeg 的 `bin` 目录加入用户 `Path`，或设置 `SHENGJI_FFMPEG` 指向可执行文件，这两种方式需要重启声记服务。Linux 和 macOS 可使用对应平台的包管理器安装。

新建会议库默认选择本机 SenseVoice，左下角显示当前选用的转录服务和模型。安装本机组件并下载模型后，在「模型设置」填写模型包目录。已有库继续使用受支持的配置。旧 OddASR 设置会回退到本机 SenseVoice，历史转录及其模型来源仍可查看。已排队的任务保留提交时的模型配置，引用已移除服务的转录任务需重新提交。生成失败时保留已有转录或纪要。

| 转录服务 | 模型设置 |
|---|---|
| OpenAI 兼容 | 默认 `gpt-transcribe`。文本模型按其 JSON 能力展示，`whisper-1` 使用字词时间戳，`gpt-4o-transcribe-diarize` 使用说话人片段格式 |
| Groq | 默认 `whisper-large-v3-turbo`，请求片段和词级时间戳 |
| 本机转录（离线） | 粤语、日语或韩语录音可优先选 SenseVoice。以普通话、河南话、天津话或四川话为主的录音可优先选 Paraformer |

### 本机 SenseVoice 与 Paraformer

1. 安装离线版，或用 `-Offline` 构建本机组件。组件与依赖应位于主程序旁的 `sherpa` 目录。高级用法可设置 `SHENGJI_SHERPA_EXE` 指向组件。
2. 在「模型设置」选择「本机转录（离线）」，下拉选择模型，再通过界面下载。SenseVoice 的模型和词表由魔搭社区提供。Paraformer 的带时间戳原版压缩包由社区加速镜像提供。[SenseVoice 模型](https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo?Revision=860e3f03644fb4d979bfd93c4ef345c93c0feaf8&FilePath=model.int8.onnx) · [SenseVoice 词表](https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo?Revision=6652fb804909a130d070d533690dd578e983784d&FilePath=tokens.txt) · [Paraformer 模型包](https://gh-proxy.com/https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-paraformer-zh-2023-09-14.tar.bz2)
3. 在声记服务主机上，把 SenseVoice 的两个文件放到同一目录，或解压 Paraformer 压缩包。将包含 `model.int8.onnx` 和 `tokens.txt` 的文件夹填入「模型包目录」。
4. 导入 16 位单声道 PCM WAV，或使用应用录制的 WAV，点击「生成转录」。可将 [Silero VAD 模型](https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx) 放进同一模型目录，16 kHz 音频将按语音段识别。其余输入按有界音频窗口识别。

模型权重与安装包分开提供。这里提供的两个模型均可按字词定位回听，其他模型包若缺少词元时间戳，会使用段级回听。本地推理处理已保存的 WAV，当前两种本机模式的发言人均需手动标注。自动区分说话人需要独立的说话人模型。[Sherpa-ONNX 说话人分离](https://k2-fsa.github.io/sherpa/onnx/speaker-diarization/index.html)

转录页和 TXT、Markdown、JSON 导出包含生成当前文本的模型、音频时长和转录耗时。转录耗时从开始调用转录服务到收到结果。重新转录成功后更新来源，失败时继续保留原结果及其来源。在「最近转录记录」可以对比同一音频的模型和耗时。

## ASR 接入 Agent

用 `go run ./cmd/shengji-server` 或 `Shengji-server-amd64.exe` 启动服务，按启动时输出的地址连接，默认是 `http://127.0.0.1:9011`。在工作台「模型设置」先配置所需的 ASR 模型。浏览器、HTTP ASR 和 MCP 共用该配置。新库默认使用本机 SenseVoice，推理需要离线工作进程和模型包。如果选择了云端 ASR，识别会把提交的音频发送到所配置的服务。

### HTTP API

用 `GET /api/asr/status` 查询活动模型、可接收格式、文件大小上限和 MCP 音频目录。用 `POST /api/asr/transcriptions` 上传音频，第一项 Multipart 文件字段名为 `file`。语言可通过查询参数传入，例如 `language=zh`，省略或使用 `auto` 会使用自动识别。

```powershell
curl.exe http://127.0.0.1:9011/api/asr/status
curl.exe -F "file=@D:\Audio\meeting.wav" "http://127.0.0.1:9011/api/asr/transcriptions?language=zh"
```

成功响应包含 `text`、`segments`、`source`、`elapsed_ms`，标准 PCM WAV 还会返回可读取的 `audio_seconds`。起止时间以秒计，字词时间戳和发言人取决于所用模型的输出能力。接口同步返回，文件上限为 500 MiB。模型识别较慢时，请按音频长度设置客户端超时。

### MCP

在 Agent 客户端添加一个 **Streamable HTTP** MCP 服务，URL 填 `http://127.0.0.1:9011/mcp`。

1. 调用 `asr_info` 获取 `input_dir`、模型和格式。
2. 把音频放进该目录。默认是会议库下的 `asr-input`，可在 Server 启动时用 `--asr-input-dir "D:\Audio\inbox"` 指定其他目录。
3. 调用 `asr_transcribe`，参数例如 `{"path":"meeting.wav","language":"zh"}`。`path` 使用相对路径，可指向目录中的子文件。任意位置的文件也可直接走 HTTP 上传，无需放入 MCP 目录。

Server 使用 `--listen 0.0.0.0:9011` 或明确的局域网地址启动时，两种 Agent 接口都可通过服务主机的局域网 IP 使用。MCP 的 `path` 指向服务主机上的文件。客户端上的文件可以直接通过 HTTP 上传。每次调用直接返回转录结果，会议库保持独立。默认本机模型接受 16 位单声道 PCM WAV。其他后端的输入能力以模型服务为准。

## AI 纪要

AI 纪要支持 Ollama、OpenAI 兼容、Claude、DeepSeek、智谱和硅基流动。在「模型设置」选择服务和模型，填写「摘要 API Key」后保存即可使用。云端转录的密钥填在同一窗口的「转录 API Key」。本机 Sherpa 无需密钥，本机 Ollama 通常也可留空。

| 摘要服务 | 文档 |
|---|---|
| DeepSeek | [模型与接口](https://api-docs.deepseek.com/quick_start/pricing/) |
| 智谱 | [快速开始](https://docs.bigmodel.cn/cn/guide/start/quick-start) |
| 硅基流动 | [快速上手](https://docs.siliconflow.cn/docs/userguide/quickstart) |

密钥按服务和接口地址加密保存。重新打开设置时会显示保存状态，留空可保留原密钥。密钥可以在桌面端或同一台电脑的本机工作台填写、替换或清除。Windows 将密钥绑定到运行声记的用户账户，迁移会议库后如遇到解密提示，请重新填写密钥。

已部署的环境变量配置仍可作为备用。DeepSeek、智谱、硅基流动分别对应 `SHENGJI_DEEPSEEK_API_KEY`、`SHENGJI_ZHIPU_API_KEY`、`SHENGJI_SILICONFLOW_API_KEY`。其他摘要服务对应 `SHENGJI_LLM_API_KEY`。云端转录对应 `SHENGJI_ASR_OPENAI_KEY` 或 `SHENGJI_ASR_GROQ_KEY`。声记仍兼容同名的旧前缀 `ODDMINUTES_`。

## 测试

```powershell
go test ./...
go vet ./...
go test -race ./internal/minutes
go test -tags wasapi_integration -coverprofile coverage.out ./internal/minutes
```

WASAPI 实机测试需要默认麦克风与播放设备，会播放短测试音。真实离线验收可设置 `SHENGJI_OFFLINE_WORKER`、`SHENGJI_SENSEVOICE_DIR`、`SHENGJI_SENSEVOICE_WAV`、`SHENGJI_PARAFORMER_DIR`、`SHENGJI_PARAFORMER_WAV`，再运行 `go test -tags sherpa_integration ./internal/minutes`。

浏览器验收位于独立的 Go 测试模块 `tests/browser`，使用本机 Microsoft Edge。首次运行会下载 Playwright 驱动，模型服务由 Go 测试桩模拟。

```powershell
go -C tests/browser test -count=1 -args -binary ..\..\bin\Shengji-server-amd64.exe
go build -tags desktoptest -o .\bin\Shengji-desktop-test.exe ./cmd/shengji
go -C tests/browser test -count=1 -args -binary ..\..\bin\Shengji-desktop-test.exe -desktop
```

服务端可以调用 FFmpeg 时，给验收命令加上 `-video ../../internal/minutes/testdata/video-with-audio.mp4`，可同时验证混合导入、视频回听和转录。

## 架构参考

- `cmd/shengji` 是 Windows 桌面入口，`cmd/shengji-server` 是浏览器与 Agent 服务入口，`cmd/sherpa-asr` 是可选的本机 ASR 工作进程。
- `internal/launch` 管理启动、监听和关闭，`internal/minutes` 管理会议库、录音与模型，`internal/agentapi` 提供 HTTP ASR 与 MCP，`internal/webui/static` 是两端共用的嵌入前端。
- Gin 按 `/api` 分组注册 HTTP 接口，服务端用标准库 `http.Server` 管理超时与退出。代理信任关闭，浏览器 API 和 WebSocket 继续校验跨站请求。可在 PowerShell 设置 `$env:GIN_MODE = 'debug'` 查看路由调试输出。 [Gin 路由分组](https://gin-gonic.com/en/docs/routing/grouping-routes/) · [可信代理](https://gin-gonic.com/en/docs/server-config/trusted-proxies/) · [平滑退出](https://gin-gonic.com/en/docs/server-config/graceful-restart-or-stop/)
- 桌面窗口与托盘生命周期参考 Wails 官方示例，安装包复用官方生成的资源和 WebView2 宏。桌面和浏览器共用 HTTP API 与同源 WebSocket。 [Wails 托盘示例](https://github.com/wailsapp/wails/tree/master/v3/examples/systray-menu) · [Server Build](https://wails.io/guides/server-build/) · [Windows 打包](https://v3.wails.io/guides/build/windows/)
- Windows 采集通过开源 `go-wca` 使用 WASAPI，按微软文档对齐 QPC 时间并在读取后释放缓冲。浏览器订阅使用 `coder/websocket` 的同源校验、连接取消和写入超时。 [go-wca](https://github.com/moutend/go-wca) · [微软 WASAPI 缓冲规范](https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nf-audioclient-iaudiocaptureclient-getbuffer) · [coder/websocket](https://github.com/coder/websocket)
- 本机 ASR 使用 Sherpa-ONNX 官方 Go API 与 VAD 示例。应用与模型分开分发，参考 Handy 的模型部署方式。 [Sherpa-ONNX Go API](https://github.com/k2-fsa/sherpa/blob/master/docs/source/onnx/go-api/index.rst) · [官方 VAD 文件识别示例](https://k2-fsa.github.io/sherpa/onnx/tauri/vad-asr-file.html) · [Handy 模型管理](https://github.com/cjpais/Handy/blob/main/src-tauri/src/managers/model.rs)
