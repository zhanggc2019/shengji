"""Run the browser workflow against a real app binary and local AI doubles."""

import argparse
import contextlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
from urllib.parse import urljoin
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from playwright.sync_api import expect, sync_playwright


class ProviderDouble(BaseHTTPRequestHandler):
    """Supply deterministic transcription and summary responses without external credentials."""

    def do_POST(self):
        """Accept the same endpoints and request bodies as the configured services."""
        body = self.rfile.read(int(self.headers["Content-Length"])) if self.headers.get("Content-Length") else b""
        if self.path == "/v1/audio/transcriptions":
            if self.headers.get("Transfer-Encoding") == "chunked":
                while chunk_size := int(self.rfile.readline().strip(), 16):
                    body += self.rfile.read(chunk_size)
                    self.rfile.read(2)
                self.rfile.read(2)
            assert b"verbose_json" in body and b'timestamp_granularities[]' in body
            response = {
                "segments": [{"text": "下周发布", "speaker": "李工", "start": 0, "end": 1.2}],
                "words": [{"word": "下周", "start": 0, "end": 0.5},
                          {"word": "发布", "start": 0.5, "end": 1.2}],
            }
        elif self.path == "/api/chat":
            assert "负责人" in json.loads(body)["messages"][0]["content"]
            response = {"message": {"content": "## 决策\n- 下周发布，由李工负责。"}}
        else:
            self.send_error(404)
            return
        payload = json.dumps(response, ensure_ascii=False).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_args):
        """Keep the smoke-test console focused on product errors."""


def free_port():
    """Choose an unused loopback port for the test application."""
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_http(url, proc, diagnostics, route="/api/health"):
    """Check readiness with a bounded wait and expose server logs on startup failure."""
    deadline = time.monotonic() + 12
    while time.monotonic() < deadline and proc.poll() is None:
        try:
            with urllib.request.urlopen(url + route, timeout=1) as response:
                if response.status == 200:
                    return
        except (OSError, urllib.error.URLError):
            time.sleep(0.12)
    raise RuntimeError(f"Application did not start: {diagnostics.read_text(encoding='utf-8', errors='replace')}")


def configure(page, provider_port):
    """Set a local provider through the same form desktop users use."""
    page.get_by_role("button", name="模型设置").click()
    page.get_by_label("转录服务", exact=True).select_option("sherpa")
    expect(page.get_by_label("模型包目录")).to_be_visible()
    expect(page.get_by_label("转录模型")).to_have_value("sensevoice")
    page.get_by_label("转录服务", exact=True).select_option("oddasr")
    page.get_by_label("转录服务地址").fill(f"http://127.0.0.1:{provider_port}")
    page.get_by_label("摘要服务地址").fill(f"http://127.0.0.1:{provider_port}")
    page.get_by_role("dialog").get_by_role("button", name="保存", exact=True).click()
    expect(page.get_by_role("dialog")).to_have_count(0)


def run_workflow(page, parallel_page, url, provider_port, audio_path):
    """Check that two browser clients share the meeting and result produced by the same Go process."""
    for tab in (page, parallel_page):
        tab.goto(url, wait_until="networkidle")
        expect(tab.locator("#connection")).to_contain_text("已连接")
        expect(tab.locator("#active-asr")).to_contain_text("本机离线 · SenseVoice")
    configure(page, provider_port)
    expect(parallel_page.locator("#active-asr")).to_contain_text("OddASR · whisper-1")
    page.get_by_role("button", name="导入音视频", exact=True).click()
    page.get_by_role("dialog").get_by_label("记录名称").fill("浏览器验收会议")
    page.get_by_label("音频或视频文件").set_input_files(audio_path)
    page.get_by_role("dialog").get_by_role("button", name="导入", exact=True).click()
    expect(page.get_by_role("heading", name="浏览器验收会议")).to_be_visible()
    expect(page.locator("audio")).to_be_visible()
    peer = parallel_page.locator("#meeting-list").get_by_role("button", name="浏览器验收会议")
    expect(peer).to_be_visible()
    peer.click()
    expect(parallel_page.locator("audio")).to_be_visible()
    page.get_by_role("button", name="生成转录").click()
    expect(page.get_by_role("button", name="发布", exact=True)).to_be_visible(timeout=10_000)
    expect(page.locator("#transcription-metrics")).to_contain_text("转录耗时")
    expect(parallel_page.get_by_role("button", name="发布", exact=True)).to_be_visible(timeout=10_000)
    page.get_by_role("button", name="编辑转录片段 1").click()
    page.get_by_label("文本", exact=True).fill("下周发布，李工负责")
    page.get_by_role("dialog").get_by_role("button", name="保存", exact=True).click()
    expect(page.get_by_text("下周发布，李工负责", exact=True)).to_be_visible()
    page.get_by_role("tab", name="AI 纪要", exact=True).click()
    page.get_by_role("button", name="生成纪要").click()
    expect(page.get_by_text("• 下周发布，由李工负责。")).to_be_visible(timeout=10_000)
    actions = page.locator("#meeting-menu summary")
    actions.click()
    expect(page.get_by_label("导出格式")).to_be_visible()
    page.keyboard.press("Escape")
    expect(page.get_by_label("导出格式")).not_to_be_visible()
    with page.expect_download() as download:
        page.get_by_role("button", name="导出", exact=True).click()
    saved = download.value
    assert "浏览器验收会议" in saved.suggested_filename
    assert "下周发布，李工负责" in Path(saved.path()).read_text(encoding="utf-8")
    page.get_by_role("searchbox").fill("李工负责")
    expect(page.locator("#meeting-list").get_by_role("button", name="浏览器验收会议")).to_be_visible()
    page.get_by_role("searchbox").fill("没有匹配")
    expect(page.get_by_text("没有匹配的会议")).to_be_visible()


