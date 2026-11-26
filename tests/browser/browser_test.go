package browser

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pw "github.com/mxschmitt/playwright-go"
)

var (
	binaryPath = flag.String("binary", "", "path to the Server or desktoptest executable")
	desktopRun = flag.Bool("desktop", false, "attach to the WebView2 desktop test build")
	videoPath  = flag.String("video", "", "optional MP4 fixture with an audio track")
	lightShot  = flag.String("screenshot", "", "save a light-theme screenshot")
	darkShot   = flag.String("dark-screenshot", "", "save a dark-theme screenshot")
	mobileShot = flag.String("mobile-screenshot", "", "save a narrow-window screenshot")
)

var expect = pw.NewPlaywrightAssertions(25_000).Locator

const fixtureASRKey = "browser-asr-key"
const fixtureSummaryKey = "browser-summary-key"

// TestBrowserSmoke exercises the same workspace through Edge and a second Edge or WebView2 client.
func TestBrowserSmoke(t *testing.T) {
	if *binaryPath == "" {
		t.Skip("use -args -binary <path> to enable browser acceptance")
	}
	binary, err := filepath.Abs(*binaryPath)
	must(t, err)
	_, err = os.Stat(binary)
	must(t, err)
	var video []byte
	if *videoPath != "" {
		video, err = os.ReadFile(*videoPath)
		must(t, err)
	}
	audio := audioFixture()
	provider := httptest.NewServer(http.HandlerFunc(providerDouble))
	t.Cleanup(provider.Close)
	url, cdp := startApp(t, binary)

	// Edge is already installed. Only the Playwright driver is needed.
	options := &pw.RunOptions{SkipInstallBrowsers: true}
	driver, err := pw.Run(options)
	if err != nil {
		must(t, pw.Install(options))
		driver, err = pw.Run(options)
		must(t, err)
	}
	t.Cleanup(func() { _ = driver.Stop() })
	edge, err := driver.Chromium.Launch(pw.BrowserTypeLaunchOptions{Channel: pw.String("msedge")})
	must(t, err)
	t.Cleanup(func() { _ = edge.Close() })
	page, err := edge.NewPage(pw.BrowserNewPageOptions{
		Viewport: &pw.Size{Width: 1200, Height: 810}, ColorScheme: pw.ColorSchemeDark,
	})
	must(t, err)
	var peer pw.Page
	if cdp != "" {
		desktop, err := driver.Chromium.ConnectOverCDP(cdp)
		must(t, err)
		t.Cleanup(func() { _ = desktop.Close() })
		contexts := desktop.Contexts()
		if len(contexts) == 0 || len(contexts[0].Pages()) == 0 {
			t.Fatal("WebView2 did not expose a page")
		}
		peer = contexts[0].Pages()[0]
		must(t, expect(peer.Locator(".brand")).ToBeVisible())
		must(t, expect(peer.Locator("html")).ToHaveAttribute("data-platform", "windows"))
		checkPageCondition(t, peer, `document.documentElement.dataset.theme === (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')`)
	} else {
		peer, err = edge.NewPage()
		must(t, err)
	}
	var pageErrors []error
	var errorMu sync.Mutex
	page.OnPageError(func(err error) {
		errorMu.Lock()
		pageErrors = append(pageErrors, err)
		errorMu.Unlock()
	})

	checkThemes(t, page, url)
	checkSharedWorkspace(t, page, peer, url, provider.URL, audio)
	checkPinnedPlayback(t, page)
	checkNotesDraft(t, page, url)
	checkRecordingShortcut(t, page)
	saveScreenshot(t, page, *lightShot)
	if *darkShot != "" {
		must(t, role(page.Locator("body"), "button", "切换到深色主题", true).Click())
		must(t, expect(page.Locator("html")).ToHaveAttribute("data-theme", "dark"))
		must(t, expect(page.Locator(".library")).ToHaveCSS("background-color", "rgb(28, 28, 28)"))
		saveScreenshot(t, page, *darkShot)
	}
	checkNarrowWorkspace(t, edge, url)
	checkShortWorkspace(t, edge, url)
	checkBatchImport(t, page, audio, video)
	checkFFmpegGuide(t, page, audio, video)
	checkPureTranscript(t, page, audio)
	checkModelChoices(t, page)
	checkSummaryChoices(t, page, url)
	errorMu.Lock()
	defer errorMu.Unlock()
	for _, err := range pageErrors {
		t.Errorf("page error: %v", err)
	}
}

