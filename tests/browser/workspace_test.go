package browser

import (
	"net/http"
	"os"
	"strings"
	"testing"

	pw "github.com/mxschmitt/playwright-go"
)

const fixtureMeeting = "浏览器验收会议"

// configureProviders selects the local doubles through the same dialog used by operators.
func configureProviders(t *testing.T, page pw.Page, providerURL string) {
	dialog := openSettings(t, page)
	provider := dialog.GetByLabel("转录服务", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)})
	selectValue(t, provider, "sherpa")
	must(t, expect(dialog.GetByLabel("模型包目录")).ToBeVisible())
	must(t, expect(dialog.GetByLabel("转录 API Key")).ToBeHidden())
	must(t, expect(dialog.GetByLabel("转录模型", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)})).ToHaveValue("sensevoice"))
	selectValue(t, provider, "openai")
	must(t, dialog.GetByLabel("转录服务地址").Fill(providerURL))
	must(t, dialog.GetByLabel("转录模型", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)}).Fill("whisper-1"))
	must(t, dialog.GetByLabel("转录 API Key").Fill(fixtureASRKey))
	must(t, dialog.GetByLabel("摘要服务地址").Fill(providerURL))
	must(t, dialog.GetByLabel("摘要 API Key").Fill(fixtureSummaryKey))
	must(t, role(dialog, "button", "保存", true).Click())
	must(t, expect(dialog).ToBeHidden())
}

// checkSharedWorkspace verifies cross-client settings, ASR, editing, summary, downloads, and search.
func checkSharedWorkspace(t *testing.T, page, peer pw.Page, url, providerURL string, audio pw.InputFile) {
	for _, tab := range []pw.Page{page, peer} {
		target := url
		if tab == peer && *desktopRun {
			target = peer.URL()
		}
		_, err := tab.Goto(target, pw.PageGotoOptions{WaitUntil: pw.WaitUntilStateDomcontentloaded})
		must(t, err)
		must(t, expect(tab.Locator("#connection")).ToContainText("已连接"))
		must(t, expect(tab.Locator("#active-asr")).ToContainText("本机离线 · SenseVoice"))
	}
	root, dialog := page.Locator("body"), page.Locator("#import-dialog")
	must(t, role(root, "button", "导入音视频", true).Click())
	must(t, dialog.GetByLabel("记录名称").Fill(fixtureMeeting))
	must(t, dialog.GetByLabel("音频或视频文件").SetInputFiles(audio))
	must(t, role(dialog, "button", "导入", true).Click())
	must(t, expect(role(root, "heading", fixtureMeeting, true)).ToBeVisible())
	must(t, expect(page.Locator("audio")).ToBeVisible())
	must(t, role(peer.Locator("#meeting-list"), "button", fixtureMeeting).Click())
	must(t, expect(peer.Locator("audio")).ToBeVisible())
	must(t, expect(role(root, "button", "生成转录", true)).ToBeHidden())
	must(t, page.Locator("#export-menu summary").Click())
	must(t, expect(role(root, "button", "JSON", true)).ToBeVisible())
	must(t, expect(role(root, "button", "Markdown", true)).ToBeHidden())
	must(t, page.Keyboard().Press("Escape"))
	must(t, role(root, "button", "配置转录模型", true).Click())
	must(t, expect(page.Locator("#settings-dialog").GetByLabel("模型包目录")).ToBeFocused())
	must(t, expect(role(page.Locator("#settings-dialog"), "button", "保存", true)).ToBeInViewport())
	must(t, page.Keyboard().Press("Escape"))
	configureProviders(t, page, providerURL)
	must(t, expect(peer.Locator("#active-asr")).ToContainText("OpenAI 兼容 · whisper-1"))

	must(t, role(root, "button", "生成转录", true).Click())
	must(t, expect(role(root, "button", "发布", true)).ToBeVisible())
	must(t, expect(page.Locator("#transcription-metrics")).ToContainText("转录耗时"))
	must(t, expect(role(peer.Locator("body"), "button", "发布", true)).ToBeVisible())
	must(t, role(root, "button", "编辑转录片段 1", true).Click())
	must(t, page.Locator("#segment-dialog").GetByLabel("文本", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)}).Fill("下周发布，李工负责"))
	must(t, role(page.Locator("#segment-dialog"), "button", "保存", true).Click())
	must(t, expect(root.GetByText("下周发布，李工负责", pw.LocatorGetByTextOptions{Exact: pw.Bool(true)})).ToBeVisible())

	must(t, role(root, "tab", "AI 纪要", true).Click())
	must(t, role(root, "button", "生成纪要", true).Click())
	must(t, expect(page.Locator("#summary")).ToContainText("• 下周发布，由李工负责。"))
	export := page.Locator("#export-menu summary")
	markdown := role(root, "button", "Markdown", true)
	must(t, export.Click())
	must(t, expect(markdown).ToBeVisible())
	must(t, page.Keyboard().Press("Escape"))
	must(t, expect(markdown).ToBeHidden())
	must(t, export.Click())
	download, err := page.ExpectDownload(func() error { return markdown.Click() })
	must(t, err)
	if !strings.Contains(download.SuggestedFilename(), fixtureMeeting) {
		t.Fatalf("unexpected download name: %q", download.SuggestedFilename())
	}
	path, err := download.Path()
	must(t, err)
	data, err := os.ReadFile(path)
	must(t, err)
	if !strings.Contains(string(data), "下周发布，李工负责") {
		t.Fatalf("download omitted the edited transcript: %s", data)
	}
	search := root.GetByRole("searchbox")
	must(t, search.Fill("李工负责"))
	must(t, expect(role(page.Locator("#meeting-list"), "button", fixtureMeeting)).ToBeVisible())
	must(t, search.Fill("没有匹配"))
	must(t, expect(root.GetByText("没有匹配的会议")).ToBeVisible())
	must(t, search.Clear())
	must(t, expect(role(page.Locator("#meeting-list"), "button", fixtureMeeting)).ToBeVisible())
}