def check_batch_import(page, audio_path, video_path):
    """Check the 20-file limit and retry only the failed file in a mixed batch."""
    page.get_by_role("button", name="导入音视频", exact=True).click()
    dialog = page.get_by_role("dialog", name="导入音视频")
    picker = dialog.get_by_label("音频或视频文件")
    submit = dialog.get_by_role("button", name="导入", exact=True)
    audio = audio_path.read_bytes()
    files = [{"name": f"test-{index}.wav", "mimeType": "audio/wav", "buffer": audio} for index in range(21)]
    picker.set_input_files(files[:20])
    expect(submit).to_be_enabled()
    picker.set_input_files(files)
    expect(dialog.get_by_role("alert")).to_contain_text("一次最多选择 20 个文件")
    expect(submit).to_be_disabled()

    batch = [
        {"name": "批量音频.wav", "mimeType": "audio/wav", "buffer": audio},
        {"name": "无效文件.wav", "mimeType": "audio/wav", "buffer": b"not a WAV"},
    ]
    if video_path:
        expect(dialog.locator("#ffmpeg-status")).to_contain_text("可以导入视频")
        batch.append({"name": "批量视频.mp4", "mimeType": "video/mp4", "buffer": video_path.read_bytes()})
    picker.set_input_files(batch)
    expect(dialog.get_by_label("记录名称")).not_to_be_visible()
    submit.click()
    expect(dialog.get_by_role("alert")).to_contain_text("1 个文件导入失败")
    expect(dialog.locator("#import-files")).to_contain_text("无效文件.wav")
    expect(dialog.locator("#import-files li")).to_have_count(1)
    dialog.get_by_role("button", name="重试失败文件").click()
    expect(dialog.locator("#import-progress")).to_have_text("已导入 0 个文件")
    dialog.get_by_role("button", name="取消").click()
    library = page.locator("#meeting-list")
    expect(library.get_by_role("button", name="批量音频")).to_have_count(1)
    if video_path:
        video = library.get_by_role("button", name="批量视频")
        expect(video).to_have_count(1)
        video.click()
        expect(page.locator("audio")).to_be_visible()
        page.get_by_role("button", name="生成转录").click()
        expect(page.get_by_role("button", name="发布", exact=True)).to_be_visible(timeout=10_000)

    page.get_by_role("button", name="导入音视频", exact=True).click()
    picker.set_input_files(files[:20])
    submit.click()
    expect(dialog).not_to_be_visible(timeout=15_000)
    expect(page.locator("#notice")).to_contain_text("已导入 20 个文件")
    expect(library.get_by_role("button", name="test-19")).to_be_visible()