// providerDouble returns deterministic local ASR and summary responses.
func providerDouble(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var result any
	switch r.URL.Path {
	case "/v1/audio/transcriptions":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		marks := r.MultipartForm.Value["timestamp_granularities[]"]
		if r.Header.Get("Authorization") != "Bearer "+fixtureASRKey ||
			r.FormValue("response_format") != "verbose_json" || len(marks) != 2 || len(r.MultipartForm.File["file"]) != 1 {
			http.Error(w, "invalid ASR fixture request", http.StatusBadRequest)
			return
		}
		segments := []map[string]any{{"text": "下周发布", "speaker": "李工", "start": 0, "end": 1.2}}
		for index := range 24 {
			start := 1.2 + float64(index)*0.03
			segments = append(segments, map[string]any{
				"text": "确认验收时间，更新项目计划。", "speaker": "王工", "start": start, "end": start + 0.03,
			})
		}
		result = map[string]any{
			"segments": segments,
			"words": []map[string]any{
				{"word": "下周", "start": 0, "end": 0.5},
				{"word": "发布", "start": 0.5, "end": 1.2},
			},
		}
	case "/api/chat":
		var input struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || r.Header.Get("Authorization") != "Bearer "+fixtureSummaryKey ||
			len(input.Messages) == 0 || !strings.Contains(input.Messages[0].Content, "负责人") {
			http.Error(w, "invalid summary fixture request", http.StatusBadRequest)
			return
		}
		result = map[string]any{"message": map[string]string{"content": "## 决策\n- 下周发布，由李工负责。"}}
	default:
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

// audioFixture creates an in-memory two-second mono PCM WAV upload.
func audioFixture() pw.InputFile {
	const samples = 64000
	wav := make([]byte, 44+samples)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], samples)
	return pw.InputFile{Name: "fixture.wav", MimeType: "audio/wav", Buffer: wav}
}

// startApp starts an isolated application process and waits for its HTTP and optional CDP ports.
func startApp(t *testing.T, binary string) (string, string) {
	t.Helper()
	root := t.TempDir()
	address := loopbackAddress(t)
	args := []string{"--listen", address, "--data-dir", filepath.Join(root, "library")}
	var cdp string
	env := append(os.Environ(), "SHENGJI_ASR_OPENAI_KEY=", "SHENGJI_LLM_API_KEY=")
	if *desktopRun {
		debug := loopbackAddress(t)
		cdp = "http://" + debug
		env = append(env, "SHENGJI_TEST_CDP_PORT="+strings.TrimPrefix(debug, "127.0.0.1:"))
	} else {
		args = append(args, "--browser-only")
	}
	log, err := os.Create(filepath.Join(root, "application.log"))
	must(t, err)
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(binary, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, log, log
	must(t, cmd.Start())
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	url := "http://" + address
	waitReady(t, url+"/api/health", log.Name(), done)
	if cdp != "" {
		waitReady(t, cdp+"/json/version", log.Name(), done)
	}
	return url, cdp
}

// loopbackAddress finds a free local TCP address for the isolated test.
func loopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	must(t, err)
	address := listener.Addr().String()
	must(t, listener.Close())
	return address
}

// waitReady bounds process startup and prints application diagnostics when startup fails.
func waitReady(t *testing.T, url, logPath string, done <-chan struct{}) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			log, _ := os.ReadFile(logPath)
			t.Fatalf("timed out waiting for %s\n%s", url, log)
		case <-done:
			log, _ := os.ReadFile(logPath)
			t.Fatalf("application exited while waiting for %s\n%s", url, log)
		case <-ticker.C:
			response, err := client.Get(url)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return
				}
			}
		}
	}
}

// role locates accessible controls, optionally matching their names exactly.
func role(root pw.Locator, kind pw.AriaRole, name string, exact ...bool) pw.Locator {
	options := pw.LocatorGetByRoleOptions{Name: name}
	if len(exact) != 0 {
		options.Exact = &exact[0]
	}
	return root.GetByRole(kind, options)
}

// selectValue selects an option using the value persisted by the application.
func selectValue(t *testing.T, target pw.Locator, value string) {
	t.Helper()
	_, err := target.SelectOption(pw.SelectOptionValues{Values: &[]string{value}})
	must(t, err)
}

// saveScreenshot optionally records a full-page screenshot for manual inspection.
func saveScreenshot(t *testing.T, page pw.Page, path string) {
	t.Helper()
	if path != "" {
		_, err := page.Screenshot(pw.PageScreenshotOptions{Path: &path, FullPage: pw.Bool(true)})
		must(t, err)
	}
}

// request sends a local API request and verifies its HTTP status.
func request(t *testing.T, method, url string, payload any, status int) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	must(t, err)
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	must(t, err)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(req)
	must(t, err)
	if response.StatusCode != status {
		_ = response.Body.Close()
		t.Fatalf("%s %s returned %s, want %d", method, url, response.Status, status)
	}
	return response
}

// openSettings waits for workspace refresh before opening the model settings dialog.
func openSettings(t *testing.T, page pw.Page) pw.Locator {
	t.Helper()
	_, err := page.WaitForFunction("() => !state.busy", nil)
	must(t, err)
	must(t, role(page.Locator("body"), "button", "模型设置", true).Click())
	dialog := page.Locator("#settings-dialog")
	must(t, expect(dialog).ToBeVisible())
	return dialog
}

// must reports an automation error at its calling step.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