// checkNotesDraft verifies that navigating between meetings retains unsaved text.
func checkNotesDraft(t *testing.T, page pw.Page, url string) {
	root, library := page.Locator("body"), page.Locator("#meeting-list")
	must(t, role(root, "tab", "笔记", true).Click())
	notes := root.GetByLabel("会议笔记")
	must(t, notes.Fill("切换会议时保留这条草稿"))
	save := role(root, "button", "保存笔记", true)
	must(t, expect(save).ToBeEnabled())
	response := request(t, http.MethodPost, url+"/api/meetings", map[string]string{"title": "另一场会议"}, http.StatusCreated)
	must(t, response.Body.Close())
	must(t, role(library, "button", "另一场会议").Click())
	must(t, role(library, "button", fixtureMeeting).Click())
	must(t, role(root, "tab", "笔记", true).Click())
	must(t, expect(notes).ToHaveValue("切换会议时保留这条草稿"))
	must(t, save.Click())
	must(t, expect(save).ToBeDisabled())
	must(t, role(root, "tab", "AI 纪要", true).Click())
	must(t, expect(page.Locator("#notice")).ToBeHidden())
	_, err := page.Locator("#search").Evaluate("(input) => input.blur()", nil)
	must(t, err)
}

// checkNarrowWorkspace verifies the collapsed library, tab keyboard controls, and narrow overflow.
func checkNarrowWorkspace(t *testing.T, browser pw.Browser, url string) {
	mobile, err := browser.NewContext(pw.BrowserNewContextOptions{
		Viewport: &pw.Size{Width: 390, Height: 844}, IsMobile: pw.Bool(true), HasTouch: pw.Bool(true),
	})
	must(t, err)
	defer mobile.Close()
	page, err := mobile.NewPage()
	must(t, err)
	_, err = page.Goto(url, pw.PageGotoOptions{WaitUntil: pw.WaitUntilStateDomcontentloaded})
	must(t, err)
	root := page.Locator("body")
	must(t, expect(role(root, "heading", "另一场会议")).ToBeVisible())
	library := role(root, "button", "会议库", true)
	must(t, expect(library).ToHaveAttribute("aria-expanded", "false"))
	const compactHeader = "document.querySelector('.topbar').getBoundingClientRect().top - document.querySelector('#library-toggle').getBoundingClientRect().bottom < 30"
	checkPageCondition(t, page, compactHeader)
	must(t, expect(root.GetByRole("searchbox")).ToBeHidden())
	must(t, library.Tap())
	must(t, expect(library).ToHaveAttribute("aria-expanded", "true"))
	must(t, role(page.Locator("#meeting-list"), "button", fixtureMeeting).Tap())
	must(t, expect(library).ToHaveAttribute("aria-expanded", "false"))
	checkPageCondition(t, page, compactHeader)
	must(t, role(root, "tab", "转录", true).Focus())
	must(t, page.Keyboard().Press("ArrowRight"))
	must(t, expect(role(root, "tab", "AI 纪要", true)).ToHaveAttribute("aria-selected", "true"))
	must(t, expect(role(root, "tabpanel", "AI 纪要", true)).ToBeVisible())
	const noOverflow = "document.documentElement.scrollWidth <= window.innerWidth"
	checkPageCondition(t, page, noOverflow)
	saveScreenshot(t, page, *mobileShot)
	must(t, page.SetViewportSize(320, 720))
	checkPageCondition(t, page, noOverflow)
}