def check_ffmpeg_guide(page, audio_path, video_path):
    """Keep selected videos while the service guides setup and accepts the batch's audio."""
    capability = {"ffmpeg": "missing", "platform": "windows"}
    capability_route = "**/api/import/capabilities"
    page.route(capability_route, lambda route: route.fulfill(json=capability))
    try:
        page.get_by_role("button", name="导入音视频", exact=True).click()
        dialog = page.get_by_role("dialog", name="导入音视频")
        expect(dialog.locator("#ffmpeg-status")).to_contain_text("尚未安装")
        picker = dialog.get_by_label("音频或视频文件")
        video = {"name": "等待配置.mp4", "mimeType": "video/mp4", "buffer": video_path.read_bytes() if video_path else b"video"}
        audio = {"name": "先导入.wav", "mimeType": "audio/wav", "buffer": audio_path.read_bytes()}
        picker.set_input_files([video])
        submit = dialog.locator("#import-submit")
        expect(submit).to_be_disabled()
        expect(dialog.get_by_text("ffmpeg.exe", exact=True)).to_be_visible()
        expect(dialog.get_by_role("link", name="打开 FFmpeg 下载页")).to_have_attribute("href", "https://ffmpeg.org/download.html#build-windows")
        picker.set_input_files([video, audio])
        dialog.get_by_role("button", name="先导入音频").click()
        expect(dialog.locator("#import-progress")).to_have_text("已导入 1 个文件")
        expect(dialog.locator("#import-files li")).to_have_text("等待配置.mp4 · 等待 FFmpeg")
        expect(submit).to_be_disabled()

        capability["ffmpeg"] = "ready"
        dialog.get_by_role("button", name="重新检测").click()
        expect(dialog.locator("#ffmpeg-status")).to_contain_text("可以导入视频")
        expect(dialog.locator("#import-files li")).to_have_text("等待配置.mp4")
        expect(submit).to_be_enabled()
        if video_path:
            submit.click()
            expect(dialog).not_to_be_visible()
            expect(page.get_by_role("heading", name="等待配置")).to_be_visible()
        else:
            dialog.get_by_role("button", name="取消").click()
    finally:
        page.unroute(capability_route)


def check_pure_transcript(page, audio_path):
    """Import a non-meeting recording and keep the timed text while omitting speaker UI."""
    page.get_by_role("button", name="导入音视频", exact=True).click()
    dialog = page.get_by_role("dialog", name="导入音视频")
    dialog.get_by_label("记录模式").select_option("transcript")
    dialog.get_by_label("记录名称").fill("纯转录验收")
    dialog.get_by_label("音频或视频文件").set_input_files(audio_path)
    dialog.get_by_role("button", name="导入", exact=True).click()
    expect(page.get_by_role("heading", name="纯转录验收")).to_be_visible()
    expect(page.locator("#date")).to_contain_text("纯转录")
    expect(page.get_by_role("tab", name="AI 纪要")).not_to_be_visible()
    page.get_by_role("button", name="生成转录", exact=True).click()
    expect(page.locator("#transcription-metrics")).to_contain_text("音频时长 00:02")
    expect(page.get_by_role("button", name="发布", exact=True)).to_be_visible(timeout=10_000)
    page.get_by_role("button", name="编辑转录片段 1", exact=True).click()
    expect(page.get_by_label("发言人", exact=True)).not_to_be_visible()
    page.locator("#segment-dialog").get_by_role("button", name="取消").click()


def check_summary_choices(page):
    """Save each named cloud preset with only a model choice."""
    for provider, model in (
        ("deepseek", "deepseek-v4-pro"), ("zhipu", "glm-5.3"), ("siliconflow", "deepseek-ai/DeepSeek-V3")
    ):
        page.get_by_role("button", name="模型设置").click()
        dialog = page.locator("#settings-dialog")
        dialog.get_by_label("摘要服务", exact=True).select_option(provider)
        expect(dialog.get_by_label("摘要服务地址")).not_to_be_visible()
        dialog.get_by_label("摘要模型", exact=True).select_option(model)
        dialog.get_by_role("button", name="保存", exact=True).click()
        expect(dialog).not_to_be_visible()
        saved = page.request.get(urljoin(page.url, "/api/settings")).json()
        assert (saved["provider"], saved["llm_model"], saved["llm_url"]) == (provider, model, "")


