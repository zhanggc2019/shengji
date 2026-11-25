package browser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	pw "github.com/mxschmitt/playwright-go"
)

// checkBatchImport exercises the batch limit, mixed media import, and retry of a failed file.
func checkBatchImport(t *testing.T, page pw.Page, audio pw.InputFile, video []byte) {
	root, dialog := page.Locator("body"), page.Locator("#import-dialog")
	importAudio := role(root, "button", "导入音视频", true)
	must(t, importAudio.Click())
	picker := dialog.GetByLabel("音频或视频文件")
	submit := role(dialog, "button", "导入", true)
	files := make([]pw.InputFile, 21)
	for i := range files {
		files[i] = audio
		files[i].Name = fmt.Sprintf("test-%d.wav", i)
	}
	must(t, picker.SetInputFiles(files[:20]))
	must(t, expect(submit).ToBeEnabled())
	must(t, picker.SetInputFiles(files))
	must(t, expect(dialog.GetByRole("alert")).ToContainText("一次最多选择 20 个文件"))
	must(t, expect(submit).ToBeDisabled())

	batch := []pw.InputFile{
		{Name: "批量音频.wav", MimeType: audio.MimeType, Buffer: audio.Buffer},
		{Name: "无效文件.wav", MimeType: audio.MimeType, Buffer: []byte("not a WAV")},
	}
	if video != nil {
		must(t, expect(dialog.Locator("#ffmpeg-status")).ToContainText("可以导入视频"))
		batch = append(batch, pw.InputFile{Name: "批量视频.mp4", MimeType: "video/mp4", Buffer: video})
	}
	must(t, picker.SetInputFiles(batch))
	must(t, expect(dialog.GetByLabel("记录名称")).ToBeHidden())
	must(t, submit.Click())
	must(t, expect(dialog.GetByRole("alert")).ToContainText("1 个文件导入失败"))
	must(t, expect(dialog.Locator("#import-files")).ToContainText("无效文件.wav"))
	must(t, expect(dialog.Locator("#import-files li")).ToHaveCount(1))
	must(t, role(dialog, "button", "重试失败文件", true).Click())
	must(t, expect(dialog.Locator("#import-progress")).ToHaveText("已导入 0 个文件"))
	must(t, role(dialog, "button", "取消", true).Click())
	library := page.Locator("#meeting-list")
	must(t, expect(role(library, "button", "批量音频")).ToHaveCount(1))
	if video != nil {
		entry := role(library, "button", "批量视频")
		must(t, expect(entry).ToHaveCount(1))
		must(t, entry.Click())
		must(t, expect(page.Locator("audio")).ToBeVisible())
		must(t, role(root, "button", "生成转录", true).Click())
		must(t, expect(role(root, "button", "发布", true)).ToBeVisible())
	}

	must(t, importAudio.Click())
	must(t, picker.SetInputFiles(files[:20]))
	must(t, submit.Click())
	must(t, expect(dialog).ToBeHidden())
	must(t, expect(page.Locator("#notice")).ToContainText("已导入 20 个文件"))
	must(t, expect(role(library, "button", "test-19")).ToBeVisible())
}

