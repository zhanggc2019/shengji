# 声记

声记支持录制、转录、回听和整理会议纪要。项目使用 Go 1.27.2、Gin 1.12.0、Wails 3.1.0、SQLite 和原生 HTML/CSS/JS。Windows 桌面窗口和浏览器使用同一个会议库、录音会话及任务队列。

## 启动与打包

开发环境运行桌面窗口

```powershell
go run .
```

仅在浏览器使用时运行 `go run -tags server .`。服务默认监听 `0.0.0.0:9011`，本机打开 `http://127.0.0.1:9011`，局域网设备通过此电脑的局域网 IPv4 地址和端口 `9011` 访问。可用 Windows 的 `ipconfig` 查看地址。桌面版也可通过托盘菜单「在浏览器中打开」。

重复启动同一会议库的新版桌面程序时，会唤醒已有窗口。升级时可通过托盘「退出声记」关闭旧版后再打开新版。默认端口已占用时自动使用可用端口，以启动输出或托盘打开的地址为准。显式传入 `--listen` 时使用指定端口。

局域网客户端可读写同一个会议库，并可控制服务主机的录音。只在本机使用时以 `--listen 127.0.0.1:9011` 启动。可用 `--data-dir <目录>` 指定库，默认目录为 `%LOCALAPPDATA%\OddMeta\OddMinutes Go`，升级与卸载保留该目录。工作台默认浅色，可通过右上角切换深色，选择保存在各自的浏览器或 WebView 配置中。

Windows 打包工具为 Wails 3.1.0 CLI 和 NSIS。离线构建另用 MinGW-w64 GCC，安装后的组件由安装包提供。

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
.\packaging\build.ps1 -Installer
.\packaging\build.ps1 -Installer -Offline
```

- `bin/Shengji-amd64-installer.exe` 安装桌面版，包含 WebView2 引导器。
- `bin/Shengji-amd64-offline-installer.exe` 另包含 Sherpa-ONNX 本机工作进程、原生库和许可文本。
- `bin/Shengji-server-amd64.exe` 是浏览器服务独立构建。
- Windows 桌面窗口需要 WebView2。录音、回听和常见音频文件导入无需 Python、Node.js 或 FFmpeg。安装包当前未签名。

## 录音、转录与纪要

Windows 版采集默认麦克风以及可选的默认输出设备回环。将音频按同一时钟混合为 16 kHz 单声道 PCM WAV，边录边写，启动时修复上次中断的录音。浏览器支持音视频导入、按转录时间跳转、发言人和文本编辑、全文搜索，以及 TXT、Markdown、JSON 和音频下载。新建或导入时可选「会议」或「纯转录」。纯转录保留文本、时间戳及回听，工作区和导出省略发言人标签。

「导入音视频」一次可选择最多 20 个文件，每个文件最多 500 MiB，各创建一条会议。多文件默认用文件名作为会议名称，失败项可单独重试。WAV、MP3、M4A、OGG、Opus、FLAC 与 WebM 音频直接入库。MP4、MOV、M4V、MKV、AVI 视频提取第一条音轨，保存为 16 kHz 单声道 PCM WAV 供回听和转录。

视频提取使用服务主机上的 FFmpeg。「导入音视频」会先检测，缺少 FFmpeg 时可展开「FFmpeg 安装指引」，音频仍可以先导入。配置完成后点击「重新检测」即可继续导入已选视频。[FFmpeg 官方下载](https://ffmpeg.org/download.html)

Windows 下载对应架构的静态版并解压，将 `bin\ffmpeg.exe` 放到声记程序所在目录。也可将 FFmpeg 的 `bin` 目录加入用户 `Path`，或设置 `SHENGJI_FFMPEG` 指向可执行文件，这两种方式需要重启声记服务。Linux 和 macOS 可使用对应平台的包管理器安装。

新建会议库默认选择本机 SenseVoice，左下角显示当前选用的转录服务和模型。安装本机组件并下载模型后，在「模型设置」填写模型包目录。已有库继续使用保存的配置。任务保存提交时的模型配置，重启后重新排队，失败时保留原有转录或纪要。

| 转录服务 | 模型设置 |
|---|---|
| OddASR | 默认 `http://127.0.0.1:9002`，模型 `whisper-1`，兼容 `verbose_json` 时间戳 |
| OpenAI 兼容 | 默认 `gpt-transcribe`。文本模型按其 JSON 能力展示，`whisper-1` 使用字词时间戳，`gpt-4o-transcribe-diarize` 使用说话人片段格式 |
| Groq | 默认 `whisper-large-v3-turbo`，请求片段和词级时间戳 |
| 本机转录（离线） | 粤语、日语或韩语录音可优先选 SenseVoice。以普通话、河南话、天津话或四川话为主的录音可优先选 Paraformer |

### 本机 SenseVoice 与 Paraformer