def check_model_choices(page):
    """Show downloadable local models with the matching use case and persist the selection."""
    page.get_by_role("button", name="模型设置").click()
    dialog = page.get_by_role("dialog").filter(has_text="模型设置")
    provider = dialog.get_by_label("转录服务", exact=True)
    provider.select_option(label="本机转录（离线）")
    model = dialog.get_by_label("转录模型", exact=True)
    expect(model).to_have_value("sensevoice")
    link = dialog.get_by_role("link", name="下载 SenseVoice 模型")
    source = "https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo"
    expect(link).to_have_attribute("href", source + "?Revision=860e3f03644fb4d979bfd93c4ef345c93c0feaf8&FilePath=model.int8.onnx")
    tokens = dialog.get_by_role("link", name="下载词表")
    expect(tokens).to_have_attribute("href", source + "?Revision=6652fb804909a130d070d533690dd578e983784d&FilePath=tokens.txt")
    expect(dialog.locator("#asr-download-help")).to_contain_text("从魔搭社区")
    expect(dialog.locator("#asr-model-use")).to_contain_text("粤语、日语或韩语")
    capabilities = dialog.locator("#asr-model-capabilities")
    expect(capabilities).to_contain_text("两个模型均可按字词定位回听")
    expect(capabilities).to_contain_text("发言人均需手动标注")

    model.select_option("paraformer")
    link = dialog.get_by_role("link", name="下载 Paraformer 模型")
    expect(link).to_have_attribute("href", "https://gh-proxy.com/https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-paraformer-zh-2023-09-14.tar.bz2")
    expect(tokens).not_to_be_visible()
    expect(dialog.locator("#asr-download-help")).to_contain_text("从社区加速镜像")
    expect(dialog.locator("#asr-model-use")).to_contain_text("普通话、河南话、天津话或四川话")
    expect(capabilities).to_contain_text("发言人均需手动标注")
    dialog.get_by_label("模型包目录").fill("test-model")
    dialog.get_by_role("button", name="保存", exact=True).click()
    expect(dialog).not_to_be_visible()
    expect(page.locator("#notice")).to_contain_text("模型设置已保存")
    page.get_by_role("button", name="模型设置").click()
    expect(model).to_have_value("paraformer")
    provider.select_option("oddasr")
    expect(link).not_to_be_visible()
    dialog.get_by_role("button", name="取消").click()


def check_notes_draft(page):
    """Preserve an unsaved note while opening a different meeting."""
    page.get_by_role("tab", name="笔记").click()
    notes = page.get_by_label("会议笔记")
    notes.fill("切换会议时保留这条草稿")
    expect(page.get_by_role("button", name="保存笔记")).to_be_enabled()
    created = page.request.post(urljoin(page.url, "/api/meetings"), data={"title": "另一场会议"})
    assert created.ok
    library = page.locator("#meeting-list")
    library.get_by_role("button", name="另一场会议").click()
    library.get_by_role("button", name="浏览器验收会议").click()
    page.get_by_role("tab", name="笔记").click()
    expect(notes).to_have_value("切换会议时保留这条草稿")
    page.get_by_role("button", name="保存笔记").click()
    expect(page.get_by_role("button", name="保存笔记")).to_be_disabled()
    page.get_by_role("tab", name="AI 纪要").click()


def check_narrow_workspace(browser, url, screenshot=None):
    """Exercise the collapsed meeting library and keyboard navigation on a narrow viewport."""
    mobile = browser.new_context(viewport={"width": 390, "height": 844}, is_mobile=True, has_touch=True)
    try:
        tab = mobile.new_page()
        tab.goto(url, wait_until="networkidle")
        expect(tab.get_by_role("heading", name="另一场会议")).to_be_visible()
        library = tab.get_by_role("button", name="会议库", exact=True)
        expect(library).to_have_attribute("aria-expanded", "false")
        assert tab.evaluate("document.querySelector('.topbar').getBoundingClientRect().top - document.querySelector('#library-toggle').getBoundingClientRect().bottom < 30")
        expect(tab.get_by_role("searchbox")).not_to_be_visible()
        library.tap()
        expect(library).to_have_attribute("aria-expanded", "true")
        tab.locator("#meeting-list").get_by_role("button", name="浏览器验收会议").tap()
        expect(library).to_have_attribute("aria-expanded", "false")
        assert tab.evaluate("document.querySelector('.topbar').getBoundingClientRect().top - document.querySelector('#library-toggle').getBoundingClientRect().bottom < 30")
        current = tab.get_by_role("tab", name="转录")
        current.focus()
        tab.keyboard.press("ArrowRight")
        expect(tab.get_by_role("tab", name="AI 纪要")).to_have_attribute("aria-selected", "true")
        expect(tab.get_by_role("tabpanel", name="AI 纪要")).to_be_visible()
        assert tab.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
        if screenshot:
            tab.screenshot(path=str(screenshot), full_page=True)
        tab.set_viewport_size({"width": 320, "height": 720})
        assert tab.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    finally:
        mobile.close()



def check_themes(page, url):
    """Verify brand and light-by-default theme behavior even under a dark OS preference."""
    page.goto(url, wait_until="networkidle")
    expect(page).to_have_title("声记")
    expect(page.locator(".brand")).to_have_text("声记")
    expect(page.locator("html")).to_have_attribute("data-theme", "light")
    page.get_by_role("button", name="切换到深色主题").click()
    expect(page.locator("html")).to_have_attribute("data-theme", "dark")
    page.reload(wait_until="networkidle")
    expect(page.locator("html")).to_have_attribute("data-theme", "dark")
    page.get_by_role("button", name="切换到浅色主题").click()
    expect(page.locator("html")).to_have_attribute("data-theme", "light")