// checkFFmpegGuide verifies that setup and recheck preserve the selected video.
func checkFFmpegGuide(t *testing.T, page pw.Page, audio pw.InputFile, video []byte) {
	const capabilityRoute = "**/api/import/capabilities"
	var ready atomic.Bool
	must(t, page.Route(capabilityRoute, func(route pw.Route) {
		status := "missing"
		if ready.Load() {
			status = "ready"
		}
		if err := route.Fulfill(pw.RouteFulfillOptions{ContentType: pw.String("application/json"), Body: fmt.Sprintf(`{"ffmpeg":%q,"platform":"windows"}`, status)}); err != nil {
			t.Errorf("FFmpeg capability fixture: %v", err)
		}
	}))
	defer page.Unroute(capabilityRoute)
	root, dialog := page.Locator("body"), page.Locator("#import-dialog")
	must(t, role(root, "button", "导入音视频", true).Click())
	must(t, expect(dialog.Locator("#ffmpeg-status")).ToContainText("尚未安装"))
	picker := dialog.GetByLabel("音频或视频文件")
	videoFile := pw.InputFile{Name: "等待配置.mp4", MimeType: "video/mp4", Buffer: video}
	if video == nil {
		videoFile.Buffer = []byte("video")
	}
	audio.Name = "先导入.wav"
	must(t, picker.SetInputFiles(videoFile))
	submit := dialog.Locator("#import-submit")
	must(t, expect(submit).ToBeDisabled())
	must(t, expect(dialog.GetByText("ffmpeg.exe", pw.LocatorGetByTextOptions{Exact: pw.Bool(true)})).ToBeVisible())
	must(t, expect(role(dialog, "link", "打开 FFmpeg 下载页", true)).ToHaveAttribute("href", "https://ffmpeg.org/download.html#build-windows"))
	must(t, picker.SetInputFiles([]pw.InputFile{videoFile, audio}))
	must(t, role(dialog, "button", "先导入音频", true).Click())
	must(t, expect(dialog.Locator("#import-progress")).ToHaveText("已导入 1 个文件"))
	must(t, expect(dialog.Locator("#import-files li")).ToHaveText("等待配置.mp4 · 等待 FFmpeg"))
	must(t, expect(submit).ToBeDisabled())

	ready.Store(true)
	must(t, role(dialog, "button", "重新检测", true).Click())
	must(t, expect(dialog.Locator("#ffmpeg-status")).ToContainText("可以导入视频"))
	must(t, expect(dialog.Locator("#import-files li")).ToHaveText("等待配置.mp4"))
	must(t, expect(submit).ToBeEnabled())
	if video != nil {
		must(t, submit.Click())
		must(t, expect(dialog).ToBeHidden())
		must(t, expect(role(root, "heading", "等待配置", true)).ToBeVisible())
	} else {
		must(t, role(dialog, "button", "取消", true).Click())
	}
}

// checkPureTranscript verifies timed results without meeting-specific controls.
func checkPureTranscript(t *testing.T, page pw.Page, audio pw.InputFile) {
	root, dialog := page.Locator("body"), page.Locator("#import-dialog")
	must(t, role(root, "button", "导入音视频", true).Click())
	selectValue(t, dialog.GetByLabel("记录模式"), "transcript")
	must(t, dialog.GetByLabel("记录名称").Fill("纯转录验收"))
	must(t, dialog.GetByLabel("音频或视频文件").SetInputFiles(audio))
	must(t, role(dialog, "button", "导入", true).Click())
	must(t, expect(role(root, "heading", "纯转录验收", true)).ToBeVisible())
	must(t, expect(page.Locator("#date")).ToContainText("纯转录"))
	must(t, expect(role(root, "tab", "AI 纪要", true)).ToBeHidden())
	must(t, role(root, "button", "生成转录", true).Click())
	must(t, expect(page.Locator("#transcription-metrics")).ToContainText("音频时长 00:02"))
	must(t, expect(role(root, "button", "发布", true)).ToBeVisible())
	must(t, role(root, "button", "编辑转录片段 1", true).Click())
	must(t, expect(root.GetByLabel("发言人", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)})).ToBeHidden())
	must(t, role(page.Locator("#segment-dialog"), "button", "取消", true).Click())
}

