# 声记

声记是一款 Windows 会议录音和转录工具。可以录制麦克风和系统声音，也可以导入已有音视频。转录后可按时间戳回听、修改文本、整理 AI 纪要和导出记录。

## 下载与升级

在 [GitHub Releases](https://github.com/zhanggc2019/shengji/releases/latest) 下载适合你电脑的安装包。

| 版本 | 安装包 | 适用场景 |
| --- | --- | --- |
| Windows x64 普通版 | `Shengji-amd64-installer.exe` | Intel 或 AMD x64 电脑。适合录音、回听和使用在线转录。 |
| Windows x64 离线版 | `Shengji-amd64-offline-installer.exe` | Intel 或 AMD x64 电脑。额外内置 Sherpa-ONNX，本机转录需要另行下载模型文件。 |
| Windows ARM64 版 | `Shengji-arm64-installer.exe` | Windows on ARM 电脑。适用范围与普通版相同。 |

两种 x64 安装包的界面和录音功能相同。想用 SenseVoice 或 Paraformer 在电脑上转录，直接选离线版。Windows 桌面端需要 WebView2，安装包带有运行时引导器，目前安装包未签名。

### 怎么升级

- **自动检查。** 安装版启动后会检查更新，运行期间每 6 小时再检查一次。也可以右键点击系统托盘里的声记图标，选择「检查更新」。检查和下载安装包需要连接 GitHub。
- **同类型升级。** 普通版只查找普通版安装包，离线版只查找离线版安装包，同时匹配 CPU 架构。对应安装包缺失时会提示。确认下载后会校验大小和 SHA-256，再由你选择是否「退出并安装」。退出时会保存正在录制的音频，后台任务会在下次启动时继续。
- **手动换装。** 从托盘退出声记，再用另一种安装包覆盖同一安装目录。例如用离线版覆盖普通版，之后就按离线版检查更新。

Windows 默认会议库位于 `%LOCALAPPDATA%\Shengji`，也可通过 `--data-dir` 指定其他目录。升级、换装和卸载会保留会议库。安装前请先保存工作台里正在编辑的文字。

旧版声记使用的会议库仍在原处。在 `Shengji.exe` 所在目录打开 PowerShell，可以显式打开旧库：

```powershell
.\Shengji.exe --data-dir "$env:LOCALAPPDATA\OddMeta\OddMinutes Go"
```

如果新默认目录尚未创建，也可以先退出使用旧库的声记，再把旧会议库整个目录复制为 `%LOCALAPPDATA%\Shengji`。新版启动时会打开这份副本。

## 快速上手

1. **录音或导入。** 点「新建记录」后开始录音，或点「导入音视频」选择已有文件。录音使用默认麦克风，可在「录音选项」中选择是否同时录制系统声音。如只需要转录文本，创建时选「纯转录」，可保留时间戳和回听。
2. **生成转录。** 在「模型设置 → 转录」配置本机模型或在线服务，回到会议点击「生成转录」。点击转录中的时间戳即可回听对应位置，滚动正文时播放器会留在顶部。
3. **整理与导出。** 会议模式支持修改转录和发言人、生成 AI 纪要或补写笔记。导出菜单按现有内容提供音频、Markdown、TXT 和 JSON。侧栏可搜索会议标题和转录内容。

录音意外中断时，声记会在下次启动时尝试恢复已保存的音频。

### 导入格式

每次最多导入 20 个文件，单文件上限为 500 MiB。多文件导入默认以文件名命名，失败项可单独重试。

| 类型 | 支持格式 | 处理方式 |
| --- | --- | --- |
| 音频 | WAV、MP3、M4A、OGG、Opus、FLAC、WebM | 直接入库。转录格式取决于选用的模型。 |
| 视频 | MP4、MOV、M4V、MKV、AVI | 用 FFmpeg 提取第一条音轨，保存为 16 kHz 单声道 PCM WAV。 |

录音、回听和音频导入可直接使用。导入视频时，如提示缺少 FFmpeg，可按弹窗里的「FFmpeg 安装指引」配置，再点「重新检测」。Windows 可将 `ffmpeg.exe` 放在声记程序旁，也可加入用户 `Path` 或设置 `SHENGJI_FFMPEG`。改动 `Path` 或环境变量后需要重启声记。[FFmpeg 官方下载](https://ffmpeg.org/download.html)

## 配置转录和 AI 纪要

### 本机转录

新建会议库默认选择本机 SenseVoice。离线版装好后，按这三步配置模型：

1. 打开「模型设置 → 转录」，选择「本机转录（离线）」和所需模型。粤语、日语或韩语优先试 SenseVoice。普通话、河南话、天津话或四川话可优先试 Paraformer。
2. 展开「下载与安装指引」。将 SenseVoice 的模型与词表放进同一个目录，或解压 Paraformer 模型包。把包含 `model.int8.onnx` 和 `tokens.txt` 的目录填入「模型包目录」并保存。
3. 使用声记录下的音频，或导入 16 位单声道 PCM WAV，再点击「生成转录」。这两种模型支持按字词定位回听，发言人需要手动标注。

需要按语音段切分时，可把 Silero VAD 模型放进同一目录，用于 16 kHz 音频。[Silero VAD 模型](https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx)

### 在线转录

在「模型设置 → 转录」选择服务并填写所需的地址、模型和「转录 API Key」。使用在线服务时，声记会将音频发送给你配置的服务。

| 服务 | 默认模型 | 时间戳与发言人 |
| --- | --- | --- |
| OpenAI 兼容 | `gpt-transcribe` | `whisper-1` 可返回字词时间戳，`gpt-4o-transcribe-diarize` 可返回带发言人的片段。 |
| Groq | `whisper-large-v3-turbo` | 请求片段和字词时间戳。 |

转录完成后，在「转录详情」可以查看模型、音频时长和转录耗时。Markdown、TXT 和 JSON 导出也会包含已记录的这些信息。重新转录失败时会保留原结果。

### AI 纪要

先生成会议转录，再到「模型设置 → AI 纪要」选择 Ollama、OpenAI 兼容、Claude、DeepSeek、智谱或硅基流动。云端服务使用「摘要 API Key」，本机 Ollama 通常可留空。配置完成后，在会议的「AI 纪要」标签页点击「生成纪要」。

密钥按服务和接口地址加密保存，可在桌面端或服务主机的本机工作台管理。修改设置时留空可保留已保存的密钥。Windows 密钥绑定当前用户，迁移会议库后如遇到解密提示，可重新填写。

<details>
<summary>环境变量和旧配置</summary>

- 本机转录组件默认位于主程序旁的 `sherpa` 目录，也可用 `SHENGJI_SHERPA_EXE` 指向工作进程。
- 在线转录可使用 `SHENGJI_ASR_OPENAI_KEY` 或 `SHENGJI_ASR_GROQ_KEY`。摘要服务可使用 `SHENGJI_LLM_API_KEY`，DeepSeek、智谱和硅基流动分别使用 `SHENGJI_DEEPSEEK_API_KEY`、`SHENGJI_ZHIPU_API_KEY` 和 `SHENGJI_SILICONFLOW_API_KEY`。兼容旧的 `ODDMINUTES_` 前缀。
- 旧 OddASR 设置会回退到本机 SenseVoice，历史转录和模型来源仍可查看。排队任务保留提交当时的配置，引用旧服务的转录任务需要重新提交。

</details>

## 浏览器、Server 与 Agent 接入

桌面版可通过托盘菜单「在浏览器中打开」。桌面窗口和连接到它的浏览器共用会议库、录音会话及任务队列。桌面版默认监听 `0.0.0.0:9011`，只供本机使用时可传入 `--listen 127.0.0.1:9011`。

需要独立浏览器服务、HTTP ASR 或 MCP 时，运行 `Shengji-server-amd64.exe` 或在源码目录执行以下命令：

```powershell
go run ./cmd/shengji-server
```

Server 默认监听 `127.0.0.1:9011`。需要局域网访问时可传入 `--listen 0.0.0.0:9011`，然后连接服务主机的局域网 IP。连接到同一工作台的客户端可以读写会议库和控制主机录音。默认端口被占用时，使用启动输出或托盘所显示的地址。

### HTTP ASR

先在浏览器工作台配置转录模型。用 `GET /api/asr/status` 查询模型、可接收格式、大小上限和 MCP 输入目录。用 `POST /api/asr/transcriptions` 上传音频，Multipart 文件字段为 `file`，语言可用 `language=zh` 指定，省略时自动识别。

```powershell
curl.exe http://127.0.0.1:9011/api/asr/status
curl.exe -F "file=@D:\Audio\meeting.wav" "http://127.0.0.1:9011/api/asr/transcriptions?language=zh"
```

接口同步返回 `text`、`segments`、`source` 和 `elapsed_ms`。可读取时长的 PCM WAV 还会返回 `audio_seconds`。片段时间以秒计，文件上限为 500 MiB。本机模型使用 16 位单声道 PCM WAV。长录音应按音频长度配置客户端超时。

### MCP

在 Agent 中添加 **Streamable HTTP** MCP 服务，地址填 `http://127.0.0.1:9011/mcp`。

1. 调用 `asr_info` 获取模型、格式和输入目录 `input_dir`。
2. 把音频放到服务主机的输入目录。默认是会议库下的 `asr-input`，可用 `--asr-input-dir "D:\Audio\inbox"` 自定义。
3. 调用 `asr_transcribe`，传入相对于输入目录的路径。例如 `{"path":"meeting.wav","language":"zh"}`。

HTTP ASR 和 MCP 使用同一套转录配置，结果直接返回给调用方。浏览器工作台的会议记录通过新建或导入创建。

## 开发、测试与发布

项目使用 Go 1.27.2、Gin、Wails、SQLite 和原生 HTML/CSS/JS。在 Windows 上用 `go run ./cmd/shengji` 启动桌面开发版。常规检查命令如下。

```powershell
go test ./...
go vet ./...
```

### 打包与发布

安装包构建需要 Wails CLI 和 NSIS。离线版还需要 MinGW-w64 GCC。

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
.\packaging\build.ps1 -Installer
.\packaging\build.ps1 -Installer -Offline
.\packaging\build.ps1 -Installer -Architecture arm64
```

构建产物位于 `bin/`。本地可用 `-Version` 指定版本号，用 `-UpdateRepository` 指定公开的 GitHub 发布仓库。

GitHub Actions 在 PR 和推送到 `main` 时运行测试。发布时，在目标提交上创建并推送正式版本标签。工作流校验标签、构建三种安装包并发布到 GitHub Releases。

```powershell
git tag v0.0.3
git push origin v0.0.3
```

<details>
<summary>浏览器、录音和离线识别验收</summary>

浏览器验收位于 `tests/browser`，使用本机 Microsoft Edge 和模拟模型服务。首次运行会下载 Playwright 驱动。

```powershell
go build -o .\bin\Shengji-server-amd64.exe ./cmd/shengji-server
go -C tests/browser test -count=1 -args -binary ..\..\bin\Shengji-server-amd64.exe
go build -tags desktoptest -o .\bin\Shengji-desktop-test.exe ./cmd/shengji
go -C tests/browser test -count=1 -args -binary ..\..\bin\Shengji-desktop-test.exe -desktop
```

服务主机装有 FFmpeg 时，可在验收命令后添加 `-video ../../internal/minutes/testdata/video-with-audio.mp4` 验证视频导入。WASAPI 实机测试需要默认麦克风和播放设备，会播放短测试音。

```powershell
go test -race ./internal/minutes
go test -tags wasapi_integration -coverprofile coverage.out ./internal/minutes
go test -tags sherpa_integration ./internal/minutes
```

离线识别验收所需的环境变量和样本要求见 [Sherpa 集成测试](internal/minutes/sherpa_integration_test.go)。

</details>

<details>
<summary>代码结构</summary>

| 目录 | 职责 |
| --- | --- |
| `cmd/shengji`、`cmd/shengji-server` | Windows 桌面程序与独立 Server 入口 |
| `internal/launch`、`internal/minutes` | 服务启动、会议库、录音和模型任务 |
| `internal/agentapi`、`cmd/sherpa-asr` | HTTP ASR、MCP 和本机转录工作进程 |
| `internal/webui/static` | 桌面与浏览器共用的界面，样式规范见 [DESIGN.md](DESIGN.md) |

</details>