@contextlib.contextmanager
def test_workspace():
    """Allow WebView2 a short time to release its temporary database files on Windows."""
    temporary = tempfile.TemporaryDirectory(prefix="oddminutes-browser-")
    try:
        yield Path(temporary.name)
    finally:
        for attempt in range(10):
            try:
                temporary.cleanup()
                break
            except PermissionError as error:
                if getattr(error, "winerror", None) != 32 or attempt == 9:
                    raise
                time.sleep(0.2)


def main():
    """Start the binary with a disposable library and exercise Microsoft Edge in headless mode."""
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("--screenshot", type=Path)
    parser.add_argument("--desktop", action="store_true", help="use a binary built with -tags desktoptest")
    parser.add_argument("--dark-screenshot", type=Path)
    parser.add_argument("--mobile-screenshot", type=Path)
    parser.add_argument("--video", type=Path, help="MP4 fixture with an audio track (requires FFmpeg on the server)")
    args = parser.parse_args()
    with test_workspace() as root:
        audio = root / "fixture.wav"
        with wave.open(str(audio), "wb") as file:
            file.setnchannels(1)
            file.setsampwidth(2)
            file.setframerate(16000)
            file.writeframes(bytes(64000))
        provider = ThreadingHTTPServer(("127.0.0.1", 0), ProviderDouble)
        thread = threading.Thread(target=provider.serve_forever, daemon=True)
        thread.start()
        address = f"127.0.0.1:{free_port()}"
        url = f"http://{address}"
        diagnostics = root / "server.log"
        flags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
        environment = os.environ.copy()
        launch = [str(args.binary.resolve()), "--listen", address, "--data-dir", str(root / "data")]
        debug_url = ""
        if args.desktop:
            debug_port = free_port()
            environment["SHENGJI_TEST_CDP_PORT"] = str(debug_port)
            debug_url = f"http://127.0.0.1:{debug_port}"
        else:
            launch.append("--browser-only")
        with diagnostics.open("wb") as log:
            process = subprocess.Popen(
                launch, env=environment,
                stdout=log, stderr=subprocess.STDOUT, creationflags=flags,
            )
            try:
                wait_http(url, process, diagnostics)
                with sync_playwright() as playwright:
                    with contextlib.ExitStack() as browsers:
                        browser = browsers.enter_context(contextlib.closing(playwright.chromium.launch(channel="msedge", headless=True)))
                        page = browser.new_page(viewport={"width": 1200, "height": 810}, color_scheme="dark")
                        desktop = None
                        if args.desktop:
                            wait_http(debug_url, process, diagnostics, "/json/version")
                            desktop = browsers.enter_context(contextlib.closing(playwright.chromium.connect_over_cdp(debug_url)))
                            secondary = desktop.contexts[0].pages[0]
                            expect(secondary.locator(".brand")).to_be_visible(timeout=10_000)
                        else:
                            secondary = browser.new_page()
                        page_errors = []
                        page.on("pageerror", lambda error: page_errors.append(error))
                        check_themes(page, url)
                        run_workflow(page, secondary, url, provider.server_port, audio)
                        page.get_by_role("searchbox").clear()
                        expect(page.locator("#meeting-list").get_by_role("button", name="浏览器验收会议")).to_be_visible()
                        check_notes_draft(page)
                        expect(page.locator("#notice")).not_to_be_visible()
                        page.locator("#search").evaluate("(input) => input.blur()")
                        if args.screenshot:
                            page.screenshot(path=str(args.screenshot), full_page=True)
                        if args.dark_screenshot:
                            page.get_by_role("button", name="切换到深色主题").click()
                            expect(page.locator("html")).to_have_attribute("data-theme", "dark")
                            expect(page.locator("#import-button")).to_have_css("background-color", "rgb(28, 32, 48)")
                            page.screenshot(path=str(args.dark_screenshot), full_page=True)
                        check_narrow_workspace(browser, url, args.mobile_screenshot)
                        check_batch_import(page, audio, args.video)
                        check_ffmpeg_guide(page, audio, args.video)
                        check_pure_transcript(page, audio)
                        check_model_choices(page)
                        check_summary_choices(page)
                        assert page_errors == [], page_errors
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=10)
                provider.shutdown()
                provider.server_close()
                thread.join(timeout=5)
        print("Shared workspace workflow passed (desktop + Edge or two Edge clients, upload, ASR, edit, summary, search, export).")


if __name__ == "__main__":
    main()
