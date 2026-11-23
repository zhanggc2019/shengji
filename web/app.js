"use strict";

const $ = (id) => document.getElementById(id);
const panelNames = ["transcript", "summary", "notes"];
const meetingPreference = "shengji.meeting";
const maxImportFiles = 20;
const videoExtension = /\.(?:mp4|mov|m4v|mkv|avi)$/i;
const senseVoiceMirror = "https://www.modelscope.cn/api/v1/models/WEAAEW/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/repo";
const localModelHelp = new Map([
  ["sensevoice", {
    name: "SenseVoice",
    scenario: "粤语、日语或韩语录音优先选 SenseVoice。它也支持普通话和英语。",
    download: senseVoiceMirror + "?Revision=860e3f03644fb4d979bfd93c4ef345c93c0feaf8&FilePath=model.int8.onnx",
    tokens: senseVoiceMirror + "?Revision=6652fb804909a130d070d533690dd578e983784d&FilePath=tokens.txt",
    instructions: "从魔搭社区下载模型和词表。把 model.int8.onnx 与 tokens.txt 放在声记服务主机的同一个文件夹，再填写「模型包目录」。",
  }],
  ["paraformer", {
    name: "Paraformer",
    scenario: "以普通话、河南话、天津话或四川话为主的录音优先选 Paraformer。它也支持英语。",
    download: "https://gh-proxy.com/https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-paraformer-zh-2023-09-14.tar.bz2",
    instructions: "从社区加速镜像下载模型包。在声记服务主机上解压，将包含 model.int8.onnx 和 tokens.txt 的文件夹填入「模型包目录」。",
  }],
]);
let noticeTimeout;
let importQueue = [];
let importFFmpeg = "checking";
let ffmpegCheck;
const state = {
  id: "", meeting: null, list: [], query: "", page: 0, revision: -1, listVersion: 0,
  recording: {state: "idle"}, busy: false, rename: false, segment: -1, online: false,
  lastLine: null, lastWord: null, asrProfiles: [], summaryProfiles: [], initialized: false, noteDrafts: new Map(),
};

// api keeps the same origin and error semantics in the desktop window and the browser.
async function api(path, method = "GET", body) {
  const options = {method};
  if (body instanceof FormData) options.body = body;
  else if (body !== undefined) {
    options.headers = {"Content-Type": "application/json"};
    options.body = JSON.stringify(body);
  }
  const response = await fetch(path, options);
  if (response.status === 204) return null;
  const result = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(result.error || "请求失败（" + response.status + "）");
  return result;
}

// pathFor constructs a stable meeting URL only from IDs returned by Go.
function pathFor(id = state.id) {
  return "/api/meetings/" + encodeURIComponent(id);
}

// notify shows errors inside the active dialog or at the top of the workspace.
function notify(message, tone = "warning") {
  window.clearTimeout(noticeTimeout);
  const dialog = document.querySelector("dialog[open]");
  const banner = $("notice");
  banner.textContent = dialog ? "" : message;
  banner.hidden = Boolean(dialog) || !message;
  banner.dataset.tone = tone;
  if (!dialog) {
    if (message && tone === "success") noticeTimeout = window.setTimeout(() => notify(""), 3200);
    return;
  }
  let slot = dialog.querySelector(".dialog-error");
  if (!slot) {
    slot = document.createElement("p");
    slot.className = "dialog-error danger";
    slot.setAttribute("role", "alert");
    dialog.querySelector("h2").after(slot);
  }
  slot.textContent = message;
  slot.hidden = !message;
}

// run prevents overlapping commands and reports any failure to the current view.
async function run(action, background = false) {
  if (state.busy) return;
  state.busy = true;
  const submitButtons = background ? [] : document.querySelectorAll("dialog[open] button:not([data-close]):enabled");
  if (!background) {
    submitButtons.forEach((button) => { button.disabled = true; });
    notify("");
  }
  try { await action(); }
  catch (error) { notify(error.message); }
  finally {
    state.busy = false;
    submitButtons.forEach((button) => { button.disabled = false; });
    renderControls();
  }
}

// formatClock formats seconds without losing the hour on long meetings.
function formatClock(seconds) {
  const value = Math.max(0, Math.floor(seconds || 0));
  const parts = [Math.floor(value / 3600), Math.floor(value / 60) % 60, value % 60];
  if (!parts[0]) parts.shift();
  return parts.map((part) => String(part).padStart(2, "0")).join(":");
}