1. 安装离线版，或用 `-Offline` 构建本机组件。组件与依赖应位于主程序旁的 `sherpa` 目录。高级用法可设置 `ODDMINUTES_SHERPA_EXE` 指向组件。
2. 在「模型设置」选择「本机转录（离线）」，下拉选择模型，再通过界面下载。SenseVoice 的模型和词表由魔搭社区提供。Paraformer 的带时间戳原版压缩包由社区加速镜像提供。[SenseVoice 模型](https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo?Revision=860e3f03644fb4d979bfd93c4ef345c93c0feaf8&FilePath=model.int8.onnx) · [SenseVoice 词表](https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo?Revision=6652fb804909a130d070d533690dd578e983784d&FilePath=tokens.txt) · [Paraformer 模型包](https://gh-proxy.com/https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-paraformer-zh-2023-09-14.tar.bz2)
3. 在声记服务主机上，把 SenseVoice 的两个文件放到同一目录，或解压 Paraformer 压缩包。将包含 `model.int8.onnx` 和 `tokens.txt` 的文件夹填入「模型包目录」。
4. 导入 16 位单声道 PCM WAV，或使用应用录制的 WAV，点击「生成转录」。可将 [Silero VAD 模型](https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx) 放进同一模型目录，16 kHz 音频将按语音段识别。其余输入按有界音频窗口识别。

模型权重与安装包分开提供。这里提供的两个模型均可按字词定位回听，其他模型包若缺少词元时间戳，会使用段级回听。本地推理处理已保存的 WAV，当前两种本机模式的发言人均需手动标注。自动区分说话人需要独立的说话人模型。[Sherpa-ONNX 说话人分离](https://k2-fsa.github.io/sherpa/onnx/speaker-diarization/index.html)

转录页和 TXT、Markdown、JSON 导出包含生成当前文本的模型、音频时长和转录耗时。转录耗时从开始调用转录服务到收到结果。重新转录成功后更新来源，失败时继续保留原结果及其来源。在「最近转录记录」可以对比同一音频的模型和耗时。

AI 纪要支持 Ollama、OpenAI 兼容、Claude、DeepSeek、智谱和硅基流动。对后三个预设，在运行声记的主机配置对应 API Key 并重启服务。然后在「模型设置」选择厂商和模型即可。

| 摘要服务 | API Key 环境变量 | 文档 |
|---|---|---|
| DeepSeek | `ODDMINUTES_DEEPSEEK_API_KEY` | [模型与接口](https://api-docs.deepseek.com/quick_start/pricing/) |
| 智谱 | `ODDMINUTES_ZHIPU_API_KEY` | [快速开始](https://docs.bigmodel.cn/cn/guide/start/quick-start) |
| 硅基流动 | `ODDMINUTES_SILICONFLOW_API_KEY` | [快速上手](https://docs.siliconflow.cn/docs/userguide/quickstart) |

自定义摘要服务继续使用 `ODDMINUTES_LLM_API_KEY`。转录服务使用 `ODDMINUTES_ASR_API_KEY`（OddASR）、`ODDMINUTES_ASR_OPENAI_KEY` 或 `ODDMINUTES_ASR_GROQ_KEY`。

## 测试

```powershell
go test ./...
go vet ./...
go test -race ./internal/minutes
go test -tags wasapi_integration -coverprofile coverage.out ./internal/minutes
```

WASAPI 实机测试需要默认麦克风与播放设备，会播放短测试音。浏览器验收使用 Microsoft Edge 和装有 Playwright 的测试虚拟环境，模型服务由本机桩模拟。真实离线验收可设置 `ODDMINUTES_OFFLINE_WORKER`、`ODDMINUTES_SENSEVOICE_DIR`、`ODDMINUTES_SENSEVOICE_WAV`、`ODDMINUTES_PARAFORMER_DIR`、`ODDMINUTES_PARAFORMER_WAV`，再运行 `go test -tags sherpa_integration ./internal/minutes`。

```powershell
uv venv .venv
uv pip install --python .\.venv\Scripts\python.exe playwright
.\.venv\Scripts\python.exe .\tests\browser_smoke.py .\bin\Shengji-server-amd64.exe
```

服务端可以调用 FFmpeg 时，给浏览器验收命令加上 `--video internal/minutes/testdata/video-with-audio.mp4`，可同时验证混合导入、视频回听和转录。

## 架构参考

- Gin 按 `/api` 分组注册 HTTP 接口，服务端用标准库 `http.Server` 管理超时与退出。代理信任关闭，浏览器 API 和 WebSocket 继续校验跨站请求。可在 PowerShell 设置 `$env:GIN_MODE = 'debug'` 查看路由调试输出。 [Gin 路由分组](https://gin-gonic.com/en/docs/routing/grouping-routes/) · [可信代理](https://gin-gonic.com/en/docs/server-config/trusted-proxies/) · [平滑退出](https://gin-gonic.com/en/docs/server-config/graceful-restart-or-stop/)
- 桌面窗口与托盘生命周期参考 Wails 官方示例，安装包复用官方生成的资源和 WebView2 宏。桌面和浏览器共用 HTTP API 与同源 WebSocket。 [Wails 托盘示例](https://github.com/wailsapp/wails/tree/master/v3/examples/systray-menu) · [Server Build](https://wails.io/guides/server-build/) · [Windows 打包](https://v3.wails.io/guides/build/windows/)
- Windows 采集通过开源 `go-wca` 使用 WASAPI，按微软文档对齐 QPC 时间并在读取后释放缓冲。浏览器订阅使用 `coder/websocket` 的同源校验、连接取消和写入超时。 [go-wca](https://github.com/moutend/go-wca) · [微软 WASAPI 缓冲规范](https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nf-audioclient-iaudiocaptureclient-getbuffer) · [coder/websocket](https://github.com/coder/websocket)
- 本机 ASR 使用 Sherpa-ONNX 官方 Go API 与 VAD 示例。应用与模型分开分发，参考 Handy 的模型部署方式。 [Sherpa-ONNX Go API](https://github.com/k2-fsa/sherpa/blob/master/docs/source/onnx/go-api/index.rst) · [官方 VAD 文件识别示例](https://k2-fsa.github.io/sherpa/onnx/tauri/vad-asr-file.html) · [Handy 模型管理](https://github.com/cjpais/Handy/blob/main/src-tauri/src/managers/model.rs)