// checkShortWorkspace covers the usable viewport of a small or highly scaled desktop window.
func checkShortWorkspace(t *testing.T, browser pw.Browser, url string) {
	page, err := browser.NewPage(pw.BrowserNewPageOptions{Viewport: &pw.Size{Width: 760, Height: 300}})
	must(t, err)
	defer page.Close()
	_, err = page.Goto(url, pw.PageGotoOptions{WaitUntil: pw.WaitUntilStateDomcontentloaded})
	must(t, err)
	root := page.Locator("body")
	must(t, expect(role(root, "heading", "另一场会议")).ToBeVisible())
	settings := role(root, "button", "模型设置", true)
	must(t, settings.ScrollIntoViewIfNeeded())
	must(t, expect(settings).ToBeInViewport(pw.LocatorAssertionsToBeInViewportOptions{Ratio: pw.Float(1)}))

	for _, width := range []int{760, 360} {
		must(t, page.SetViewportSize(width, 300))
		if width == 360 {
			must(t, role(root, "button", "会议库", true).Click())
		}
		dialog := openSettings(t, page)
		selectValue(t, dialog.GetByLabel("转录服务", pw.LocatorGetByLabelOptions{Exact: pw.Bool(true)}), "sherpa")
		checkPageCondition(t, page, `(() => {
			const dialog = document.querySelector('#settings-dialog');
			const bounds = dialog.getBoundingClientRect();
			return bounds.top >= 0 && bounds.bottom <= innerHeight && dialog.scrollHeight > dialog.clientHeight;
		})()`)
		save := role(dialog, "button", "保存", true)
		must(t, expect(save).ToBeInViewport(pw.LocatorAssertionsToBeInViewportOptions{Ratio: pw.Float(1)}))
		must(t, dialog.GetByLabel("模型包目录").ScrollIntoViewIfNeeded())
		must(t, expect(save).ToBeInViewport(pw.LocatorAssertionsToBeInViewportOptions{Ratio: pw.Float(1)}))
		must(t, role(dialog, "button", "取消", true).Click())
		checkPageCondition(t, page, "document.documentElement.scrollWidth <= window.innerWidth")
	}
}

// checkPageCondition asserts a browser-computed layout relationship.
func checkPageCondition(t *testing.T, page pw.Page, condition string) {
	t.Helper()
	value, err := page.Evaluate(condition)
	must(t, err)
	if value != true {
		t.Fatalf("page condition failed: %s", condition)
	}
}

// checkThemes verifies the default, theme persistence, and uniqueness of the empty-state entries.
func checkThemes(t *testing.T, page pw.Page, url string) {
	_, err := page.Goto(url, pw.PageGotoOptions{WaitUntil: pw.WaitUntilStateDomcontentloaded})
	must(t, err)
	title, err := page.Title()
	must(t, err)
	if title != "声记" {
		t.Fatalf("unexpected page title: %s", title)
	}
	root, html := page.Locator("body"), page.Locator("html")
	must(t, expect(page.Locator(".brand")).ToHaveText("声记"))
	must(t, expect(html).ToHaveAttribute("data-theme", "light"))
	must(t, expect(role(root, "heading", "暂无会议", true)).ToBeVisible())
	must(t, expect(role(root, "button", "新建记录", true)).ToHaveCount(1))
	must(t, expect(role(root, "button", "导入音视频", true)).ToHaveCount(1))
	must(t, role(root, "button", "切换到深色主题", true).Click())
	must(t, expect(html).ToHaveAttribute("data-theme", "dark"))
	_, err = page.Reload(pw.PageReloadOptions{WaitUntil: pw.WaitUntilStateDomcontentloaded})
	must(t, err)
	must(t, expect(html).ToHaveAttribute("data-theme", "dark"))
	must(t, role(root, "button", "切换到浅色主题", true).Click())
	must(t, expect(html).ToHaveAttribute("data-theme", "light"))
}