// elem builds text nodes so imported transcripts and model output cannot inject HTML.
function elem(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

// formatDate shows the local time of the current library entry.
function formatDate(value) {
  return new Date(value).toLocaleString("zh-CN", {dateStyle: "medium", timeStyle: "short"});
}

// setLibraryOpen controls the narrow-screen library disclosure.
function setLibraryOpen(open) {
  document.querySelector(".workspace").toggleAttribute("data-library-open", open);
  $("library-toggle").setAttribute("aria-expanded", String(open));
}

// recentMeeting reads the remembered workspace selection, if storage is available.
function recentMeeting() {
  try { return localStorage.getItem(meetingPreference) || ""; }
  catch { return ""; }
}

// rememberMeeting keeps the most recent selection across restarts.
function rememberMeeting(id) {
  try {
    if (id) localStorage.setItem(meetingPreference, id);
    else localStorage.removeItem(meetingPreference);
  } catch {}
}

// renderList presents search results without overwriting the current selection.
function renderList() {
  const list = $("meeting-list");
  list.replaceChildren();
  if (!state.list.length) list.append(elem("p", state.query ? "没有匹配的会议" : "暂无会议"));
  for (const meeting of state.list) {
    const button = elem("button", undefined, "meeting-item");
    button.classList.toggle("selected", meeting.id === state.id);
    if (meeting.id === state.id) button.setAttribute("aria-current", "true");
    button.append(elem("span", meeting.title), elem("small", formatDate(meeting.created_at) +
      (meeting.duration ? " · " + formatClock(meeting.duration) : "")));
    button.addEventListener("click", () => run(async () => {
      await selectMeeting(meeting.id);
      if (matchMedia("(max-width: 655px)").matches) {
        setLibraryOpen(false);
        $("title").focus();
      }
    }));
    list.append(button);
  }
}

// refreshList drops stale results if the user changed the search while a request was pending.
async function refreshList(append = false) {
  const version = ++state.listVersion;
  if (!append) state.page = 0;
  const result = await api("/api/meetings?q=" + encodeURIComponent(state.query) + "&page=" + state.page);
  if (version !== state.listVersion) return;
  state.list = append ? state.list.concat(result) : result;
  $("more-button").hidden = result.length < 50;
  renderList();
}

// selectMeeting updates only the meeting still selected when the response arrives.
async function selectMeeting(id) {
  const meeting = id ? await api(pathFor(id)) : null;
  if (state.id !== id) showPanel("transcript");
  state.id = id;
  rememberMeeting(id);
  renderMeeting(meeting);
  renderList();
}

// refreshCurrent refreshes only on mutations from the WebSocket rather than at the meter frequency.
async function refreshCurrent() {
  await Promise.all([refreshList(), refreshASRStatus().catch(() => {})]);
  const initial = !state.initialized;
  const fallback = state.list[0]?.id || "";
  const id = state.id || (initial ? recentMeeting() || fallback : "");
  state.initialized = true;
  if (!id) return;
  try { await selectMeeting(id); }
  catch (error) {
    if (error.message !== "会议不存在") throw error;
    await selectMeeting(fallback !== id ? fallback : "");
  }
}

// renderASRStatus describes the configured model for the next transcription.
function renderASRStatus(config) {
  const service = state.asrProfiles.find((profile) => profile.id === config.asr_provider);
  const model = service?.local ? localModelHelp.get(config.asr_model)?.name || config.asr_model : config.asr_model;
  const label = service?.local ? "本机离线" : service?.name || config.asr_provider;
  const pending = service?.local && !config.asr_model_dir ? "（待配置）" : "";
  $("active-asr").textContent = "转录：" + label + " · " + model + pending;
}

// refreshASRStatus updates the footer after a settings change in either client.
async function refreshASRStatus() {
  const [config, profiles] = await Promise.all([
    api("/api/settings"),
    state.asrProfiles.length ? state.asrProfiles : api("/api/asr/profiles"),
  ]);
  state.asrProfiles = profiles;
  renderASRStatus(config);
}

// connect opens a local event channel and reconnects when a browser resumes.
function connect() {
  const socket = new WebSocket((location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + "/ws");
  socket.onopen = () => {
    state.revision = -1;
    state.online = true;
    setConnection("已连接");
  };
  socket.onmessage = (message) => {
    const event = JSON.parse(message.data);
    state.recording = event.recording;
    renderControls();
    if (event.revision !== state.revision) {
      if (state.busy) return;
      state.revision = event.revision;
      run(refreshCurrent, true);
    }
  };
  socket.onclose = () => {
    state.online = false;
    setConnection("正在重新连接");
    window.setTimeout(connect, 1800);
  };
}

// setConnection keeps connection state accessible as text and a colored indicator.
function setConnection(text) {
  const label = $("connection");
  label.replaceChildren(elem("span", undefined, "connection-dot"), document.createTextNode(text));
  label.classList.toggle("online", state.online);
}

// showPanel exposes the selected view while preserving unsaved text.
function showPanel(name) {
  for (const panel of panelNames) {
    const selected = panel === name;
    const tab = $(panel + "-tab");
    $(panel + "-panel").hidden = !selected;
    tab.setAttribute("aria-selected", String(selected));
    tab.tabIndex = selected ? 0 : -1;
  }
}

// navigatePanels follows standard keyboard navigation for a horizontal tab list.
function navigatePanels(event) {
  const current = panelNames.indexOf(event.target.id.replace("-tab", ""));
  if (current < 0) return;
  const length = panelNames.length;
  const destinations = {ArrowRight: (current + 1) % length, ArrowLeft: (current + length - 1) % length, Home: 0, End: length - 1};
  const next = destinations[event.key];
  if (next === undefined) return;
  event.preventDefault();
  showPanel(panelNames[next]);
  $(panelNames[next] + "-tab").focus();
}

// renderJob reports the latest request without replacing existing results on failure.
function renderJob(kind, target) {
  const node = $(target);
  const job = (state.meeting?.jobs || []).find((entry) => entry.kind === kind);
  const text = job && {queued: "等待处理", running: "正在处理…", completed: "已完成", failed: job.error}[job.state];
  node.textContent = text || "";
  node.classList.toggle("danger", job?.state === "failed");
  node.hidden = !text;
}

// renderMeeting updates the view without resetting a playing recording or unsaved notes.
function renderMeeting(meeting) {
  state.meeting = meeting;
  $("detail").hidden = !meeting;
  $("empty").hidden = Boolean(meeting);
  const player = $("audio-player");
  if (!meeting) {
    player.pause();
    player.removeAttribute("src");
    return;
  }
  $("title").textContent = meeting.title;
  const transcriptOnly = meeting.mode === "transcript";
  $("date").textContent = formatDate(meeting.created_at) + (transcriptOnly ? " · 纯转录" : "") +
    (meeting.recording === "recovered" ? " · 已恢复录音" : "");
  document.querySelector(".tabs").hidden = transcriptOnly;
  if (transcriptOnly) showPanel("transcript");
  const audioURL = pathFor() + "/audio";
  player.hidden = !meeting.has_audio;
  $("download-audio").hidden = !meeting.has_audio;
  if (meeting.has_audio) {
    if (player.getAttribute("src") !== audioURL) player.src = audioURL;
    $("download-audio").href = audioURL + "?download=1";
  } else if (player.hasAttribute("src")) {
    player.pause();
    player.removeAttribute("src");
    player.load();
  }
  const notes = state.noteDrafts.get(meeting.id) ?? meeting.notes;
  if ($("notes").value !== notes) $("notes").value = notes;
  renderTranscriptionInfo(meeting);
  renderSegments(meeting.segments || []);
  renderSummary(meeting.summary);
  renderJob("transcription", "transcribe-status");
  renderJob("summary", "summary-status");
  renderControls();
}

// transcriptionMeasurements formats one run's timing consistently across the panel and history.
function transcriptionMeasurements(metrics, knownDuration) {
  const duration = metrics?.audio_seconds || knownDuration;
  const parts = duration > 0 ? ["音频时长 " + formatClock(duration)] : [];
  if (metrics) parts.push("转录耗时 " + (metrics.elapsed_ms / 1000).toFixed(2) + " 秒");
  return parts.join(" · ");
}

// renderTranscriptionInfo shows the model and timing of completed runs.
function renderTranscriptionInfo(meeting) {
  const source = $("transcription-source");
  source.hidden = !meeting.segments?.length;
  source.textContent = source.hidden ? "" : "转录模型：" + (meeting.transcription_source?.label || "未记录");
  const timing = $("transcription-metrics");
  timing.textContent = source.hidden ? "" : transcriptionMeasurements(meeting.transcription_metrics, meeting.duration);
  timing.hidden = !timing.textContent;
  const runs = (meeting.jobs || []).filter((job) => job.transcription_metrics);
  $("transcription-history").hidden = runs.length < 2;
  $("transcription-runs").replaceChildren(...runs.map((job) => elem("li",
    (job.transcription_source?.label || "未记录") + " · " + transcriptionMeasurements(job.transcription_metrics, meeting.duration))));
}

// recordAudioDuration saves browser media metadata when the uploader could not read it.
async function recordAudioDuration() {
  const duration = $("audio-player").duration;
  const meeting = state.meeting;
  if (!meeting?.has_audio || meeting.duration || !Number.isFinite(duration) || duration <= 0) return;
  try {
    const saved = await api(pathFor(meeting.id), "PATCH", {duration});
    if (state.id === meeting.id && !state.meeting.duration) {
      state.meeting.duration = saved.duration;
      renderTranscriptionInfo(state.meeting);
      renderControls();
    }
  } catch {}
}

// renderControls mirrors the authoritative recorder and job state in all open windows.
function renderControls() {
  const current = state.recording;
  const meeting = state.meeting;
  const active = Boolean(current.meeting_id);
  const owns = active && current.meeting_id === meeting?.id;
  const pending = (meeting?.jobs || []).some((job) => job.state === "running" || job.state === "queued");
  const badge = $("recording-badge");
  const liveText = active ? ({recording: "录音中", paused: "已暂停", error: "录音中断，仍可保存"}[current.state] || "") : "";
  if (badge.textContent !== liveText) badge.textContent = liveText;
  if (badge.title !== (current.error || "")) badge.title = current.error || "";
  const audioStatus = $("audio-status");
  const audioText = owns ? liveText : meeting?.has_audio ? "已保存，可回听" : active ? "正在录制另一场会议" : "待录音";
  if (audioStatus.textContent !== audioText) audioStatus.textContent = audioText;
  const timer = $("duration");
  const clock = formatClock(owns ? current.seconds : meeting?.duration);
  if (timer.textContent !== clock) timer.textContent = clock;
  $("save-notes").disabled = !meeting || (state.noteDrafts.get(meeting.id) ?? meeting.notes) === meeting.notes;
  $("mic-meter").value = current.mic_peak || 0;
  $("system-meter").value = current.system_peak || 0;
  $("meters").hidden = !owns;
  $("audio-options").hidden = owns || Boolean(meeting?.has_audio);
  $("start-button").hidden = owns || Boolean(meeting?.has_audio);
  $("start-button").disabled = active || pending;
  $("pause-button").hidden = !owns || current.state === "error";
  $("pause-button").textContent = current.state === "paused" ? "继续录音" : "暂停";
  $("stop-button").hidden = !owns;
  $("delete-button").disabled = owns || pending;
  $("transcribe-button").disabled = !meeting?.has_audio || pending || owns;
  $("transcribe-button").textContent = meeting?.segments?.length ? "重新转录" : "生成转录";
  $("summary-button").disabled = !meeting?.segments?.length || pending || owns;
  $("summary-button").textContent = meeting?.summary ? "重新生成纪要" : "生成纪要";
}

// appendTimedText keeps the provider's original punctuation and normalization while linking matched tokens.
function appendTimedText(paragraph, segment) {
  const text = segment.text;
  const folded = text.toLocaleLowerCase();
  let cursor = 0;
  for (const [index, word] of (segment.words || []).entries()) {
    const label = word.word.trim();
    if (!label) continue;
    const offset = folded.indexOf(label.toLocaleLowerCase(), cursor);
    if (offset < cursor) continue;
    paragraph.append(document.createTextNode(text.slice(cursor, offset)));
    const token = elem("button", text.slice(offset, offset + label.length), "token");
    token.dataset.word = String(index);
    token.addEventListener("click", () => seek(word.start));
    paragraph.append(token);
    cursor = offset + label.length;
  }
  paragraph.append(document.createTextNode(text.slice(cursor)));
}


// renderSegments builds time links and opens an editor without interpolating user text.
function renderSegments(segments) {
  const transcript = $("transcript");
  transcript.replaceChildren();
  state.lastLine = null;
  state.lastWord = null;
  if (!segments.length) transcript.append(elem("p", "暂无转录。录制或导入音视频后可生成。", "empty-text"));
  for (const [index, segment] of segments.entries()) {
    const row = elem("div", undefined, "segment");
    const meta = elem("div");
    const stamp = elem("button", formatClock(segment.start), "stamp");
    stamp.title = "播放此处";
    stamp.disabled = !(segment.end > segment.start);
    stamp.addEventListener("click", () => seek(segment.start));
    meta.append(stamp);
    if (state.meeting.mode !== "transcript") meta.append(elem("small", segment.speaker || "发言人"));
    const paragraph = elem("p");
    appendTimedText(paragraph, segment);
    const edit = elem("button", "编辑");
    edit.setAttribute("aria-label", "编辑转录片段 " + (index + 1));
    edit.addEventListener("click", () => openSegment(index));
    row.append(meta, paragraph, edit);
    transcript.append(row);
  }
  highlight();
}

// renderSummary displays the common Markdown headings and lists as safe text.
function renderSummary(markdown) {
  const node = $("summary");
  node.replaceChildren();
  if (!markdown) {
    node.append(elem("p", "生成转录后可整理会议概要、决策和行动项。", "empty-text"));
    return;
  }
  for (const line of markdown.split("\n")) {
    if (/^#{1,6}\s/.test(line)) node.append(elem("h2", line.replace(/^#{1,6}\s+/, "")));
    else node.append(elem("div", line.replace(/^[-*]\s+/, "• ") || "\u00a0"));
  }
}

// seek plays from a transcription timestamp when a recording is available.
function seek(seconds) {
  const player = $("audio-player");
  if (player.hidden || player.readyState === 0) return;
  player.currentTime = seconds;
  player.play().catch(() => {});
}

// highlight tracks the current segment and word without rebuilding the transcript.
function highlight() {
  const player = $("audio-player");
  const segments = state.meeting?.segments || [];
  const now = player.currentTime;
  const index = segments.findIndex((segment) => segment.start <= now && now < segment.end);
  const row = index < 0 ? null : $("transcript").children[index];
  if (state.lastLine !== row) {
    state.lastLine?.classList.remove("active");
    row?.classList.add("active");
    state.lastLine = row;
  }
  const words = index < 0 ? [] : (segments[index].words || []);
  const wordIndex = words.findIndex((word) => word.start <= now && now < word.end);
  const word = wordIndex < 0 ? null : row?.querySelector('[data-word="' + wordIndex + '"]');
  if (state.lastWord !== word) {
    state.lastWord?.classList.remove("active");
    word?.classList.add("active");
    state.lastWord = word;
  }
}

// record sends only control commands over HTTP; status is shared through the event stream.
async function record(action) {
  const options = action === "start" ? {system_audio: $("system-audio").checked, auto_gain: $("auto-gain").checked} : undefined;
  try { renderMeeting(await api(pathFor() + "/record/" + action, "POST", options)); }
  finally { await refreshList(); }
}

// startJob submits background work and refreshes its durable status.
async function startJob(kind) {
  await api(pathFor() + "/jobs/" + kind, "POST", kind === "transcription" ? {language: $("language").value} : undefined);
  await selectMeeting(state.id);
}

// openMeetingDialog uses the same title form for creation and renaming.
function openMeetingDialog(rename = false) {
  state.rename = rename;
  $("meeting-dialog-title").textContent = rename ? "重命名记录" : "新建记录";
  $("meeting-mode-field").hidden = rename;
  if (!rename) $("meeting-mode").value = "meeting";
  $("meeting-title").value = rename ? state.meeting.title : "";
  $("meeting-dialog").showModal();
  notify("");
  $("meeting-title").focus();
}

// openSegment loads the selected line into the safe text editor.
function openSegment(index) {
  state.segment = index;
  const segment = state.meeting.segments[index];
  const transcriptOnly = state.meeting.mode === "transcript";
  $("segment-speaker-label").hidden = $("segment-speaker").hidden = $("segment-rename-label").hidden = transcriptOnly;
  $("segment-speaker").value = segment.speaker || "";
  $("segment-text").value = segment.text;
  $("segment-rename").checked = false;
  $("segment-dialog").showModal();
  notify("");
}

// openSettings retrieves the current model configuration without exposing keys.
async function openSettings() {
  const [config, profiles, summaries] = await Promise.all([
    api("/api/settings"), api("/api/asr/profiles"), api("/api/summary/profiles"),
  ]);
  state.asrProfiles = profiles;
  state.summaryProfiles = summaries;
  $("asr-provider").replaceChildren(...profiles.map((profile) => {
    const option = elem("option", profile.name);
    option.value = profile.id;
    return option;
  }));
  $("provider").replaceChildren(...summaries.map((profile) => {
    const option = elem("option", profile.name);
    option.value = profile.id;
    return option;
  }));
  const form = $("settings-form");
  for (const [key, value] of Object.entries(config)) form.elements.namedItem(key).value = value;
  updateASRSettings(false);
  updateSummarySettings(false);
  $("settings-dialog").showModal();
  notify("");
}

// updateASRSettings switches between HTTP connection fields and the local model directory.
function updateASRSettings(usePreset) {
  const profile = state.asrProfiles.find((entry) => entry.id === $("asr-provider").value);
  if (!profile) return;
  const local = profile.local;
  $("asr-url").hidden = $("asr-url-label").hidden = local;
  $("asr-url").required = !local;
  $("asr-url").disabled = local;
  $("asr-model-dir").hidden = $("asr-model-dir-label").hidden = !local;
  $("asr-model-dir").disabled = !local;
  $("asr-model").hidden = local;
  $("asr-model").required = !local;
  $("asr-local-model").hidden = $("asr-local-model").disabled = !local;
  $("asr-model-label").htmlFor = local ? "asr-local-model" : "asr-model";
  $("asr-model-guide").hidden = !local;
  if (usePreset) {
    $("asr-url").value = profile.url;
    $("asr-model").value = profile.model;
  }
  if (local) {
    const model = $("asr-model").value;
    $("asr-local-model").value = localModelHelp.has(model) ? model : profile.model;
    updateLocalModelHelp();
  }
}

// updateSummarySettings switches from manual endpoint inputs to preset model choices.
function updateSummarySettings(usePreset) {
  const profile = state.summaryProfiles.find((entry) => entry.id === $("provider").value);
  if (!profile) return;
  const preset = Boolean(profile.models?.length);
  const url = $("llm-url");
  const model = $("llm-model");
  const picker = $("llm-preset-model");
  const wasPreset = !picker.hidden;
  url.hidden = url.disabled = $("llm-url-label").hidden = preset;
  url.required = !preset;
  model.hidden = preset;
  model.required = !preset;
  picker.hidden = picker.disabled = !preset;
  $("llm-model-label").htmlFor = preset ? "llm-preset-model" : "llm-model";
  const hint = $("llm-key-help");
  hint.hidden = !preset;
  if (preset) {
    const choices = [...profile.models];
    if (!usePreset && model.value && !choices.includes(model.value)) choices.push(model.value);
    picker.replaceChildren(...choices.map((name) => {
      const option = elem("option", name);
      option.value = name;
      return option;
    }));
    picker.value = usePreset ? choices[0] : model.value || choices[0];
    model.value = picker.value;
    hint.textContent = "在运行声记的电脑首次配置 " + profile.key_env + " 后，可直接选择模型。";
  } else if (usePreset && wasPreset) {
    url.value = profile.id === "ollama" ? "http://127.0.0.1:11434" : "";
    model.value = profile.id === "ollama" ? "qwen3:8b" : "";
  }
}

// updateLocalModelHelp keeps the saved model, download link and use case aligned.
function updateLocalModelHelp() {
  const model = $("asr-local-model").value;
  const info = localModelHelp.get(model);
  if (!info) return;
  $("asr-model").value = model;
  $("asr-model-use").textContent = info.scenario;
  const download = $("asr-model-download");
  download.href = info.download;
  download.textContent = "下载 " + info.name + " 模型";
  download.hidden = false;
  const tokens = $("asr-token-download");
  tokens.hidden = !info.tokens;
  if (info.tokens) tokens.href = info.tokens;
  else tokens.removeAttribute("href");
  $("asr-download-help").textContent = info.instructions;
}

// onSubmit prevents full navigation while a dialog saves its operation.
function onSubmit(id, submit) {
  $(id).addEventListener("submit", (event) => {
    event.preventDefault();
    run(() => submit(event.currentTarget));
  });
}

// canImportFile keeps video selections pending while the service lacks FFmpeg.
function canImportFile({file}) {
  return importFFmpeg === "ready" || !videoExtension.test(file.name);
}

// showImportQueue renders available files, waiting videos and individual errors.
function showImportQueue() {
  const list = $("import-files");
  const count = importQueue.length;
  const available = importQueue.filter(canImportFile);
  const waiting = count - available.length;
  list.replaceChildren(...importQueue.slice(0, maxImportFiles).map((entry) => {
    const ready = canImportFile(entry);
    const detail = ready ? entry.error : "等待 FFmpeg";
    return elem("li", entry.file.name + (detail ? " · " + detail : ""), ready && entry.error ? "danger" : undefined);
  }));
  list.hidden = count === 0 || count > maxImportFiles;
  $("import-title-field").hidden = count > 1;
  const submit = $("import-submit");
  submit.disabled = $("import-file").disabled || available.length === 0 || count > maxImportFiles;
  if (waiting && importFFmpeg !== "checking") $("ffmpeg-guide").open = true;
  submit.textContent = waiting && available.length ? "先导入音频" :
    available.some((entry) => entry.error) ? "重试失败文件" : "导入";
}

// checkImportTool rechecks the service host and never requests the selected files.
async function checkImportTool() {
  if (ffmpegCheck) return ffmpegCheck;
  const status = $("ffmpeg-status");
  const recheck = $("ffmpeg-recheck");
  const messages = {
    ready: "已检测到 FFmpeg，可以导入视频。",
    missing: "服务端尚未安装 FFmpeg。音频可以直接导入。",
    unusable: "已找到 FFmpeg，但无法启动。请查看安装指引。",
    unknown: "暂时无法检测 FFmpeg。请点击「重新检测」。",
  };
  importFFmpeg = "checking";
  status.textContent = "正在检测 FFmpeg…";
  recheck.disabled = true;
  showImportQueue();
  ffmpegCheck = api("/api/import/capabilities").then((result) => {
    importFFmpeg = result.ffmpeg;
    const windows = result.platform === "windows";
    document.querySelectorAll('[data-ffmpeg-platform="windows"]').forEach((item) => { item.hidden = !windows; });
    document.querySelector('[data-ffmpeg-platform="other"]').hidden = windows;
    $("ffmpeg-download").href = windows ? "https://ffmpeg.org/download.html#build-windows" : "https://ffmpeg.org/download.html";
  }).catch(() => {
    importFFmpeg = "unknown";
  }).finally(() => {
    status.textContent = messages[importFFmpeg] || messages.unknown;
    $("ffmpeg-unusable-tip").hidden = importFFmpeg !== "unusable";
    recheck.disabled = false;
    ffmpegCheck = undefined;
    showImportQueue();
  });
  return ffmpegCheck;
}

// openImportDialog clears the previous selection so a new batch starts independently.
function openImportDialog() {
  if ($("import-file").disabled) return;
  $("import-form").reset();
  importQueue = [];
  $("ffmpeg-guide").open = false;
  $("import-progress").textContent = "";
  showImportQueue();
  $("import-dialog").showModal();
  notify("");
  void checkImportTool();
}

// selectImportFiles records up to 20 local files for individual bounded uploads.
function selectImportFiles() {
  importQueue = Array.from($("import-file").files, (file) => ({file, error: ""}));
  if (importQueue.length > 1) $("import-title").value = "";
  $("import-progress").textContent = importQueue.length ? "已选择 " + importQueue.length + " 个文件" : "";
  showImportQueue();
  notify(importQueue.length > maxImportFiles ? "一次最多选择 20 个文件，请重新选择。" : "");
}

// importSelectedFiles streams files sequentially and keeps only failed items available for retry.
async function importSelectedFiles() {
  if (!importQueue.length || importQueue.length > maxImportFiles) return;
  const files = importQueue.filter(canImportFile);
  if (!files.length) return;
  const waiting = importQueue.filter((entry) => !canImportFile(entry));
  const useTitle = importQueue.length === 1;
  const dialog = $("import-dialog");
  const progress = $("import-progress");
  const fileInput = $("import-file");
  const title = $("import-title");
  const close = $("import-close");
  const mode = $("import-mode");
  const failures = [];
  let first;
  fileInput.disabled = title.disabled = close.disabled = mode.disabled = true;
  $("import-submit").disabled = true;
  notify("");
  try {
    for (const [index, entry] of files.entries()) {
      progress.textContent = "正在导入 " + (index + 1) + " / " + files.length + " · " + entry.file.name;
      const body = new FormData();
      body.append("mode", mode.value);
      if (useTitle) body.append("title", title.value);
      body.append("audio", entry.file);
      try {
        const meeting = await api("/api/import", "POST", body);
        first ??= meeting.id;
      } catch (error) {
        failures.push({file: entry.file, error: error.message});
      }
    }
    const processed = files.length - failures.length;
    importQueue = [...waiting, ...failures];
    progress.textContent = "已导入 " + processed + " 个文件";
    if (failures.length) {
      notify(failures.length + " 个文件导入失败，可单独重试。");
    } else if (!waiting.length) {
      dialog.close();
      notify(progress.textContent, "success");
    }
    if (first) {
      await selectMeeting(first);
      await refreshList();
    }
  } finally {
    fileInput.disabled = title.disabled = close.disabled = mode.disabled = false;
    showImportQueue();
  }
  if (failures.some(({error}) => error.includes("需要 FFmpeg"))) void checkImportTool();
}

// wireEvents binds the shared browser UI to the local Go API.
function wireEvents() {
  $("new-button").onclick = $("empty-new").onclick = () => openMeetingDialog();
  $("edit-title").onclick = () => { $("meeting-menu").open = false; openMeetingDialog(true); };
  $("library-toggle").onclick = (event) => setLibraryOpen(event.currentTarget.getAttribute("aria-expanded") !== "true");
  document.addEventListener("pointerdown", (event) => {
    if (!$("meeting-menu").contains(event.target)) $("meeting-menu").open = false;
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !$("meeting-menu").open) return;
    $("meeting-menu").open = false;
    $("meeting-menu").querySelector("summary").focus();
  });
  $("import-button").onclick = $("empty-import").onclick = openImportDialog;
  $("import-file").onchange = selectImportFiles;
  $("ffmpeg-recheck").onclick = checkImportTool;
  $("import-dialog").addEventListener("cancel", (event) => {
    if ($("import-file").disabled) event.preventDefault();
  });
  $("settings-button").onclick = () => run(openSettings);
  $("asr-provider").onchange = () => updateASRSettings(true);
  $("asr-local-model").onchange = updateLocalModelHelp;
  $("provider").onchange = () => updateSummarySettings(true);
  $("llm-preset-model").onchange = (event) => { $("llm-model").value = event.target.value; };
  $("more-button").onclick = () => run(async () => { state.page++; await refreshList(true); });
  let searchTimer;
  $("search").oninput = () => {
    state.query = $("search").value;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => refreshList().catch((error) => notify(error.message)), 200);
  };
  for (const button of document.querySelectorAll("[data-close]")) {
    button.onclick = () => button.closest("dialog").close();
  }
  for (const panel of panelNames) $(panel + "-tab").onclick = () => showPanel(panel);
  document.querySelector(".tabs").addEventListener("keydown", navigatePanels);
  $("start-button").onclick = () => run(() => record("start"));
  $("pause-button").onclick = () => run(() => record(state.recording.state === "paused" ? "resume" : "pause"));
  $("stop-button").onclick = () => run(() => record("stop"));
  $("audio-player").addEventListener("timeupdate", highlight);
  $("audio-player").addEventListener("loadedmetadata", recordAudioDuration);
  $("transcribe-button").onclick = () => run(() => startJob("transcription"));
  $("summary-button").onclick = () => run(() => startJob("summary"));
  $("notes").oninput = () => {
    state.noteDrafts.set(state.id, $("notes").value);
    renderControls();
  };
  $("save-notes").onclick = () => run(async () => {
    const id = state.id;
    const notes = $("notes").value;
    const meeting = await api(pathFor(id), "PATCH", {notes});
    if (state.noteDrafts.get(id) === notes) state.noteDrafts.delete(id);
    if (state.id === id) renderMeeting(meeting);
    notify("笔记已保存", "success");
  });
  $("export-button").onclick = () => {
    const link = elem("a");
    link.href = pathFor() + "/export?format=" + $("export-format").value;
    link.download = "";
    link.click();
  };
  $("delete-button").onclick = () => run(async () => {
    $("meeting-menu").open = false;
    if (!window.confirm("删除「" + state.meeting.title + "」及其音频？")) return;
    await api(pathFor(), "DELETE");
    state.noteDrafts.delete(state.id);
    await selectMeeting("");
    await refreshList();
  });
  onSubmit("meeting-form", async () => {
    const details = {title: $("meeting-title").value};
    if (!state.rename) details.mode = $("meeting-mode").value;
    const result = await api(state.rename ? pathFor() : "/api/meetings", state.rename ? "PATCH" : "POST", details);
    $("meeting-dialog").close();
    await selectMeeting(result.id);
    await refreshList();
  });
  $("import-form").addEventListener("submit", (event) => {
    event.preventDefault();
    run(importSelectedFiles, true);
  });
  onSubmit("segment-form", async () => {
    const edit = {text: $("segment-text").value};
    if (state.meeting.mode !== "transcript") {
      edit.speaker = $("segment-speaker").value;
      edit.rename_speaker = $("segment-rename").checked;
    }
    const meeting = await api(pathFor() + "/segments/" + state.segment, "PATCH", edit);
    $("segment-dialog").close();
    renderMeeting(meeting);
  });
  onSubmit("settings-form", async (form) => {
    const saved = await api("/api/settings", "PUT", Object.fromEntries(new FormData(form)));
    renderASRStatus(saved);
    $("settings-dialog").close();
    notify("模型设置已保存", "success");
  });
}

wireEvents();
connect();
run(refreshCurrent, true);