// checkModelChoices verifies local model links, guidance, and persistence.
func checkModelChoices(t *testing.T, page pw.Page) {
	dialog := openSettings(t, page)
	provider := dialog.GetByLabel("转录服务", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)})
	selectValue(t, provider, "sherpa")
	model := dialog.GetByLabel("转录模型", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)})
	must(t, expect(model).ToHaveValue("sensevoice"))
	must(t, dialog.Locator("#asr-model-guide summary").Click())
	const senseVoice = "https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo"
	must(t, expect(role(dialog, "link", "下载 SenseVoice 模型", true)).ToHaveAttribute("href",
		senseVoice+"?Revision=860e3f03644fb4d979bfd93c4ef345c93c0feaf8&FilePath=model.int8.onnx"))
	tokens := role(dialog, "link", "下载词表", true)
	must(t, expect(tokens).ToHaveAttribute("href", senseVoice+"?Revision=6652fb804909a130d070d533690dd578e983784d&FilePath=tokens.txt"))
	hint, capabilities := dialog.Locator("#asr-model-use"), dialog.Locator("#asr-model-capabilities")
	must(t, expect(dialog.Locator("#asr-download-help")).ToContainText("从魔搭社区"))
	must(t, expect(hint).ToContainText("粤语、日语或韩语"))
	must(t, expect(capabilities).ToContainText("两个模型均可按字词定位回听"))
	must(t, expect(capabilities).ToContainText("发言人均需手动标注"))

	selectValue(t, model, "paraformer")
	link := role(dialog, "link", "下载 Paraformer 模型", true)
	must(t, expect(link).ToHaveAttribute("href", "https://gh-proxy.com/https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-paraformer-zh-2023-09-14.tar.bz2"))
	must(t, expect(tokens).ToBeHidden())
	must(t, expect(hint).ToContainText("普通话、河南话、天津话或四川话"))
	must(t, expect(capabilities).ToContainText("两个模型均可按字词定位回听"))
	must(t, expect(capabilities).ToContainText("发言人均需手动标注"))
	must(t, dialog.GetByLabel("模型包目录").Fill("test-model"))
	must(t, role(dialog, "button", "保存", true).Click())
	must(t, expect(dialog).ToBeHidden())
	must(t, expect(page.Locator("#notice")).ToContainText("模型设置已保存"))
	openSettings(t, page)
	must(t, expect(model).ToHaveValue("paraformer"))
	selectValue(t, provider, "openai")
	must(t, expect(link).ToBeHidden())
	must(t, role(dialog, "button", "取消", true).Click())
}

// checkSummaryChoices verifies that each cloud preset saves its model and write-only API key.
func checkSummaryChoices(t *testing.T, page pw.Page, url string) {
	for _, entry := range []struct{ provider, model string }{
		{"deepseek", "deepseek-v4-pro"}, {"zhipu", "glm-5.3"}, {"siliconflow", "deepseek-ai/DeepSeek-V3"},
	} {
		dialog := openSettings(t, page)
		selectValue(t, dialog.GetByLabel("摘要服务", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)}), entry.provider)
		must(t, expect(dialog.GetByLabel("摘要服务地址")).ToBeHidden())
		key := dialog.GetByLabel("摘要 API Key")
		must(t, expect(key).ToHaveAttribute("type", "password"))
		must(t, key.Fill("fixture-"+entry.provider))
		selectValue(t, dialog.GetByLabel("摘要模型", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)}), entry.model)
		must(t, role(dialog, "button", "保存", true).Click())
		must(t, expect(dialog).ToBeHidden())
		response := request(t, http.MethodGet, url+"/api/settings?credentials=1", nil, http.StatusOK)
		var saved struct {
			Provider    string `json:"provider"`
			Model       string `json:"llm_model"`
			URL         string `json:"llm_url"`
			Credentials struct {
				LLMSaved bool `json:"llm_saved"`
			} `json:"credentials"`
		}
		err := json.NewDecoder(response.Body).Decode(&saved)
		_ = response.Body.Close()
		must(t, err)
		if saved.Provider != entry.provider || saved.Model != entry.model || saved.URL != "" || !saved.Credentials.LLMSaved {
			t.Fatalf("unexpected saved provider: %+v", saved)
		}
	}
	dialog := openSettings(t, page)
	must(t, expect(dialog.GetByLabel("摘要 API Key")).ToHaveValue(""))
	must(t, expect(dialog.Locator("#llm-key-help")).ToContainText("已保存"))
	must(t, dialog.GetByLabel("清除已保存的摘要密钥").Check())
	must(t, role(dialog, "button", "保存", true).Click())
	must(t, expect(dialog).ToBeHidden())
	dialog = openSettings(t, page)
	must(t, expect(dialog.GetByLabel("清除已保存的摘要密钥")).ToBeHidden())
	must(t, role(dialog, "button", "取消", true).Click())
}
