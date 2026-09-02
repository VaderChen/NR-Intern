const state = {
  agent: null,
  serviceSettings: null,
  providers: [],
  providerSettings: null,
  selectedProviderSettingsID: "",
  providerSettingsDraft: null,
  providerModelLists: {},
  providerModelLoads: {},
  providerModelErrors: {},
  providerTestedModels: {},
  mcpSettings: null,
  selectedMCPSettingsID: "",
  mcpSettingsDraft: null,
  mcpImportDraft: null,
  reverseProxy: null,
  reverseProxyHydrated: false,
  reverseProxyLoading: false,
  permissions: null,
  workspaces: [],
  workspace: null,
  projects: [],
  sessions: [],
  session: null,
  plans: [],
  planTab: "active",
  planEditingID: "",
  expandedPlanIDs: new Set(),
  planExpansionInitialized: false,
  activePlanID: "",
  planDragID: "",
  sessionDragID: "",
  sessionDragProjectID: "",
  sessionOrderSaving: false,
  backendHealthy: false,
  backendCompatible: false,
  backendStatus: null,
  backendCompatibilityError: "",
  backendLifecycleState: "stopped",
  refreshingBackend: false,
  running: false,
  runningSessionId: "",
  runActivityText: "",
  canceling: false,
  screenCapturing: false,
  screenCaptureHideWindow: localStorage.getItem("nrIntern.screenCapture.hideWindow") !== "0",
  currentRunId: "",
  currentRunStatus: "",
  retryableRunId: "",
  retryableSessionId: "",
  pendingApproval: null,
  pendingApprovalSessionId: "",
  liveMessage: null,
  runDraft: null,
  entriesAfter: 0,
  entriesHasMore: false,
  newProjectSandboxRoots: [],
  editingProject: null,
  editProjectSandboxRoots: [],
  contextSession: null,
  contextProject: null,
  contextResource: null,
  renamingSession: null,
  inspectingSession: null,
  deletingSession: null,
  pendingAttachments: [],
  attachmentSessionId: "",
  promptQueue: [],
  outboxLoaded: false,
  outboxAvailable: true,
  queueDraining: false,
  activeRuns: new Map(),
  retryableRuns: new Map(),
  restoreSkippedRunIDs: new Set(),
  restoreSkippedSessionIDs: new Set(),
  trackedRunId: "",
  runStartedAt: new Map(),
  // lastRunDuration 記住每個 Session 最後一次 Run 花了多久，Run 結束後留在狀態列。
  lastRunDuration: new Map(),
  sessionSelectionVersion: 0,
  sessionRuntimeSaving: false,
  messageAutoScroll: true,
  providerUsageRequest: 0,
  contextUsage: null,
  contextCapabilities: null,
  contextCapabilitiesRequest: 0,
  contextCapabilityCache: {},
  contextCompactionSessionId: "",
  contextCompactionSessions: new Set(),
  collapsedProjects: new Set(JSON.parse(localStorage.getItem("collapsedProjects") || "[]")),
  projectSectionCollapsed: localStorage.getItem("projectSectionCollapsed") === "1",
  schedules: [],
  notifications: [],
  updateStatus: null,
  globalSearchResults: [],
  scheduleSectionCollapsed: localStorage.getItem("scheduleSectionCollapsed") === "1",
  scheduleSandboxRoots: [],
  editingSchedule: null,
  scheduleSaving: false,
  restoreCandidates: [],
};

let nativeConversationActivity = null;

const $ = (id) => document.getElementById(id);
const defaultServiceNames = Object.freeze({
  "zh-TW": "永不休息的實習生",
  en: "Never Rest Intern",
  ja: "休まないインターン",
  ko: "쉬지 않는 인턴",
});
const defaultServiceName = defaultServiceNames["zh-TW"];
const knownDefaultServiceNames = new Set([
  ...Object.values(defaultServiceNames),
  "聰明的實習生",
  "Smart Intern",
  "賢いインターン",
  "똑똑한 인턴",
]);
const transientStatuses = new Set([502, 503, 504]);
const supportedBackendAPIMajor = 1;
const requiredBackendCapabilities = Object.freeze([
  "durable-outbox.v1",
  "run-cancel-immediate.v1",
  "run-events.v1",
  "run-recovery.v1",
  "run-retry.v1",
]);
// 側邊欄的排程列是動態產生的，i18n 的 MutationObserver 只認得靜態文字，
// 因此這裡自行查表；語言切換時會重新繪製整份清單。
const translate = (source) => window.NRInternI18n?.t(source) ?? source;
const scheduleWeekdayNames = ["週日", "週一", "週二", "週三", "週四", "週五", "週六"];
const scheduleRefreshIntervalMilliseconds = 30_000;
const activeRunStatuses = new Set(["queued", "running", "paused", "waiting_approval"]);
const waitingForModelPrefix = "等待模型回應";
// 前幾秒不顯示等待訊息：正常速度的回答不需要被提醒「還在等」。
const waitingForModelDelayMilliseconds = 5000;
const mcpAuthModeLabels = Object.freeze({
  none: "不使用驗證",
  bearer: "Bearer Token（金鑰）",
  basic: "Basic Auth（帳號密碼）",
  headers: "自訂 HTTP Headers",
});
const displayModes = new Set(["auto", "light", "dark"]);
const uiLanguagePreferences = new Set(["auto", "zh-TW", "en", "ja", "ko"]);
const systemColorScheme = window.matchMedia("(prefers-color-scheme: dark)");
const applePlatform = /Mac|iPhone|iPad|iPod/i.test(navigator.userAgentData?.platform || navigator.platform || navigator.userAgent);
const renderRichContent = window.RichTextRenderer.render;
const scheduleRichContent = window.RichTextRenderer.schedule;
const richContentSource = window.RichTextRenderer.source;
const maxPendingAttachments = 16;
const maxAttachmentBytes = 8 * 1024 * 1024;
const messageAutoScrollThresholdRatio = 0.03;
const fallbackContextWindowTokens = 256 * 1024;
const backendRefreshIntervalMilliseconds = 4000;
const backendStartupRefreshIntervalMilliseconds = 200;
const providerUsageUIRefreshIntervalMilliseconds = 30_000;
const reverseProxyRefreshIntervalMilliseconds = 3_000;
const memoStorageKey = "nrIntern.memo.v1";
const memoSaveDelayMilliseconds = 250;
let memoSaveTimer = 0;
const imageEditorState = {
  image: null,
  operations: [],
  draft: null,
  tool: "rectangle",
  pointerId: null,
  start: null,
  copying: false,
};
const promptComposition = {
  active: false,
  enterObserved: false,
  suppressNextEnter: false,
  resetTimer: null,
};
let chatDragDepth = 0;
let messageScrollFrame = 0;
let backendRefreshTimer = 0;
let providerOAuthPollTimer = 0;
let backendFastPollUntil = Date.now() + 5000;
let outboxDatabasePromise = null;
const dialogPointerGesture = {
  dialog: null,
  inputKind: "",
  pointerId: null,
  startX: 0,
  startY: 0,
  dragged: false,
  suppressClickFor: null,
};

function applyServiceName(value) {
  const serviceName = String(value || "").trim() || defaultServiceName;
  $("brandName").textContent = serviceName;
  document.title = serviceName;
  if ($("settingServiceName")) $("settingServiceName").value = serviceName;
  if (state.agent) state.agent.name = serviceName;
}

function normalizedUILanguage(value) {
  return uiLanguagePreferences.has(value) ? value : "auto";
}

function localizedDefaultServiceName() {
  const language = window.NRInternI18n?.language || "zh-TW";
  return defaultServiceNames[language] || defaultServiceName;
}

function syncLocalizedDefaultServiceName() {
  const displayedName = String($("settingServiceName")?.value || $("brandName")?.textContent || "").trim();
  const usesDefault = state.serviceSettings
    ? state.serviceSettings.service_name_is_default
    : knownDefaultServiceNames.has(displayedName);
  if (!usesDefault) return;
  const serviceName = localizedDefaultServiceName();
  if (state.serviceSettings) state.serviceSettings.service_name = serviceName;
  applyServiceName(serviceName);
}

function applyUILanguage(value, persist = true) {
  const uiLanguage = normalizedUILanguage(value);
  if (persist) localStorage.setItem("uiLanguage", uiLanguage);
  window.NRInternI18n?.setLanguage(uiLanguage);
  if ($("settingUILanguage")) $("settingUILanguage").value = uiLanguage;
  syncLocalizedDefaultServiceName();
  return uiLanguage;
}

function notifyNativeStartupReady() {
  if (typeof window.nrInternStartupReady !== "function") return;
  window.requestAnimationFrame(() => {
    window.requestAnimationFrame(() => {
      Promise.resolve(window.nrInternStartupReady()).catch(() => {});
    });
  });
}

function normalizedServiceSettings(value = {}) {
  const current = state.serviceSettings || {};
  const wallClockSeconds = Number(value.max_wall_clock_seconds ?? current.max_wall_clock_seconds ?? 7200);
  const maxTokens = Number(value.max_tokens ?? current.max_tokens ?? 0);
  const maxToolCalls = Number(value.max_tool_calls ?? current.max_tool_calls ?? 0);
  const serviceName = String(value.service_name || current.service_name || state.agent?.name || defaultServiceName).trim() || defaultServiceName;
  const serviceNameIsDefault = value.service_name_is_default
    ?? current.service_name_is_default
    ?? knownDefaultServiceNames.has(serviceName);
  const currentHTTPFetch = current.http_fetch ?? {};
  const httpFetch = value.http_fetch ?? {};
  return {
    service_name: serviceName,
    service_name_is_default: Boolean(serviceNameIsDefault),
    ui_language: normalizedUILanguage(value.ui_language ?? current.ui_language ?? "auto"),
    notifications_enabled: Boolean(value.notifications_enabled ?? current.notifications_enabled ?? false),
    max_wall_clock_seconds: Number.isInteger(wallClockSeconds) && wallClockSeconds > 0 ? wallClockSeconds : 7200,
    max_tokens: Number.isInteger(maxTokens) && maxTokens >= 0 ? maxTokens : 0,
    max_tool_calls: Number.isInteger(maxToolCalls) && maxToolCalls >= 0 ? maxToolCalls : 0,
    http_fetch: {
      enabled: Boolean(httpFetch.enabled ?? currentHTTPFetch.enabled),
      allow_private_networks: Boolean(httpFetch.allow_private_networks ?? currentHTTPFetch.allow_private_networks ?? true),
    },
    extended_tools: Boolean(value.extended_tools ?? current.extended_tools ?? false),
    tool_call_mode: (value.tool_call_mode ?? current.tool_call_mode) === "instruction" ? "instruction" : "native",
    // 預設 true：舊的後端沒有這個欄位時，畫面要顯示實際生效的行為。
    tool_retrieval: Boolean(value.tool_retrieval ?? current.tool_retrieval ?? true),
  };
}

function applyServiceSettings(value) {
  const settings = normalizedServiceSettings(value);
  state.serviceSettings = settings;
  applyUILanguage(settings.ui_language);
  applyServiceName(settings.service_name_is_default ? localizedDefaultServiceName() : settings.service_name);
  if ($("settingNotificationsEnabled")) $("settingNotificationsEnabled").checked = settings.notifications_enabled;
  syncNotificationUIAvailability();
  if ($("settingMaxWallClockMinutes")) $("settingMaxWallClockMinutes").value = String(Math.ceil(settings.max_wall_clock_seconds / 60));
  if ($("settingMaxTokens")) $("settingMaxTokens").value = String(settings.max_tokens);
  if ($("settingMaxToolCalls")) $("settingMaxToolCalls").value = String(settings.max_tool_calls);
  if ($("settingExtendedTools")) {
    $("settingExtendedTools").checked = settings.extended_tools;
    $("settingInstructionToolCalls").checked = settings.tool_call_mode === "instruction";
    $("settingToolRetrieval").checked = settings.tool_retrieval;
  }
  if ($("settingHTTPFetchEnabled")) {
    $("settingHTTPFetchEnabled").checked = settings.http_fetch.enabled;
    $("settingHTTPFetchPrivateNetworks").checked = settings.http_fetch.allow_private_networks;
    syncHTTPFetchSettingFields();
  }
}

function notificationCenterEnabled() {
  return Boolean(state.serviceSettings?.notifications_enabled);
}

function syncNotificationUIAvailability() {
  const enabled = notificationCenterEnabled();
  const button = $("notificationButton");
  if (!button) return;
  button.classList.toggle("hidden", !enabled);
  button.disabled = !enabled || !state.backendHealthy;
  button.closest(".sidebar-action-row")?.classList.toggle("notifications-disabled", !enabled);
  renderAboutVersionInfo();
  if (enabled) return;
  state.notifications = [];
  closeNotificationPopover();
  renderNotifications();
}

// 關閉對外網路時，私有網段的選項沒有意義，直接停用避免誤會成兩個獨立開關。
function syncHTTPFetchSettingFields() {
  const enabled = $("settingHTTPFetchEnabled").checked;
  $("settingHTTPFetchPrivateNetworks").disabled = !enabled;
}

function installDialogDragGuards() {
  const beginGesture = (event, inputKind) => {
    if (event.button !== 0 || dialogPointerGesture.dialog) return;
    const dialog = event.target.closest?.("dialog[open]");
    if (!dialog || event.target === dialog) return;
    dialogPointerGesture.dialog = dialog;
    dialogPointerGesture.inputKind = inputKind;
    dialogPointerGesture.pointerId = inputKind === "pointer" ? event.pointerId : null;
    dialogPointerGesture.startX = event.clientX;
    dialogPointerGesture.startY = event.clientY;
    dialogPointerGesture.dragged = false;
  };
  const trackGesture = (event, inputKind) => {
    if (!dialogPointerGesture.dialog || dialogPointerGesture.inputKind !== inputKind) return;
    if (inputKind === "pointer" && dialogPointerGesture.pointerId !== event.pointerId) return;
    const distance = Math.hypot(
      event.clientX - dialogPointerGesture.startX,
      event.clientY - dialogPointerGesture.startY,
    );
    if (distance >= 4) dialogPointerGesture.dragged = true;
  };
  const clearGesture = () => {
    dialogPointerGesture.dialog = null;
    dialogPointerGesture.inputKind = "";
    dialogPointerGesture.pointerId = null;
    dialogPointerGesture.dragged = false;
  };
  const finishGesture = (event, inputKind) => {
    if (!dialogPointerGesture.dialog || dialogPointerGesture.inputKind !== inputKind) return;
    if (inputKind === "pointer" && dialogPointerGesture.pointerId !== event.pointerId) return;
    if (dialogPointerGesture.dragged) {
      dialogPointerGesture.suppressClickFor = dialogPointerGesture.dialog;
      window.setTimeout(() => {
        dialogPointerGesture.suppressClickFor = null;
      }, 0);
    }
    clearGesture();
  };
  document.addEventListener("pointerdown", (event) => {
    beginGesture(event, "pointer");
  }, true);
  document.addEventListener("pointermove", (event) => {
    trackGesture(event, "pointer");
  }, true);
  document.addEventListener("pointerup", (event) => finishGesture(event, "pointer"), true);
  document.addEventListener("pointercancel", (event) => finishGesture(event, "pointer"), true);
  document.addEventListener("mousedown", (event) => beginGesture(event, "mouse"), true);
  document.addEventListener("mousemove", (event) => trackGesture(event, "mouse"), true);
  document.addEventListener("mouseup", (event) => finishGesture(event, "mouse"), true);
  document.addEventListener("click", (event) => {
    const dialog = event.target.closest?.("dialog[open]") || (event.target.matches?.("dialog[open]") ? event.target : null);
    if (!dialog || dialogPointerGesture.suppressClickFor !== dialog) return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);
  for (const dialog of document.querySelectorAll("dialog")) {
    dialog.addEventListener("cancel", (event) => {
      if (dialogPointerGesture.dialog === dialog || state.planDragID) event.preventDefault();
    });
  }
  window.addEventListener("blur", clearGesture);
}

function hasSystemShortcutModifier(event) {
  const primaryModifier = applePlatform ? event.metaKey : event.ctrlKey;
  return primaryModifier || event.altKey || event.metaKey;
}

function normalizeFullwidthASCII(value) {
  let result = "";
  for (const character of value || "") {
    const codePoint = character.codePointAt(0);
    if (codePoint === 0x3000) result += " ";
    else if (codePoint >= 0xFF01 && codePoint <= 0xFF5E) result += String.fromCodePoint(codePoint - 0xFEE0);
    else result += character;
  }
  return result;
}

function applyDisplayMode(value, persist = true) {
  const mode = displayModes.has(value) ? value : "auto";
  const resolved = mode === "auto" ? (systemColorScheme.matches ? "dark" : "light") : mode;
  document.documentElement.dataset.displayMode = mode;
  document.documentElement.dataset.theme = resolved;
  $("displayMode").value = mode;
  if (persist) localStorage.setItem("displayMode", mode);
}

function refreshAutomaticDisplayMode() {
  if (document.documentElement.dataset.displayMode === "auto") applyDisplayMode("auto", false);
}

applyDisplayMode(document.documentElement.dataset.displayMode || "auto", false);
applyUILanguage(document.documentElement.dataset.uiLanguage || "auto", false);
if (typeof systemColorScheme.addEventListener === "function") {
  systemColorScheme.addEventListener("change", refreshAutomaticDisplayMode);
} else {
  systemColorScheme.addListener(refreshAutomaticDisplayMode);
}

async function request(path, options = {}) {
  const { reconnects, ...fetchOptions } = options;
  const method = (fetchOptions.method || "GET").toUpperCase();
  const maxReconnects = reconnects ?? (method === "GET" ? 3 : 0);
  let attempt = 0;
  while (true) {
    try {
      const response = await fetch(`/backend${path}`, {
        ...fetchOptions,
        headers: { "Content-Type": "application/json", ...(fetchOptions.headers || {}) },
      });
      if (transientStatuses.has(response.status) && attempt < maxReconnects) {
        attempt += 1;
        await delay(300 * (2 ** (attempt - 1)));
        continue;
      }
      if (!response.ok) {
        let detail = `${response.status} ${response.statusText}`;
        try { detail = (await response.json()).detail || detail; } catch (_) {}
        const error = new Error(detail);
        error.noReconnect = true;
        throw error;
      }
      if (response.status === 204) return null;
      return (await response.json()).data;
    } catch (error) {
      if (attempt >= maxReconnects || error.name === "AbortError" || error.noReconnect) throw error;
      attempt += 1;
      await delay(300 * (2 ** (attempt - 1)));
    }
  }
}

async function desktop(path, options = {}) {
  const response = await fetch(`/desktop/api/${path}`, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const body = response.status === 204 ? null : await response.json();
  if (!response.ok) throw new Error(body?.error || response.statusText);
  return body?.data;
}

async function captureScreenToClipboard() {
  if (state.screenCapturing) return;
  state.screenCapturing = true;
  const button = $("captureScreen");
  const optionsButton = $("screenCaptureOptions");
  let nativeWindowHidden = false;
  button.disabled = true;
  optionsButton.disabled = true;
  button.setAttribute("aria-busy", "true");
  try {
    if (state.screenCaptureHideWindow && typeof window.nrInternSetWindowHidden === "function") {
      await Promise.resolve(window.nrInternSetWindowHidden(true));
      nativeWindowHidden = true;
      await delay(160);
    }
    const result = await desktop("screen-capture", { method: "POST", body: "{}" });
    if (!result) return;
    if (nativeWindowHidden) {
      await Promise.resolve(window.nrInternSetWindowHidden(false));
      nativeWindowHidden = false;
      await delay(80);
    }
    toast(translate(result.status === "launched"
      ? "已開啟畫面擷取；完成後圖片會複製到剪貼簿"
      : "畫面已擷取並複製到剪貼簿"));
    if (result.image_base64) await openImageEditor(result.image_base64);
  } catch (error) {
    toast(error.message);
  } finally {
    if (nativeWindowHidden && typeof window.nrInternSetWindowHidden === "function") {
      await Promise.resolve(window.nrInternSetWindowHidden(false)).catch(() => {});
    }
    state.screenCapturing = false;
    button.disabled = false;
    optionsButton.disabled = false;
    button.removeAttribute("aria-busy");
  }
}

function closeScreenCaptureMenu() {
  $("screenCaptureMenu").classList.add("hidden");
  $("screenCaptureOptions").setAttribute("aria-expanded", "false");
}

function toggleScreenCaptureMenu() {
  const menu = $("screenCaptureMenu");
  const willOpen = menu.classList.contains("hidden");
  menu.classList.toggle("hidden", !willOpen);
  $("screenCaptureOptions").setAttribute("aria-expanded", String(willOpen));
}

function imageEditorCoordinates(event) {
  const canvas = $("imageEditorCanvas");
  const rect = canvas.getBoundingClientRect();
  if (!rect.width || !rect.height) return { x: 0, y: 0 };
  return {
    x: Math.max(0, Math.min(canvas.width, (event.clientX - rect.left) * canvas.width / rect.width)),
    y: Math.max(0, Math.min(canvas.height, (event.clientY - rect.top) * canvas.height / rect.height)),
  };
}

function imageEditorScale() {
  const canvas = $("imageEditorCanvas");
  const rect = canvas.getBoundingClientRect();
  return rect.width ? canvas.width / rect.width : 1;
}

function fitImageEditorCanvas() {
  const dialog = $("imageEditorDialog");
  const canvas = $("imageEditorCanvas");
  if (!dialog.open || !imageEditorState.image || !canvas.width || !canvas.height) return;
  const stage = $("imageEditorStage").getBoundingClientRect();
  const scale = Math.min(1, Math.max(0.05, (stage.width - 4) / canvas.width), Math.max(0.05, (stage.height - 4) / canvas.height));
  canvas.style.width = `${Math.max(1, Math.floor(canvas.width * scale))}px`;
  canvas.style.height = `${Math.max(1, Math.floor(canvas.height * scale))}px`;
}

function drawImageEditorOperation(context, operation, draft = false) {
  context.save();
  context.strokeStyle = operation.color;
  context.fillStyle = operation.color;
  context.lineWidth = operation.lineWidth;
  context.lineCap = "round";
  context.lineJoin = "round";
  if (draft) context.setLineDash([operation.lineWidth * 2.5, operation.lineWidth * 1.5]);
  if (operation.type === "rectangle") {
    const x = Math.min(operation.x1, operation.x2);
    const y = Math.min(operation.y1, operation.y2);
    context.strokeRect(x, y, Math.abs(operation.x2 - operation.x1), Math.abs(operation.y2 - operation.y1));
  } else if (operation.type === "line") {
    context.beginPath();
    context.moveTo(operation.x1, operation.y1);
    context.lineTo(operation.x2, operation.y2);
    context.stroke();
  } else if (operation.type === "text") {
    context.font = `700 ${operation.fontSize}px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`;
    context.textBaseline = "top";
    context.setLineDash([]);
    context.strokeStyle = "rgba(255, 255, 255, .92)";
    context.lineWidth = Math.max(2, operation.fontSize * .12);
    context.strokeText(operation.text, operation.x, operation.y);
    context.fillStyle = operation.color;
    context.fillText(operation.text, operation.x, operation.y);
  }
  context.restore();
}

function renderImageEditor() {
  const canvas = $("imageEditorCanvas");
  const context = canvas.getContext("2d");
  if (!context || !imageEditorState.image) return;
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.drawImage(imageEditorState.image, 0, 0, canvas.width, canvas.height);
  for (const operation of imageEditorState.operations) drawImageEditorOperation(context, operation);
  if (imageEditorState.draft) drawImageEditorOperation(context, imageEditorState.draft, true);
  const hasOperations = imageEditorState.operations.length > 0;
  $("undoImageEdit").disabled = !hasOperations;
  $("resetImageEdit").disabled = !hasOperations;
}

function setImageEditorTool(tool) {
  imageEditorState.tool = tool;
  for (const button of document.querySelectorAll("[data-image-tool]")) {
    const selected = button.dataset.imageTool === tool;
    button.classList.toggle("is-selected", selected);
    button.setAttribute("aria-pressed", String(selected));
  }
  const textMode = tool === "text";
  $("imageEditorTextControl").classList.toggle("hidden", !textMode);
  $("imageEditorFontControl").classList.toggle("hidden", !textMode);
  if (textMode) $("imageEditorText").focus();
}

function normalizeImageEditorColor(value) {
  const match = String(value || "").trim().match(/^#?([0-9a-f]{6})$/i);
  return match ? `#${match[1].toLowerCase()}` : "";
}

function closeImageEditorColorPalette() {
  $("imageEditorColorPalette").classList.add("hidden");
  $("imageEditorColorButton").setAttribute("aria-expanded", "false");
}

function setImageEditorColor(value, { close = false, syncHex = true } = {}) {
  const color = normalizeImageEditorColor(value);
  if (!color) return false;
  $("imageEditorColor").value = color;
  $("imageEditorColorSwatch").style.backgroundColor = color;
  if (syncHex) $("imageEditorColorHex").value = color.toUpperCase();
  for (const button of document.querySelectorAll("[data-image-color]")) {
    const selected = button.dataset.imageColor.toLowerCase() === color;
    button.classList.toggle("is-selected", selected);
    button.setAttribute("aria-pressed", String(selected));
  }
  if (close) closeImageEditorColorPalette();
  return true;
}

function toggleImageEditorColorPalette() {
  const palette = $("imageEditorColorPalette");
  const willOpen = palette.classList.contains("hidden");
  palette.classList.toggle("hidden", !willOpen);
  $("imageEditorColorButton").setAttribute("aria-expanded", String(willOpen));
  if (willOpen) $("imageEditorColorHex").focus();
}

function loadImageEditorSource(imageBase64) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error(translate("無法建立編輯圖像")));
    image.src = `data:image/png;base64,${imageBase64}`;
  });
}

async function openImageEditor(imageBase64) {
  const image = await loadImageEditorSource(imageBase64);
  imageEditorState.image = image;
  imageEditorState.operations = [];
  imageEditorState.draft = null;
  imageEditorState.pointerId = null;
  imageEditorState.start = null;
  const canvas = $("imageEditorCanvas");
  canvas.width = image.naturalWidth;
  canvas.height = image.naturalHeight;
  $("imageEditorStatus").textContent = `${image.naturalWidth} × ${image.naturalHeight}`;
  $("imageEditorText").value = "";
  setImageEditorColor($("imageEditorColor").value);
  closeImageEditorColorPalette();
  setImageEditorTool("rectangle");
  const dialog = $("imageEditorDialog");
  if (!dialog.open) dialog.showModal();
  requestAnimationFrame(() => {
    fitImageEditorCanvas();
    renderImageEditor();
    canvas.focus();
  });
}

function startImageEditorDrawing(event) {
  if (event.button !== 0 || !imageEditorState.image) return;
  const point = imageEditorCoordinates(event);
  const scale = imageEditorScale();
  if (imageEditorState.tool === "text") {
    const text = $("imageEditorText").value.trim();
    if (!text) {
      toast(translate("請先輸入要放到圖上的文字"));
      $("imageEditorText").focus();
      return;
    }
    imageEditorState.operations.push({
      type: "text",
      text,
      x: point.x,
      y: point.y,
      color: $("imageEditorColor").value,
      fontSize: Number($("imageEditorFontSize").value) * scale,
    });
    renderImageEditor();
    return;
  }
  imageEditorState.pointerId = event.pointerId;
  imageEditorState.start = point;
  imageEditorState.draft = {
    type: imageEditorState.tool,
    x1: point.x,
    y1: point.y,
    x2: point.x,
    y2: point.y,
    color: $("imageEditorColor").value,
    lineWidth: Number($("imageEditorLineWidth").value) * scale,
  };
  $("imageEditorCanvas").setPointerCapture(event.pointerId);
  event.preventDefault();
}

function moveImageEditorDrawing(event) {
  if (imageEditorState.pointerId !== event.pointerId || !imageEditorState.draft) return;
  const point = imageEditorCoordinates(event);
  imageEditorState.draft.x2 = point.x;
  imageEditorState.draft.y2 = point.y;
  renderImageEditor();
}

function finishImageEditorDrawing(event, commit = true) {
  if (imageEditorState.pointerId !== event.pointerId || !imageEditorState.draft) return;
  const canvas = $("imageEditorCanvas");
  if (canvas.hasPointerCapture(event.pointerId)) canvas.releasePointerCapture(event.pointerId);
  const operation = imageEditorState.draft;
  const distance = Math.hypot(operation.x2 - operation.x1, operation.y2 - operation.y1);
  if (commit && distance >= Math.max(2, operation.lineWidth)) imageEditorState.operations.push(operation);
  imageEditorState.pointerId = null;
  imageEditorState.start = null;
  imageEditorState.draft = null;
  renderImageEditor();
}

function undoImageEdit() {
  imageEditorState.operations.pop();
  renderImageEditor();
}

function resetImageEdit() {
  imageEditorState.operations = [];
  imageEditorState.draft = null;
  renderImageEditor();
}

function imageEditorPNGBlob() {
  renderImageEditor();
  return new Promise((resolve, reject) => {
    $("imageEditorCanvas").toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error(translate("無法建立編輯圖像")));
    }, "image/png");
  });
}

async function copyEditedImage() {
  if (imageEditorState.copying || !imageEditorState.image) return false;
  imageEditorState.copying = true;
  const button = $("copyEditedImage");
  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  $("imageEditorStatus").textContent = translate("正在複製圖像…");
  try {
    const blob = await imageEditorPNGBlob();
    await desktop("clipboard/image", { method: "POST", headers: { "Content-Type": "image/png" }, body: blob });
    $("imageEditorStatus").textContent = translate("已更新系統剪貼簿");
    toast(translate("已更新系統剪貼簿"));
    return true;
  } catch (error) {
    $("imageEditorStatus").textContent = error.message;
    toast(error.message);
    return false;
  } finally {
    imageEditorState.copying = false;
    button.disabled = false;
    button.removeAttribute("aria-busy");
  }
}

async function closeImageEditor() {
  closeImageEditorColorPalette();
  if (await copyEditedImage()) $("imageEditorDialog").close();
}

function backendCompatibility(status) {
  const apiVersion = String(status?.api_version || "").trim();
  const apiMajor = Number.parseInt(apiVersion.split(".")[0], 10);
  if (!apiVersion || !Number.isInteger(apiMajor)) {
    return { compatible: false, message: "後端版本過舊，缺少 API 相容資訊；請重新啟動 NR-Intern。" };
  }
  if (apiMajor !== supportedBackendAPIMajor) {
    return { compatible: false, message: `前端需要 API ${supportedBackendAPIMajor}.x，但後端為 ${apiVersion}；請更新或重新啟動整套程式。` };
  }
  const capabilities = new Set(Array.isArray(status.capabilities) ? status.capabilities : []);
  const missing = requiredBackendCapabilities.filter((capability) => !capabilities.has(capability));
  if (missing.length > 0) {
    return { compatible: false, message: `後端缺少必要功能（${missing.join("、")}）；請重新啟動或更新 NR-Intern。` };
  }
  return { compatible: true, message: "" };
}

async function readBackendCompatibility() {
  try {
    const status = await request("/api/v1/admin/status", { reconnects: 0 });
    const result = backendCompatibility(status);
    return { ...result, status };
  } catch (_) {
    return {
      compatible: false,
      status: null,
      message: "目前後端不支援版本／功能相容檢查；請完整關閉後重新啟動 NR-Intern。",
    };
  }
}

function toast(message) {
  $("toast").textContent = message;
  $("toast").classList.remove("hidden");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => $("toast").classList.add("hidden"), 4200);
}

function confirmAction(message) {
  const dialog = $("confirmationDialog");
  if (dialog.open) return Promise.resolve(false);
  $("confirmationMessage").textContent = message;
  dialog.returnValue = "cancel";
  return new Promise((resolve) => {
    dialog.addEventListener("close", () => resolve(dialog.returnValue === "confirm"), { once: true });
    dialog.showModal();
  });
}

async function refreshBackend() {
  if (state.refreshingBackend) return;
  state.refreshingBackend = true;
  try {
    const status = await desktop("status");
    const wasHealthy = state.backendHealthy;
    let compatibility = { compatible: false, status: null, message: "" };
    if (status.healthy) compatibility = await readBackendCompatibility();
    state.backendCompatible = Boolean(status.healthy && compatibility.compatible);
    state.backendStatus = compatibility.status;
    renderAboutVersionInfo();
    state.backendCompatibilityError = compatibility.message || "";
    state.backendHealthy = state.backendCompatible;
    state.backendLifecycleState = status.state || "stopped";
    const label = status.healthy && !state.backendCompatible
      ? "後端版本不相容"
      : status.healthy
      ? (status.owned ? "後端執行中" : "已連接外部後端")
      : ["starting", "running"].includes(status.state) ? "啟動中" : "後端未啟動";
    $("backendState").textContent = label;
    $("managementBackendState").textContent = label;
    $("backendURL").textContent = status.backend_url;
    $("backendDetail").textContent = state.backendCompatibilityError
      || (status.last_error
      ? `錯誤：${status.last_error}`
      : status.pid ? `PID ${status.pid}` : status.owned ? "由桌面程式管理" : "");
    for (const id of ["statusDot", "managementStatusDot"]) {
      $(id).className = `dot ${state.backendHealthy ? "ok" : "bad"}`;
    }
    $("stopBackend").disabled = !status.owned;
    $("restartBackend").disabled = !status.owned;
    $("startBackend").disabled = status.healthy || ["starting", "running"].includes(status.state);
    $("newWorkspace").disabled = !state.backendHealthy || state.running;
    $("newManagementWorkspace").disabled = !state.backendHealthy || state.running;
    $("workspaceSelect").disabled = !state.backendHealthy || state.running;
    $("workspaceManagementSelect").disabled = !state.backendHealthy || state.running;
    $("newProject").disabled = !state.backendHealthy || !state.workspace;
    $("newSchedule").disabled = !state.backendHealthy || !state.workspace;
    $("openManagement").disabled = !state.backendHealthy;
    $("providerUsageButton").disabled = !state.backendHealthy;
    syncNotificationUIAvailability();
    if (!state.backendHealthy) closeProviderUsagePopover();
    if (!state.backendHealthy) closeNotificationPopover();
    if (state.backendHealthy && (!wasHealthy || !state.agent)) await loadApplicationState();
    if (!state.backendHealthy && wasHealthy) resetWorkspace();
  } catch (error) {
    toast(error.message);
  } finally {
    state.refreshingBackend = false;
    scheduleBackendRefresh();
  }
}

function scheduleBackendRefresh() {
  clearTimeout(backendRefreshTimer);
  const starting = !state.backendHealthy
    && (["starting", "running"].includes(state.backendLifecycleState) || Date.now() < backendFastPollUntil);
  backendRefreshTimer = window.setTimeout(refreshBackend,
    starting ? backendStartupRefreshIntervalMilliseconds : backendRefreshIntervalMilliseconds);
}

function resetWorkspace() {
  clearPendingAttachments();
  if ($("restoreRunsDialog")?.open) $("restoreRunsDialog").close("cancel");
  state.restoreCandidates = [];
  state.restoreSkippedRunIDs.clear();
  state.restoreSkippedSessionIDs.clear();
  // 後端離線時仍保留 durable outbox；恢復連線後再繼續送出。
  if ($("promptQueue")) renderPromptQueue();
  state.agent = null;
  state.providers = [];
  state.providerSettings = null;
  state.selectedProviderSettingsID = "";
  state.providerSettingsDraft = null;
  state.providerModelLoads = {};
  state.providerModelErrors = {};
  state.providerTestedModels = {};
  state.workspaces = [];
  state.workspace = null;
  state.projects = [];
  state.sessions = [];
  state.schedules = [];
  state.session = null;
  state.plans = [];
  state.planTab = "active";
  state.planEditingID = "";
  state.expandedPlanIDs.clear();
  state.planExpansionInitialized = false;
  state.activePlanID = "";
  state.planDragID = "";
  state.sessionDragID = "";
  state.sessionDragProjectID = "";
  state.sessionOrderSaving = false;
  state.sessionRuntimeSaving = false;
  state.activeRuns.clear();
  state.retryableRuns.clear();
  state.runningSessionId = "";
  state.currentRunStatus = "";
  state.runActivityText = "";
  state.retryableRunId = "";
  state.retryableSessionId = "";
  state.pendingApproval = null;
  state.pendingApprovalSessionId = "";
  state.liveMessage = null;
  state.runStartedAt.clear();
  state.contextCompactionSessions.clear();
  state.contextCompactionSessionId = "";
  syncRunStateAliases();
  state.contextUsage = null;
  state.contextCapabilities = null;
  state.contextCapabilitiesRequest += 1;
  state.contextCapabilityCache = {};
  $("sessionTitle").textContent = "選擇或建立對話";
  $("agentLabel").textContent = "後端目前離線";
  $("prompt").disabled = true;
  syncRunActionButton();
  $("workspaceSelect").replaceChildren();
  $("workspaceManagementSelect").replaceChildren();
  syncSessionRuntimeControls({ loadModels: false });
  syncPlanButton();
  renderContextUsage();
  syncWorkspaceSettings();
  $("messages").replaceChildren();
  $("messages").classList.add("hidden");
  $("emptyState").classList.remove("hidden");
  renderNavigation();
}

async function controlBackend(action) {
  try {
    if (["start", "restart"].includes(action)) backendFastPollUntil = Date.now() + 30000;
    await desktop(action, { method: "POST", body: "{}" });
    await refreshBackend();
  } catch (error) {
    toast(error.message);
  }
}

async function loadApplicationState() {
  const [agents, providers, workspaces, diagnostics] = await Promise.all([
    request("/api/v1/agents"),
    request("/api/v1/providers"),
    request("/api/v1/workspaces"),
    request("/api/v1/admin/diagnostics"),
  ]);
  state.agent = agents[0] || null;
  applyServiceSettings(diagnostics.config || {});
  state.providers = providers;
  state.workspaces = workspaces;
  state.permissions = diagnostics.config?.permissions || null;
  void loadUpdateStatus();
  $("agentLabel").textContent = state.agent?.name || "尚無 Agent";
  renderProviderOptions();
  renderPermissionOptions();
  renderWorkspaceOptions();
  const storedID = sessionStorage.getItem("activeWorkspaceID");
  state.workspace = state.workspaces.find((item) => item.id === storedID) || state.workspaces[0] || null;
  if (state.workspace) {
    syncWorkspaceSelectorValues(state.workspace.id);
    sessionStorage.setItem("activeWorkspaceID", state.workspace.id);
    syncWorkspaceSettings();
    void initializeWorkspaceModels(state.workspace.id);
  }
  if (!state.agent || !state.workspace) return;
  await Promise.all([loadProjects(), loadSessions(), loadSchedules()]);
  await restoreWorkspaceRun();
  if (notificationCenterEnabled()) void loadNotifications();
  void drainPromptQueue();
  $("newProject").disabled = !state.backendHealthy || !state.workspace;
  $("newManagementWorkspace").disabled = state.running;
  $("workspaceSelect").disabled = state.running;
  $("workspaceManagementSelect").disabled = state.running;
}

function renderPermissionOptions() {
  const policy = state.permissions || {};
  const defaultProfile = policy.default_profile || "default";
  const profiles = policy.allow_client_choice
    ? [...new Set([defaultProfile, ...(policy.elevated_profiles || [])])]
    : [defaultProfile];
  for (const id of ["newPermission", "settingPermission"]) {
    const select = $(id);
    const current = select.value;
    select.replaceChildren(...profiles.map((profile) => {
      const option = document.createElement("option");
      option.value = profile;
      option.textContent = profile === defaultProfile ? `後端預設（${profile}）` : profile;
      return option;
    }));
    select.value = profiles.includes(current) ? current : defaultProfile;
    select.title = policy.allow_client_choice ? "後端允許選擇權限 profile" : "權限 profile 由後端鎖定";
  }
}

async function loadProjects() {
  if (!state.workspace) return;
  state.projects = await request(`/api/v1/projects?workspace_id=${encodeURIComponent(state.workspace.id)}`);
  renderProjectOptions();
  renderNavigation();
}

// 排程是 Workspace 層級的獨立實體，不隨 Project 或 Session 一起載入。
async function loadSchedules() {
  if (!state.workspace) {
    state.schedules = [];
    renderSchedules();
    return;
  }
  const previousRuns = new Map(state.schedules.map((schedule) => [schedule.id, schedule.last_run_id || ""]));
  try {
    state.schedules = await request(`/api/v1/schedules?workspace_id=${encodeURIComponent(state.workspace.id)}`);
  } catch (_) {
    // 後端未提供排程儲存時只顯示空清單，不打斷其他側邊欄內容。
    state.schedules = [];
  }
  renderSchedules();
  // 背景觸發的排程會建立新的對話；偵測到新的 Run 才重新抓 Session，避免定期輪詢整份清單。
  const triggered = state.schedules.some((schedule) => {
    const runID = schedule.last_run_id || "";
    return runID && previousRuns.has(schedule.id) && previousRuns.get(schedule.id) !== runID;
  });
  if (triggered) await loadSessions();
}

async function loadSessions() {
  if (!state.agent || !state.workspace) return;
  state.sessions = await request(`/api/v1/agents/${encodeURIComponent(state.agent.id)}/sessions?workspace_id=${encodeURIComponent(state.workspace.id)}`);
  if (state.session) {
    const updated = state.sessions.find((item) => item.id === state.session.id);
    if (updated) {
      state.session = updated;
      syncSessionUI();
    } else {
      state.session = null;
    }
  }
  renderNavigation();
}

function renderNavigation() {
  const pinned = [...state.sessions]
    .filter((session) => session.pinned)
    .sort((a, b) => new Date(b.pinned_at || b.updated_at) - new Date(a.pinned_at || a.updated_at));
  $("pinnedSection").classList.toggle("hidden", pinned.length === 0);
  $("pinnedList").replaceChildren(...pinned.map((session) => sessionNode(session, true)));

  const projectSectionCollapsed = state.projectSectionCollapsed;
  const projectSectionToggle = $("projectSectionToggle");
  projectSectionToggle.setAttribute("aria-expanded", String(!projectSectionCollapsed));
  projectSectionToggle.querySelector(".caret")?.classList.toggle("collapsed", projectSectionCollapsed);
  const projectList = $("projectList");
  projectList.classList.toggle("hidden", projectSectionCollapsed);
  projectList.replaceChildren();
  if (!projectSectionCollapsed) {
    for (const project of state.projects) {
      const sessions = orderedProjectSessions(state.sessions.filter((session) => session.project_id === project.id && !session.pinned));
      projectList.append(projectNode(project, sessions));
    }
    const ungrouped = orderedProjectSessions(state.sessions.filter((session) => !session.project_id && !session.pinned));
    if (ungrouped.length > 0) projectList.append(projectNode(null, ungrouped));
    if (state.projects.length === 0 && ungrouped.length === 0 && state.backendHealthy) {
      const empty = document.createElement("p");
      empty.className = "navigation-empty";
      empty.textContent = "尚無專案，先建立一個專案。";
      projectList.append(empty);
    }
  }
  renderSchedules();
}

function orderedProjectSessions(sessions) {
  return sessions
    .map((session, index) => ({ session, index }))
    .sort((left, right) => {
      const leftPosition = Number.isFinite(Number(left.session.position)) ? Number(left.session.position) : 0;
      const rightPosition = Number.isFinite(Number(right.session.position)) ? Number(right.session.position) : 0;
      return leftPosition - rightPosition || left.index - right.index;
    })
    .map((value) => value.session);
}

function projectNode(project, sessions) {
  const projectID = project?.id || "uncategorized";
  const group = document.createElement("section");
  group.className = "project-group";
  group.dataset.projectId = project?.id || "";
  const header = document.createElement("div");
  header.className = "project-head";
  const toggle = document.createElement("button");
  toggle.className = "project-toggle";
  toggle.type = "button";
  toggle.setAttribute("aria-expanded", String(!state.collapsedProjects.has(projectID)));
  const caret = projectCaretIcon(state.collapsedProjects.has(projectID));
  const icon = folderIcon();
  const name = document.createElement("span");
  name.className = "project-name";
  name.textContent = project?.name || "未分類";
  toggle.append(caret, icon, name);
  toggle.addEventListener("click", () => toggleProject(projectID));
  if (project) {
    header.addEventListener("contextmenu", (event) => openProjectContextMenu(event, project));
    toggle.addEventListener("keydown", (event) => {
      if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) return;
      event.preventDefault();
      const bounds = header.getBoundingClientRect();
      showProjectContextMenu(project, bounds.left + Math.min(bounds.width, 180), bounds.top + 12, true);
    });
  }
  header.append(toggle);
  if (project) {
    const actions = document.createElement("div");
    actions.className = "project-row-actions";
    const newConversation = iconButton(chatBubbleIcon(), "在此專案建立對話", () => openSessionDialog(project.id));
    newConversation.classList.add("project-chat-button");
    newConversation.disabled = !state.backendHealthy || !state.agent;
    const manage = iconButton("⋯", "管理專案", () => manageProject(project));
    manage.classList.add("project-manage-button");
    manage.disabled = !state.backendHealthy;
    actions.append(manage, newConversation);
    header.append(actions);
  }
  const children = document.createElement("div");
  children.className = "project-sessions";
  children.dataset.projectId = project?.id || "";
  children.classList.toggle("hidden", state.collapsedProjects.has(projectID));
  if (sessions.length === 0) {
    const empty = document.createElement("small");
    empty.className = "project-empty";
    empty.textContent = "沒有對話";
    children.append(empty);
  } else {
    children.append(...sessions.map((session) => sessionNode(session, false)));
  }
  group.append(header, children);
  return group;
}

function sessionNode(session, pinnedContext) {
  const row = document.createElement("div");
  const running = sessionRunIsActive(session.id);
  row.className = `session-row ${state.session?.id === session.id ? "active" : ""} ${running ? "running" : ""}`;
  row.dataset.sessionId = session.id;
  row.dataset.projectId = session.project_id || "";
  row.draggable = !pinnedContext && !sessionRunIsActive(session.id) && !state.sessionOrderSaving;
  if (row.draggable) {
    row.addEventListener("dragstart", (event) => startSessionDrag(event, row, session));
    row.addEventListener("dragend", clearSessionDragState);
    row.addEventListener("dragover", (event) => markSessionDropTarget(event, row, session));
    row.addEventListener("dragleave", (event) => clearSessionDropTarget(event, row));
    row.addEventListener("drop", (event) => dropSession(event, row, session));
  }
  row.addEventListener("contextmenu", (event) => openSessionContextMenu(event, session));
  const button = document.createElement("button");
  button.className = "session";
  const title = document.createElement("span");
  title.textContent = session.title || "未命名";
  const meta = document.createElement("small");
  const providerID = session.provider_id || state.workspace?.default_provider_id || "";
  meta.textContent = displayedModelForProvider(providerID, session.model)
    || providerDisplayName(providerID)
    || "未選擇模型";
  button.append(title, meta);
  button.addEventListener("click", () => selectSession(session));
  button.addEventListener("keydown", (event) => {
    if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) return;
    event.preventDefault();
    const bounds = row.getBoundingClientRect();
    showSessionContextMenu(session, bounds.left + Math.min(bounds.width, 180), bounds.top + 12, true);
  });
  const runIndicator = document.createElement("span");
  runIndicator.className = "session-run-indicator";
  runIndicator.setAttribute("role", "status");
  runIndicator.setAttribute("aria-label", "此對話正在背景執行");
  runIndicator.title = "此對話正在背景執行";
  for (let index = 0; index < 3; index += 1) runIndicator.append(document.createElement("i"));
  const pin = iconButton(session.pinned ? "◆" : "◇", session.pinned ? "取消釘選" : "釘選", () => setPinned(session, !session.pinned));
  pin.classList.add("pin-button");
  pin.disabled = sessionRunIsActive(session.id);
  if (pinnedContext) pin.classList.add("pinned");
  row.append(button, runIndicator, pin);
  return row;
}

function startSessionDrag(event, row, session) {
  if (!row.draggable || state.sessionOrderSaving) {
    event.preventDefault();
    return;
  }
  state.sessionDragID = session.id;
  state.sessionDragProjectID = session.project_id || "";
  row.classList.add("dragging");
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", session.id);
  }
  for (const group of document.querySelectorAll(".project-group")) {
    group.classList.toggle("session-drop-disabled", group.dataset.projectId !== state.sessionDragProjectID);
  }
}

function validSessionDropTarget(session) {
  return Boolean(
    state.sessionDragID
      && state.sessionDragID !== session.id
      && state.sessionDragProjectID === (session.project_id || "")
      && !state.sessionOrderSaving,
  );
}

function clearSessionDropMarkers() {
  for (const row of document.querySelectorAll(".session-row.drop-before, .session-row.drop-after")) {
    row.classList.remove("drop-before", "drop-after");
  }
}

function markSessionDropTarget(event, row, session) {
  if (!validSessionDropTarget(session)) {
    if (event.dataTransfer) event.dataTransfer.dropEffect = "none";
    return;
  }
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
  clearSessionDropMarkers();
  const bounds = row.getBoundingClientRect();
  row.classList.add(event.clientY < bounds.top + bounds.height / 2 ? "drop-before" : "drop-after");
}

function clearSessionDropTarget(event, row) {
  if (event.relatedTarget && row.contains(event.relatedTarget)) return;
  row.classList.remove("drop-before", "drop-after");
}

function dropSession(event, row, session) {
  if (!validSessionDropTarget(session)) return;
  event.preventDefault();
  const bounds = row.getBoundingClientRect();
  const insertBefore = row.classList.contains("drop-before")
    || (!row.classList.contains("drop-after") && event.clientY < bounds.top + bounds.height / 2);
  void persistSessionOrder(session.id, insertBefore);
}

function clearSessionDragState() {
  state.sessionDragID = "";
  state.sessionDragProjectID = "";
  clearSessionDropMarkers();
  for (const group of document.querySelectorAll(".project-group.session-drop-disabled")) {
    group.classList.remove("session-drop-disabled");
  }
  for (const row of document.querySelectorAll(".session-row.dragging")) row.classList.remove("dragging");
}

async function persistSessionOrder(targetSessionID, insertBefore) {
  const sourceSessionID = state.sessionDragID;
  const projectID = state.sessionDragProjectID;
  const workspaceID = state.workspace?.id || "";
  const originalSessions = state.sessions;
  clearSessionDragState();
  if (!sourceSessionID || !workspaceID || state.sessionOrderSaving) return;

  const ordered = orderedProjectSessions(state.sessions.filter((session) => (
    !session.pinned
      && session.workspace_id === workspaceID
      && (session.project_id || "") === projectID
  )));
  const source = ordered.find((session) => session.id === sourceSessionID);
  const target = ordered.find((session) => session.id === targetSessionID);
  if (!source || !target) return;
  const reordered = ordered.filter((session) => session.id !== sourceSessionID);
  let targetIndex = reordered.findIndex((session) => session.id === targetSessionID);
  if (!insertBefore) targetIndex += 1;
  reordered.splice(targetIndex, 0, source);
  if (reordered.every((session, index) => session.id === ordered[index]?.id)) return;

  const positionByID = new Map(reordered.map((session, index) => [session.id, index]));
  state.sessionOrderSaving = true;
  state.sessions = state.sessions.map((session) => positionByID.has(session.id)
    ? { ...session, position: positionByID.get(session.id) }
    : session);
  renderNavigation();
  try {
    const values = await request(`/api/v1/agents/${encodeURIComponent(state.agent.id)}/sessions/order`, {
      method: "PUT",
      body: JSON.stringify({
        workspace_id: workspaceID,
        project_id: projectID,
        session_ids: reordered.map((session) => session.id),
      }),
    });
    const updatedByID = new Map(values.map((session) => [session.id, session]));
    state.sessions = state.sessions.map((session) => updatedByID.get(session.id) || session);
    if (state.session && updatedByID.has(state.session.id)) state.session = updatedByID.get(state.session.id);
  } catch (error) {
    state.sessions = originalSessions;
    toast(error.message);
  } finally {
    state.sessionOrderSaving = false;
    renderNavigation();
  }
}

function iconButton(content, label, listener) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "icon-button";
  if (typeof content === "string") button.textContent = content;
  else button.append(content);
  button.title = label;
  button.setAttribute("aria-label", label);
  button.addEventListener("click", (event) => {
    event.stopPropagation();
    listener();
  });
  return button;
}

function chatBubbleIcon() {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.classList.add("chat-bubble-icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(namespace, "path");
  path.setAttribute("d", "M5 5.75h14v9.5H10l-4.5 3v-3H5z");
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.8");
  path.setAttribute("stroke-linejoin", "round");
  svg.append(path);
  return svg;
}

function folderIcon() {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.classList.add("folder-icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(namespace, "path");
  path.setAttribute("d", "M3.5 7.25A1.75 1.75 0 0 1 5.25 5.5h4.1l2 2h7.4a1.75 1.75 0 0 1 1.75 1.75v8.5a1.75 1.75 0 0 1-1.75 1.75H5.25a1.75 1.75 0 0 1-1.75-1.75Z");
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.7");
  path.setAttribute("stroke-linecap", "round");
  path.setAttribute("stroke-linejoin", "round");
  svg.append(path);
  return svg;
}

function projectCaretIcon(collapsed) {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.classList.add("caret");
  if (collapsed) svg.classList.add("collapsed");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(namespace, "path");
  path.setAttribute("d", "m7.5 9.5 4.5 4.5 4.5-4.5");
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.8");
  path.setAttribute("stroke-linecap", "round");
  path.setAttribute("stroke-linejoin", "round");
  svg.append(path);
  return svg;
}

function openSessionContextMenu(event, session) {
  event.preventDefault();
  event.stopPropagation();
  showSessionContextMenu(session, event.clientX, event.clientY, false);
}

function openProjectContextMenu(event, project) {
  event.preventDefault();
  event.stopPropagation();
  showProjectContextMenu(project, event.clientX, event.clientY, false);
}

function showProjectContextMenu(project, clientX, clientY, focusMenu) {
  if (!project) return;
  closeSessionContextMenu();
  closeResourceContextMenu();
  state.contextProject = project;
  const menu = $("projectContextMenu");
  const hasDirectory = Array.isArray(project.sandbox_roots) && project.sandbox_roots.some((root) => String(root || "").trim());
  $("openContextProjectDirectory").disabled = !hasDirectory;
  $("openContextProjectDirectory").title = hasDirectory ? "開啟專案目錄" : "此專案尚未設定 Sandbox 目錄";
  $("manageContextProject").disabled = !state.backendHealthy;
  $("deleteContextProject").disabled = !state.backendHealthy;
  $("newContextProjectSession").disabled = !state.backendHealthy || !state.agent;
  menu.style.left = "0px";
  menu.style.top = "0px";
  menu.classList.remove("hidden");
  const bounds = menu.getBoundingClientRect();
  const left = Math.max(8, Math.min(clientX, window.innerWidth - bounds.width - 8));
  const top = Math.max(8, Math.min(clientY, window.innerHeight - bounds.height - 8));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
  if (focusMenu) menu.querySelector("button:not(:disabled)")?.focus();
}

function closeProjectContextMenu() {
  const menu = $("projectContextMenu");
  if (menu.classList.contains("hidden")) return;
  menu.classList.add("hidden");
  state.contextProject = null;
}

function showSessionContextMenu(session, clientX, clientY, focusMenu) {
  const menu = $("sessionContextMenu");
  closeProjectContextMenu();
  closeResourceContextMenu();
  state.contextSession = session;
  $("pinContextSessionLabel").textContent = session.pinned ? "取消釘選" : "釘選對話";
  for (const id of ["renameContextSession", "pinContextSession", "deleteContextSession"]) {
    $(id).disabled = sessionRunIsActive(session.id);
  }
  menu.style.left = "0px";
  menu.style.top = "0px";
  menu.classList.remove("hidden");
  const bounds = menu.getBoundingClientRect();
  const left = Math.max(8, Math.min(clientX, window.innerWidth - bounds.width - 8));
  const top = Math.max(8, Math.min(clientY, window.innerHeight - bounds.height - 8));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
  if (focusMenu) menu.querySelector("button:not(:disabled)")?.focus();
}

function closeSessionContextMenu() {
  const menu = $("sessionContextMenu");
  if (menu.classList.contains("hidden")) return;
  menu.classList.add("hidden");
  state.contextSession = null;
}

function resourceFromElement(element) {
  const link = element?.closest?.(".resource-link[data-resource-kind][data-resource-target]");
  if (!link) return null;
  const kind = link.dataset.resourceKind;
  const target = link.dataset.resourceTarget || "";
  if (!target || !["url", "path"].includes(kind)) return null;
  return { kind, target };
}

function showResourceContextMenu(event, resource) {
  event.preventDefault();
  event.stopPropagation();
  closeSessionContextMenu();
  closeProjectContextMenu();
  state.contextResource = resource;
  const menu = $("resourceContextMenu");
  const menuHost = event.target.closest?.("dialog[open]") || document.body;
  if (menu.parentElement !== menuHost) menuHost.append(menu);
  const isURL = resource.kind === "url";
  $("openContextResourceLabel").textContent = isURL ? "使用預設瀏覽器開啟" : "開啟";
  $("copyContextResourceLabel").textContent = isURL ? "複製網址" : "複製路徑";
  $("revealContextResource").classList.toggle("hidden", isURL);
  menu.style.left = "0px";
  menu.style.top = "0px";
  menu.classList.remove("hidden");
  const bounds = menu.getBoundingClientRect();
  const left = Math.max(8, Math.min(event.clientX, window.innerWidth - bounds.width - 8));
  const top = Math.max(8, Math.min(event.clientY, window.innerHeight - bounds.height - 8));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
}

function closeResourceContextMenu() {
  const menu = $("resourceContextMenu");
  if (menu.classList.contains("hidden")) return;
  menu.classList.add("hidden");
  state.contextResource = null;
}

async function openResource(resource, action = "open") {
  if (!resource) return;
  try {
    await desktop("resources/open", {
      method: "POST",
      body: JSON.stringify({ kind: resource.kind, target: resource.target, action }),
    });
  } catch (error) {
    toast(error.message);
  }
}

function toggleProject(projectID) {
  if (state.collapsedProjects.has(projectID)) state.collapsedProjects.delete(projectID);
  else state.collapsedProjects.add(projectID);
  localStorage.setItem("collapsedProjects", JSON.stringify([...state.collapsedProjects]));
  renderNavigation();
}

function toggleScheduleSection() {
  state.scheduleSectionCollapsed = !state.scheduleSectionCollapsed;
  localStorage.setItem("scheduleSectionCollapsed", state.scheduleSectionCollapsed ? "1" : "0");
  renderSchedules();
}

function toggleProjectSection() {
  state.projectSectionCollapsed = !state.projectSectionCollapsed;
  localStorage.setItem("projectSectionCollapsed", state.projectSectionCollapsed ? "1" : "0");
  renderNavigation();
}

function renderSchedules() {
  const collapsed = state.scheduleSectionCollapsed;
  const toggle = $("scheduleToggle");
  toggle.setAttribute("aria-expanded", String(!collapsed));
  toggle.querySelector(".caret")?.classList.toggle("collapsed", collapsed);
  const list = $("scheduleList");
  list.classList.toggle("hidden", collapsed);
  list.replaceChildren();
  if (collapsed) return;
  if (state.schedules.length === 0) {
    const empty = document.createElement("small");
    empty.className = "schedule-empty";
    empty.textContent = translate("尚無排程");
    list.append(empty);
    return;
  }
  list.append(...state.schedules.map(scheduleNode));
}

function scheduleNode(schedule) {
  const row = document.createElement("div");
  row.className = `schedule-row ${schedule.enabled ? "" : "is-disabled"}`;
  row.dataset.scheduleId = schedule.id;
  const open = document.createElement("button");
  open.type = "button";
  open.className = "schedule-open";
  const copy = document.createElement("span");
  copy.className = "schedule-copy";
  const name = document.createElement("span");
  name.textContent = schedule.name || translate("未命名排程");
  name.title = schedule.prompt || "";
  name.setAttribute("data-i18n-ignore", "");
  const meta = document.createElement("small");
  meta.textContent = scheduleMetaText(schedule);
  if (schedule.last_status === "failed") meta.classList.add("is-failed");
  copy.append(name, meta);
  open.append(scheduleClockIcon(), copy);
  open.addEventListener("click", () => openScheduleTarget(schedule));
  const actions = document.createElement("div");
  actions.className = "schedule-row-actions";
  const run = iconButton(schedulePlayIcon(), "立即執行一次", () => void runScheduleNow(schedule));
  run.classList.add("schedule-run-button");
  run.disabled = !state.backendHealthy;
  const manage = iconButton("⋯", "管理排程", () => openScheduleDialog(schedule));
  manage.classList.add("schedule-manage-button");
  manage.disabled = !state.backendHealthy;
  actions.append(run, manage);
  row.append(open, actions);
  return row;
}

// 點排程列優先開啟它最近一次建立的對話；還沒跑過就直接開設定。
function openScheduleTarget(schedule) {
  const session = state.sessions.find((item) => item.id === schedule.last_session_id);
  if (session) {
    void selectSession(session);
    return;
  }
  openScheduleDialog(schedule);
}

function scheduleMetaText(schedule) {
  const summary = scheduleRecurrenceText(schedule.recurrence || {});
  if (!schedule.enabled) return `${summary} · ${translate("已停用")}`;
  if (schedule.last_status === "failed") return `${summary} · ${translate("上次啟動失敗")}`;
  if (!schedule.next_run_at) return summary;
  return `${summary} · ${translate("下次 %s").replace("%s", formatScheduleMoment(schedule.next_run_at))}`;
}

function scheduleRecurrenceText(recurrence) {
  if (recurrence.frequency === "daily") {
    return translate("每天 %s").replace("%s", recurrence.time_of_day || "");
  }
  if (recurrence.frequency === "weekly") {
    const language = window.NRInternI18n?.language || "zh-TW";
    const separator = language === "zh-TW" || language === "ja" ? "、" : ", ";
    const days = (recurrence.weekdays || []).map((value) => translate(scheduleWeekdayNames[value] || "")).join(separator);
    return `${days} ${recurrence.time_of_day || ""}`.trim();
  }
  const minutes = Number(recurrence.interval_minutes) || 0;
  if (minutes >= 60 && minutes % 60 === 0) {
    const hours = minutes / 60;
    return hours === 1 ? translate("每 1 小時") : translate("每 %s 小時").replace("%s", String(hours));
  }
  return minutes === 1 ? translate("每 1 分鐘") : translate("每 %s 分鐘").replace("%s", String(minutes));
}

function formatScheduleMoment(value) {
  const date = new Date(value || "");
  if (Number.isNaN(date.getTime())) return "-";
  const language = window.NRInternI18n?.language || "zh-TW";
  const sameDay = date.toDateString() === new Date().toDateString();
  // 側邊欄空間有限，一律用 24 小時制，避免「下午01:46」這種較長的表示法。
  return new Intl.DateTimeFormat(language, sameDay
    ? { hour: "2-digit", minute: "2-digit", hour12: false }
    : { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
}

function scheduleClockIcon() {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.classList.add("schedule-icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const circle = document.createElementNS(namespace, "circle");
  circle.setAttribute("cx", "12");
  circle.setAttribute("cy", "12");
  circle.setAttribute("r", "8.2");
  const hands = document.createElementNS(namespace, "path");
  hands.setAttribute("d", "M12 7.4v5l3 1.8");
  for (const node of [circle, hands]) {
    node.setAttribute("fill", "none");
    node.setAttribute("stroke", "currentColor");
    node.setAttribute("stroke-width", "1.7");
    node.setAttribute("stroke-linecap", "round");
    node.setAttribute("stroke-linejoin", "round");
    svg.append(node);
  }
  return svg;
}

function schedulePlayIcon() {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(namespace, "path");
  path.setAttribute("d", "M8.5 6.4 17 12l-8.5 5.6Z");
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.7");
  path.setAttribute("stroke-linejoin", "round");
  svg.append(path);
  return svg;
}

function renderProjectOptions() {
  for (const select of [$("newSessionProject"), $("settingProject")]) {
    const selected = select.value;
    select.replaceChildren(new Option("未分類", ""));
    for (const project of state.projects) select.add(new Option(project.name, project.id));
    if ([...select.options].some((option) => option.value === selected)) select.value = selected;
  }
}

// 執行用選單只顯示已啟用 Provider。一般 Provider API 本身只回傳執行路由中
// 的啟用項目；管理設定若已載入，再額外防止尚未刷新完成的停用項目短暫出現。
function selectableProviders() {
  const disabledIDs = new Set((state.providerSettings?.providers || [])
    .filter((provider) => provider.enabled === false)
    .map((provider) => provider.id));
  return state.providers.filter((provider) => provider.enabled !== false && !disabledIDs.has(provider.id));
}

function providerDisplayName(provider) {
  if (typeof provider === "string") provider = state.providers.find((item) => item.id === provider);
  return String(provider?.display_name || provider?.id || "").trim();
}

function activeProviderID() {
  return String(state.session?.provider_id || state.workspace?.default_provider_id || "").trim();
}

function activeContextIdentity(session = state.session) {
  const providerID = String(session?.provider_id || state.workspace?.default_provider_id || "").trim();
  const provider = state.providers.find((item) => item.id === providerID);
  const model = String(session?.model || state.workspace?.model || provider?.default_model || "").trim();
  return {
    sessionID: String(session?.id || ""),
    providerID,
    model,
    key: `${providerID}\u0000${model}`,
  };
}

function formatContextTokenCount(value) {
  if (value === null || value === undefined || value === "") return "-";
  const number = Number(value);
  if (!Number.isFinite(number) || number < 0) return "-";
  const language = window.NRInternI18n?.language || "zh-TW";
  return new Intl.NumberFormat(language, { maximumFractionDigits: 0 }).format(number);
}

function formatSessionUsageCost(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number < 0) return "—";
  const language = window.NRInternI18n?.language || "zh-TW";
  return new Intl.NumberFormat(language, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 4,
    maximumFractionDigits: 6,
  }).format(number);
}

function renderSessionUsage() {
  const control = $("sessionUsage");
  if (!control) return;
  const usage = state.session?.usage;
  control.classList.toggle("hidden", !state.session || !usage);
  if (!state.session || !usage) return;
  const total = Number(usage.total_tokens);
  const tokenText = Number.isFinite(total) && total >= 0
    ? `${formatContextTokenCount(total)} ${translate("tokens")}`
    : `— ${translate("tokens")}`;
  $("sessionUsageTokens").textContent = tokenText;
  const hasCost = usage.estimated_cost_usd !== undefined && usage.estimated_cost_usd !== null;
  $("sessionUsageCost").textContent = hasCost
    ? formatSessionUsageCost(usage.estimated_cost_usd)
    : "—";
  const title = hasCost
    ? `${translate("Session 用量")} · ${translate("估算成本")}`
    : `${translate("Session 用量")} · ${translate("尚未設定模型價格")}`;
  control.title = title;
  control.setAttribute("aria-label", `${tokenText} · ${$("sessionUsageCost").textContent}`);
}

function contextUsageSnapshot() {
  const identity = activeContextIdentity();
  const usage = state.contextUsage;
  const capabilities = state.contextCapabilities?.key === identity.key ? state.contextCapabilities : null;
  const usageMatches = usage?.sessionID === identity.sessionID
    && usage?.providerID === identity.providerID
    && usage?.model === identity.model;
  const inputTokens = usageMatches && Number.isFinite(Number(usage.inputTokens)) ? Number(usage.inputTokens) : null;
  // 相容服務與 Codex 模型不一定提供 Context Window；未提供時以 256K
  // 作為 UI 的保守上限，避免百分比與剩餘空間永遠無法計算。
  const contextWindow = Number(capabilities?.context_window) > 0
    ? Number(capabilities.context_window)
    : fallbackContextWindowTokens;
  const maxOutputTokens = Number(capabilities?.max_output_tokens) > 0 ? Number(capabilities.max_output_tokens) : null;
  const percent = inputTokens !== null && contextWindow
    ? Math.max(0, Math.min(100, (inputTokens / contextWindow) * 100))
    : null;
  return {
    ...identity,
    inputTokens,
    contextWindow,
    maxOutputTokens,
    remainingTokens: inputTokens !== null && contextWindow ? Math.max(0, contextWindow - inputTokens) : null,
    percent,
  };
}

function renderContextUsage() {
  const control = $("contextUsageControl");
  if (!control) return;
  const visible = Boolean(state.session);
  control.classList.toggle("hidden", !visible);
  if (!visible) {
    closeContextUsagePopover();
    return;
  }
  const snapshot = contextUsageSnapshot();
  const percentText = snapshot.percent === null ? "-" : `${formatProviderUsagePercent(snapshot.percent)}%`;
  $("contextUsagePercent").textContent = percentText;
  $("contextUsageDetailPercent").textContent = percentText;
  $("contextUsageProvider").textContent = providerDisplayName(snapshot.providerID) || snapshot.providerID || "-";
  $("contextUsageModel").textContent = snapshot.model || "-";
  $("contextUsageInput").textContent = formatContextTokenCount(snapshot.inputTokens);
  $("contextUsageLimit").textContent = formatContextTokenCount(snapshot.contextWindow);
  $("contextUsageOutputLimit").textContent = formatContextTokenCount(snapshot.maxOutputTokens);
  $("contextUsageRemaining").textContent = formatContextTokenCount(snapshot.remainingTokens);
  const track = $("contextUsageTrack");
  const bar = $("contextUsageBar");
  if (snapshot.percent === null) {
    track.classList.add("unknown");
    track.removeAttribute("aria-valuenow");
    track.setAttribute("aria-valuetext", "-");
    bar.style.width = "0%";
  } else {
    track.classList.remove("unknown");
    track.setAttribute("aria-valuenow", String(snapshot.percent));
    track.setAttribute("aria-valuetext", percentText);
    bar.style.width = `${snapshot.percent}%`;
  }
}

function recordContextUsage(usage, sessionID = state.session?.id, providerID = activeContextIdentity().providerID, model = activeContextIdentity().model) {
  const inputTokens = Number(usage?.input_tokens);
  if (!sessionID || !Number.isFinite(inputTokens) || inputTokens < 0) return;
  state.contextUsage = {
    sessionID: String(sessionID),
    providerID: String(providerID || "").trim(),
    model: String(model || "").trim(),
    inputTokens,
    outputTokens: Number(usage?.output_tokens) || 0,
    totalTokens: Number(usage?.total_tokens) || 0,
  };
  renderContextUsage();
}

async function loadContextCapabilities(sessionID = state.session?.id) {
  const identity = activeContextIdentity();
  if (!sessionID || identity.sessionID !== sessionID || !identity.providerID) {
    state.contextCapabilities = null;
    renderContextUsage();
    return;
  }
  const cached = state.contextCapabilityCache[identity.key];
  if (cached) {
    state.contextCapabilities = cached;
    renderContextUsage();
    return;
  }
  const requestID = ++state.contextCapabilitiesRequest;
  state.contextCapabilities = null;
  renderContextUsage();
  try {
    const query = identity.model ? `?model=${encodeURIComponent(identity.model)}` : "";
    const value = await request(`/api/v1/providers/${encodeURIComponent(identity.providerID)}/capabilities${query}`);
    const capabilities = { ...value, key: identity.key };
    state.contextCapabilityCache[identity.key] = capabilities;
    if (requestID !== state.contextCapabilitiesRequest || activeContextIdentity().key !== identity.key) return;
    state.contextCapabilities = capabilities;
  } catch (_) {
    if (requestID === state.contextCapabilitiesRequest) state.contextCapabilities = { key: identity.key };
  }
  renderContextUsage();
}

function closeContextUsagePopover() {
  $("contextUsagePopover")?.classList.add("hidden");
  $("contextUsageButton")?.setAttribute("aria-expanded", "false");
}

function toggleContextUsagePopover() {
  const popover = $("contextUsagePopover");
  if (!popover) return;
  const opening = popover.classList.contains("hidden");
  popover.classList.toggle("hidden", !opening);
  $("contextUsageButton").setAttribute("aria-expanded", String(opening));
  if (opening) {
    renderContextUsage();
    void loadContextCapabilities();
  }
}

function formatProviderUsagePercent(value) {
  const number = Math.max(0, Math.min(100, Number(value)));
  return Number.isInteger(number) ? String(number) : number.toFixed(1).replace(/\.0$/, "");
}

function formatProviderUsageReset(value, includeDate) {
  const date = new Date(value || "");
  if (Number.isNaN(date.getTime())) return "-";
  const language = window.NRInternI18n?.language || "zh-TW";
  return new Intl.DateTimeFormat(language, includeDate
    ? { year: "numeric", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit" }
    : { hour: "2-digit", minute: "2-digit" }).format(date);
}

function renderProviderUsageWindow(prefix, windowValue, includeDate) {
  const available = Boolean(windowValue?.available) && Number.isFinite(Number(windowValue?.remaining_percent));
  const track = $(`providerUsage${prefix}Track`);
  const bar = $(`providerUsage${prefix}Bar`);
  const value = $(`providerUsage${prefix}Value`);
  const reset = $(`providerUsage${prefix}Reset`);
  if (!available) {
    track.classList.add("unknown");
    track.removeAttribute("aria-valuenow");
    track.setAttribute("aria-valuetext", "-");
    bar.style.width = "0%";
    value.textContent = "-";
    reset.textContent = "-";
    return;
  }
  const remaining = Math.max(0, Math.min(100, Number(windowValue.remaining_percent)));
  const text = `${formatProviderUsagePercent(remaining)}%`;
  track.classList.remove("unknown");
  track.setAttribute("aria-valuenow", String(remaining));
  track.setAttribute("aria-valuetext", text);
  bar.style.width = `${remaining}%`;
  value.textContent = text;
  reset.textContent = formatProviderUsageReset(windowValue.reset_at, includeDate);
}

function renderProviderUsage(usage) {
  renderProviderUsageWindow("FiveHour", usage?.five_hour, false);
  renderProviderUsageWindow("SevenDay", usage?.seven_day, true);
}

async function loadActiveProviderUsage() {
  const providerID = activeProviderID();
  const requestID = ++state.providerUsageRequest;
  renderProviderUsage(null);
  if (!providerID || !state.backendHealthy) return;
  try {
    const usage = await request(`/api/v1/providers/${encodeURIComponent(providerID)}/usage`, { reconnects: 0 });
    if (requestID !== state.providerUsageRequest || activeProviderID() !== providerID) return;
    renderProviderUsage(usage);
  } catch (_) {
    if (requestID === state.providerUsageRequest) renderProviderUsage(null);
  }
}

function closeProviderUsagePopover() {
  $("providerUsagePopover").classList.add("hidden");
  $("providerUsageButton").setAttribute("aria-expanded", "false");
}

function refreshOpenProviderUsage() {
  if (!$("providerUsagePopover").classList.contains("hidden")) void loadActiveProviderUsage();
}

function toggleProviderUsagePopover() {
  const popover = $("providerUsagePopover");
  if (!popover.classList.contains("hidden")) {
    closeProviderUsagePopover();
    return;
  }
  popover.classList.remove("hidden");
  $("providerUsageButton").setAttribute("aria-expanded", "true");
  void loadActiveProviderUsage();
}

function readMemo() {
  try {
    return localStorage.getItem(memoStorageKey) || "";
  } catch (_) {
    return "";
  }
}

function updateMemoActions() {
  $("clearMemo").disabled = !$("memoText").value;
}

function saveMemo() {
  clearTimeout(memoSaveTimer);
  memoSaveTimer = 0;
  const status = $("memoSaveState");
  try {
    localStorage.setItem(memoStorageKey, $("memoText").value);
    status.textContent = translate("已儲存");
    status.classList.remove("error");
  } catch (_) {
    status.textContent = translate("無法儲存記事，請縮短內容後重試。");
    status.classList.add("error");
  }
  updateMemoActions();
}

function scheduleMemoSave() {
  clearTimeout(memoSaveTimer);
  $("memoSaveState").textContent = translate("儲存中…");
  $("memoSaveState").classList.remove("error");
  updateMemoActions();
  memoSaveTimer = setTimeout(saveMemo, memoSaveDelayMilliseconds);
}

function openMemo() {
  closeProviderUsagePopover();
  const editor = $("memoText");
  editor.value = readMemo();
  $("memoSaveState").textContent = translate("自動儲存在本機");
  $("memoSaveState").classList.remove("error");
  updateMemoActions();
  $("memoDialog").showModal();
  requestAnimationFrame(() => {
    editor.focus();
    editor.setSelectionRange(editor.value.length, editor.value.length);
  });
}

function closeMemo() {
  if (memoSaveTimer) saveMemo();
  $("memoDialog").close();
}

async function clearMemo() {
  if (!$("memoText").value) return;
  if (!(await confirmAction(translate("確定清除所有記事內容嗎？")))) return;
  $("memoText").value = "";
  saveMemo();
  toast(translate("記事已清除"));
  $("memoText").focus();
}

function renderProviderOptions() {
  for (const select of [$("newWorkspaceProvider"), $("settingWorkspaceProvider")]) {
    const selected = select.value;
    select.replaceChildren();
    for (const provider of selectableProviders()) {
      select.add(new Option(providerDisplayName(provider), provider.id));
    }
    if ([...select.options].some((option) => option.value === selected)) select.value = selected;
  }
}

function renderSessionProviderOptions() {
  for (const select of [$("newProvider"), $("settingProvider")]) {
    const selected = select.value;
    const workspaceDefault = state.workspace?.default_provider_id || "";
    const workspaceDefaultName = providerDisplayName(workspaceDefault) || workspaceDefault;
    select.replaceChildren(new Option(workspaceDefaultName ? `使用 Workspace 預設（${workspaceDefaultName}）` : "使用 Workspace 預設", ""));
    for (const provider of selectableProviders()) {
      select.add(new Option(providerDisplayName(provider), provider.id));
    }
    if ([...select.options].some((option) => option.value === selected)) select.value = selected;
  }
}

function providerDescriptor(providerID) {
  return selectableProviders().find((provider) => provider.id === providerID) || null;
}

function defaultModelForProvider(providerID) {
  const configured = providerID === state.workspace?.default_provider_id && state.workspace?.model
    ? state.workspace.model
    : providerDescriptor(providerID)?.default_model || "";
  const catalog = providerModelCatalog(providerID);
  if (catalog !== null) {
    return catalog.includes(configured) ? configured : catalog[0] || "";
  }
  return configured;
}

// Runtime 介面只顯示 Provider 模型目錄實際回報的名稱；尚未載入或無法
// 取得目錄時不顯示未驗證的設定值，手動設定仍保留在系統管理頁。
function displayedModelForProvider(providerID, explicitModel = "") {
  providerID = String(providerID || "").trim();
  const configured = String(explicitModel || "").trim()
    || (providerID === state.workspace?.default_provider_id ? String(state.workspace?.model || "").trim() : "")
    || String(providerDescriptor(providerID)?.default_model || "").trim();
  const catalog = providerModelCatalog(providerID);
  if (catalog === null) return "";
  if (catalog.length === 0) return "";
  return catalog.includes(configured) ? configured : catalog[0];
}

async function initializeWorkspaceModels(workspaceID) {
  let workspace = state.workspaces.find((item) => item.id === workspaceID);
  if (!workspace) return;
  const providerIDs = [...new Set(workspace.provider_ids || [])];
  await Promise.all(providerIDs.map((providerID) => loadProviderModels(providerID, { notify: false })));
  workspace = state.workspaces.find((item) => item.id === workspaceID) || workspace;
  const providerID = workspace.default_provider_id || "";
  const catalog = providerModelCatalog(providerID);
  const configured = String(workspace.model || "").trim();
  if (catalog?.length && !catalog.includes(configured)) {
    try {
      const updated = await request(`/api/v1/workspaces/${encodeURIComponent(workspaceID)}`, {
        method: "PATCH",
        body: JSON.stringify({ model: catalog[0] }),
      });
      state.workspaces = state.workspaces.map((item) => item.id === updated.id ? updated : item);
      workspace = updated;
    } catch (error) {
      // 顯示層仍會採用模型目錄的第一個有效模型；持久化失敗時交由設定頁重試。
    }
  }
  if (state.workspace?.id !== workspaceID) return;
  state.workspace = workspace;
  syncWorkspaceSettings();
  renderNavigation();
  if (state.session) syncSessionUI();
}

function providerModelCatalog(providerID) {
  if (!Object.prototype.hasOwnProperty.call(state.providerModelLists, providerID)) return null;
  return [...new Set((state.providerModelLists[providerID] || [])
    .map((value) => String(value || "").trim())
    .filter(Boolean))];
}

function sessionRuntimeModels(providerID) {
  return providerModelCatalog(providerID) || [];
}

function closeModelPopover() {
  $("sessionRuntimeControls")?.classList.add("hidden");
  $("modelControlButton")?.setAttribute("aria-expanded", "false");
}

function toggleModelPopover() {
  const popover = $("sessionRuntimeControls");
  if (!popover || !state.session) return;
  const open = popover.classList.toggle("hidden");
  $("modelControlButton")?.setAttribute("aria-expanded", String(!open));
}

function syncModelCapsule(providerID, model, catalog) {
  const button = $("modelControlButton");
  const value = $("modelCapsuleValue");
  if (!button || !value) return;
  const providerName = providerDisplayName(providerID) || providerID;
  const displayModel = catalog === null
    ? (state.providerModelErrors[providerID] ? "無法載入" : "載入中…")
    : model || "未設定";
  value.textContent = displayModel;
  button.title = providerName ? `${providerName} · ${displayModel}` : `模型：${displayModel}`;
  button.setAttribute("aria-label", providerName
    ? `模型：${displayModel}（${providerName}）`
    : `模型：${displayModel}`);
}

function syncSessionRuntimeControlState() {
  const disabled = !state.session || selectedSessionIsRunning() || state.sessionRuntimeSaving;
  const providerID = state.session?.provider_id || state.workspace?.default_provider_id || "";
  $("sessionProviderSelect").disabled = disabled;
  $("sessionModelSelect").disabled = disabled || sessionRuntimeModels(providerID).length === 0;
  $("sessionThinkingSelect").disabled = disabled;
  $("sessionRuntimeControls").setAttribute("aria-busy", state.sessionRuntimeSaving ? "true" : "false");
}

function syncSessionRuntimeControls({ loadModels = true } = {}) {
  const controls = $("sessionRuntimeControls");
  const modelControl = $("modelControl");
  const session = state.session;
  modelControl.classList.toggle("hidden", !session);
  if (!session) {
    closeModelPopover();
    $("sessionProviderSelect").replaceChildren();
    $("sessionModelSelect").replaceChildren();
    $("sessionThinkingSelect").value = "";
    $("modelCapsuleValue").textContent = "—";
    syncSessionRuntimeControlState();
    return;
  }

  const providerID = session.provider_id || state.workspace?.default_provider_id || "";
  const providerSelect = $("sessionProviderSelect");
  providerSelect.replaceChildren();
  for (const provider of selectableProviders()) {
    providerSelect.add(new Option(providerDisplayName(provider), provider.id));
  }
  providerSelect.value = providerID;

  const model = session.model || defaultModelForProvider(providerID);
  const catalog = providerModelCatalog(providerID);
  const models = catalog || [];
  const modelSelect = $("sessionModelSelect");
  modelSelect.replaceChildren();
  for (const value of models) modelSelect.add(new Option(value, value));
  if (catalog === null) {
    modelSelect.add(new Option(state.providerModelErrors[providerID] ? "-" : "正在載入模型…", ""));
  } else if (models.length === 0) {
    modelSelect.add(new Option("-", ""));
  }
  const validModel = models.includes(model);
  modelSelect.value = validModel ? model : models[0] || "";
  const thinkingMode = String(session.thinking_mode || session.metadata?.thinking_mode || "").trim().toLowerCase();
  const thinkingSelect = $("sessionThinkingSelect");
  thinkingSelect.value = [...thinkingSelect.options].some((option) => option.value === thinkingMode)
    ? thinkingMode
    : "";
  syncModelCapsule(providerID, modelSelect.value, catalog);
  syncSessionRuntimeControlState();

  if (loadModels && providerID && catalog === null && !state.providerModelErrors[providerID]) {
    void ensureSessionProviderModels(providerID, session.id);
  }
  if (catalog !== null && models.length > 0 && !validModel && !selectedSessionIsRunning() && !state.sessionRuntimeSaving) {
    void updateSessionRuntime(session.id, { model: defaultModelForProvider(providerID) || models[0] });
  }
}

async function ensureSessionProviderModels(providerID, sessionID) {
  if (state.providerModelLoads[providerID]) return state.providerModelLoads[providerID];
  const loading = loadProviderModels(providerID, { notify: false });
  state.providerModelLoads[providerID] = loading;
  try {
    await loading;
  } finally {
    delete state.providerModelLoads[providerID];
  }
  if (state.session?.id === sessionID && (state.session.provider_id || state.workspace?.default_provider_id) === providerID) {
    syncSessionRuntimeControls({ loadModels: false });
  }
  return state.providerModelLists[providerID] || [];
}

// sessionBusyError 判斷後端是否因為「這個對話還有排隊或執行中的 Run」而拒絕。
// 後端的原始訊息（conflict: session has a queued or running run）對使用者沒有意義，
// 而且通常代表前端的 Run 狀態已經和後端不同步。
function sessionBusyError(error) {
  const detail = String(error?.message || "").toLowerCase();
  return detail.includes("queued or running run") || detail.includes("queued, running");
}

// resyncActiveRun 重新向後端確認這個對話是否真的還有進行中的 Run。
// 前端以為已經結束、後端還沒收尾時，畫面必須回到「執行中」，使用者才能取消它，
// 而不是反覆看到一則看不懂的錯誤。
async function resyncActiveRun(sessionID) {
  if (!sessionID) return false;
  try {
    const values = await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/runs`, { reconnects: 1 });
    const active = (Array.isArray(values) ? values : []).find((run) => activeRunStatuses.has(run.status));
    if (!active) return false;
    attachExistingRun(active);
    syncSelectedRunUI();
    return true;
  } catch (_) {
    return false;
  }
}

async function updateSessionRuntime(sessionID, input) {
  state.sessionRuntimeSaving = true;
  syncSessionRuntimeControlState();
  try {
    const updated = await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}`, {
      method: "PATCH",
      reconnects: 3,
      body: JSON.stringify(input),
    });
    state.sessions = state.sessions.map((item) => item.id === updated.id ? updated : item);
    if (state.session?.id === updated.id) {
      state.session = updated;
      syncSessionUI();
    }
    renderNavigation();
    return updated;
  } catch (error) {
    if (state.session?.id === sessionID) syncSessionRuntimeControls({ loadModels: false });
    if (sessionBusyError(error)) {
      const restored = await resyncActiveRun(sessionID);
      toast(restored
        ? translate("這個對話還有進行中的工作，完成或取消後才能調整 Provider 與模型")
        : translate("後端仍在收尾上一個工作，稍待幾秒再試一次"));
      return null;
    }
    toast(error.message);
    return null;
  } finally {
    state.sessionRuntimeSaving = false;
    syncSessionRuntimeControlState();
    syncPlanLockControl();
  }
}

async function changeSessionProvider(event) {
  const sessionID = state.session?.id;
  const providerID = event.target.value;
  if (!sessionID || !providerID || selectedSessionIsRunning()) return;
  state.sessionRuntimeSaving = true;
  syncSessionRuntimeControlState();
  await ensureSessionProviderModels(providerID, sessionID);
  const models = sessionRuntimeModels(providerID);
  if (models.length === 0) {
    state.sessionRuntimeSaving = false;
    syncSessionRuntimeControls({ loadModels: false });
    toast(state.providerModelErrors[providerID] || `Provider ${providerID} 沒有可用模型`);
    return;
  }
  const model = defaultModelForProvider(providerID) || models[0] || "";
  await updateSessionRuntime(sessionID, { provider_id: providerID, model });
}

async function changeSessionModel(event) {
  const sessionID = state.session?.id;
  if (!sessionID || selectedSessionIsRunning()) return;
  await updateSessionRuntime(sessionID, { model: event.target.value });
}

async function changeSessionThinking(event) {
  const sessionID = state.session?.id;
  if (!sessionID || selectedSessionIsRunning()) return;
  await updateSessionRuntime(sessionID, { thinking_mode: event.target.value });
}

function renderWorkspaceOptions() {
  for (const select of [$("workspaceSelect"), $("workspaceManagementSelect")]) {
    const selected = state.workspace?.id || select.value;
    select.replaceChildren();
    for (const workspace of state.workspaces) {
      select.add(new Option(workspace.name, workspace.id));
    }
    if ([...select.options].some((option) => option.value === selected)) select.value = selected;
  }
}

function syncWorkspaceSelectorValues(workspaceID = state.workspace?.id || "") {
  for (const select of [$("workspaceSelect"), $("workspaceManagementSelect")]) {
    if ([...select.options].some((option) => option.value === workspaceID)) select.value = workspaceID;
  }
}

async function switchWorkspace(workspaceID) {
  if (state.running) {
    syncWorkspaceSelectorValues();
    toast("Run 執行中，完成或取消後才能切換 Workspace");
    return;
  }
  const workspace = state.workspaces.find((item) => item.id === workspaceID);
  if (!workspace) {
    syncWorkspaceSelectorValues();
    return;
  }
  if (workspace.id === state.workspace?.id) {
    syncWorkspaceSelectorValues(workspace.id);
    return;
  }
  state.workspace = workspace;
  syncWorkspaceSelectorValues(workspace.id);
  sessionStorage.setItem("activeWorkspaceID", workspace.id);
  state.projects = [];
  state.sessions = [];
  state.schedules = [];
  state.session = null;
  clearPendingAttachments();
  clearSessionUI();
  syncWorkspaceSettings();
  void initializeWorkspaceModels(workspace.id);
  renderNavigation();
  try {
    await Promise.all([loadProjects(), loadSessions(), loadSchedules()]);
  } catch (error) {
    toast(error.message);
  }
}

function clearSessionUI() {
	state.messageAutoScroll = true;
	state.plans = [];
	state.planTab = "active";
	state.planEditingID = "";
	state.expandedPlanIDs.clear();
	state.planExpansionInitialized = false;
	state.activePlanID = "";
	state.planDragID = "";
  state.contextUsage = null;
  state.contextCapabilities = null;
  state.contextCapabilitiesRequest += 1;
  closeContextUsagePopover();
  $("sessionTitle").textContent = state.workspace?.name || "選擇或建立對話";
  $("sessionUsage").classList.add("hidden");
  $("sessionUsageTokens").textContent = "";
  $("sessionUsageCost").textContent = "";
  const workspaceModel = displayedModelForProvider(state.workspace?.default_provider_id, state.workspace?.model);
  $("agentLabel").textContent = state.workspace
    ? `${providerDisplayName(state.workspace.default_provider_id) || state.workspace.default_provider_id}${workspaceModel ? ` · ${workspaceModel}` : ""}`
    : state.agent?.name || "General Harness Agent";
  syncSessionRuntimeControls({ loadModels: false });
  syncPlanButton();
  renderContextUsage();
  $("prompt").disabled = true;
  syncRunActionButton();
  $("messages").replaceChildren();
  $("messages").classList.add("hidden");
  $("emptyState").classList.remove("hidden");
  $("sessionSettings").classList.add("hidden");
  $("noSessionManagement").classList.add("hidden");
  refreshOpenProviderUsage();
}

function syncWorkspaceSettings() {
  const workspace = state.workspace;
  $("workspaceSettings").classList.toggle("hidden", !workspace);
  $("noWorkspaceSettings").classList.toggle("hidden", Boolean(workspace));
  if (!workspace) return;
  syncWorkspaceSelectorValues(workspace.id);
  renderProviderOptions();
  renderSessionProviderOptions();
  $("settingWorkspaceName").value = workspace.name || "";
  $("settingWorkspaceDescription").value = workspace.description || "";
  $("settingWorkspaceProvider").value = workspace.default_provider_id || "";
  $("settingWorkspaceModel").value = workspace.model || "";
  $("settingWorkspaceInstructions").value = workspace.instructions || "";
  if (!state.session) clearSessionUI();
}

async function selectSession(session) {
  const selectionVersion = ++state.sessionSelectionVersion;
  const previousRun = activeRunFor(state.session?.id);
  if (previousRun) previousRun.liveMessage = null;
  state.liveMessage = null;
  state.messageAutoScroll = true;
  if (session?.id && session.id !== state.attachmentSessionId) clearPendingAttachments();
  try {
    const selected = await request(`/api/v1/sessions/${encodeURIComponent(session.id)}`);
    if (selectionVersion !== state.sessionSelectionVersion) return;
    state.session = selected;
    state.plans = [];
    state.planTab = "active";
    state.planEditingID = "";
    state.expandedPlanIDs.clear();
    state.planExpansionInitialized = false;
    state.activePlanID = "";
    state.planDragID = "";
    state.contextUsage = null;
    state.contextCapabilities = null;
    syncSessionUI();
    renderNavigation();
    await Promise.all([loadMessages(), loadPlans(selected.id)]);
    if (selectionVersion !== state.sessionSelectionVersion) return;
    await loadSessionRunState(selected.id);
    if (selectionVersion !== state.sessionSelectionVersion) return;
    syncSelectedRunUI();
  } catch (error) {
    if (selectionVersion !== state.sessionSelectionVersion) return;
    toast(error.message);
  }
}

async function restoreWorkspaceRun() {
  if (!state.workspace) return;
  try {
    const values = await request("/api/v1/runs?status=queued,running,paused,waiting_approval", { reconnects: 1 });
    const sessions = new Map(state.sessions.map((session) => [session.id, session]));
    const activeRuns = (Array.isArray(values) ? values : [])
      .filter((value) => sessions.has(value.session_id))
      .sort((left, right) => new Date(right.created_at) - new Date(left.created_at));
    const selectedRuns = await chooseRunsToRestore(activeRuns);
    for (const run of selectedRuns) attachExistingRun(run);
    const latest = selectedRuns[0];
    if (latest && state.session?.id !== latest.session_id) await selectSession(sessions.get(latest.session_id));
    else syncSelectedRunUI();
  } catch (_) {
    // 相容檢查已保護正式後端；這裡的失敗不能阻斷使用者瀏覽既有對話。
  }
}

const restoreRunStatusLabels = Object.freeze({
  queued: "排隊中",
  running: "執行中",
  paused: "已暫停",
  waiting_approval: "等待核准",
});

function renderRestoreRunOptions({ preserveSelection = false } = {}) {
  const list = $("restoreRunsList");
  if (!list) return;
  const selectedIDs = preserveSelection
    ? new Set([...list.querySelectorAll("input[type=checkbox]:checked")].map((input) => input.value))
    : new Set(state.restoreCandidates.map((run) => run.id));
  list.replaceChildren(...state.restoreCandidates.map((run) => {
    const option = document.createElement("label");
    option.className = "restore-run-option";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.value = run.id;
    checkbox.checked = selectedIDs.has(run.id);
    checkbox.setAttribute("aria-label", translate("恢復這個對話"));
    const copy = document.createElement("span");
    copy.className = "restore-run-copy";
    const title = document.createElement("strong");
    title.textContent = state.sessions.find((session) => session.id === run.session_id)?.title || translate("未命名");
    title.setAttribute("data-i18n-ignore", "true");
    const status = document.createElement("small");
    status.textContent = translate(restoreRunStatusLabels[run.status] || "執行中");
    const updated = document.createElement("small");
    const updatedAt = run.updated_at || run.created_at;
    updated.textContent = `${translate("上次更新")}：${updatedAt ? new Date(updatedAt).toLocaleString() : "—"}`;
    copy.append(title, status, updated);
    option.append(checkbox, copy);
    return option;
  }));
}

function chooseRunsToRestore(runs) {
  if (!runs.length) return Promise.resolve([]);
  const dialog = $("restoreRunsDialog");
  if (!dialog) return Promise.resolve(runs);
  state.restoreCandidates = runs;
  renderRestoreRunOptions();
  dialog.showModal();
  return new Promise((resolve) => {
    dialog.addEventListener("close", () => {
      const candidates = [...state.restoreCandidates];
      const selected = dialog.returnValue === "confirm"
        ? candidates.filter((run) => [...$("restoreRunsList").querySelectorAll("input[type=checkbox]:checked")].some((input) => input.value === run.id))
        : [];
      const selectedIDs = new Set(selected.map((run) => run.id));
      const selectedSessionIDs = new Set(selected.map((run) => run.session_id));
      for (const run of candidates) {
        if (!selectedIDs.has(run.id)) state.restoreSkippedRunIDs.add(run.id);
        if (!selectedSessionIDs.has(run.session_id)) state.restoreSkippedSessionIDs.add(run.session_id);
      }
      state.restoreCandidates = [];
      resolve(selected);
    }, { once: true });
  });
}

async function loadSessionRunState(sessionID) {
  if (!sessionID) return;
  try {
    const values = await request(`/api/v1/runs?session_id=${encodeURIComponent(sessionID)}`);
    const runs = Array.isArray(values) ? values : [];
    const active = runs.find((value) => activeRunStatuses.has(value.status));
    // 啟動時選擇不恢復，只代表「不要自動切過去跟著看」，不代表這個 Run 不存在：
    // 後端仍然禁止修改有進行中 Run 的 Session。使用者自己打開這個對話時就要看到
    // 真實狀態，才有辦法取消它，而不是每次調整設定都撞到 409。
    if (active && !activeRunFor(sessionID)) {
      attachExistingRun(active);
      state.restoreSkippedRunIDs.delete(active.id);
      state.restoreSkippedSessionIDs.delete(sessionID);
      return;
    }
    const retryable = runs.find((value) => ["failed", "canceled"].includes(value.status) && value.error?.retryable);
    if (retryable) state.retryableRuns.set(sessionID, retryable);
    else state.retryableRuns.delete(sessionID);
    syncRunStateAliases();
    syncSelectedRunUI();
  } catch (_) {
    // 對話內容仍可讀取；Run 狀態下次切換或輪詢時再補查。
  }
}

function attachExistingRun(run) {
  if (!run?.id || activeRunFor(run.session_id)) return;
  const sessionID = run.session_id;
  const runState = addActiveRun(newRunState(sessionID, run));
  if (run.pending_approval) showApproval(run.pending_approval, sessionID);
  renderNavigation();
  syncSelectedRunUI();
  void runWithReconnect({ runId: run.id, sessionId: sessionID, lastSequence: 0, runState })
    .catch((error) => {
      if (activeRunFor(sessionID) === runState) setRunActivity(`背景 Run 連線中斷：${error.message}`, sessionID);
    })
    .finally(async () => {
      await finishActiveRun(runState);
    });
}

async function finishActiveRun(runState) {
  if (!removeActiveRun(runState)) return;
  runState.eventController?.abort();
  // 連線中斷等情況不會送出 run.completed／run.failed，這裡用 console 自己的
  // 起算時間補上，狀態列才不會什麼都不留。
  if (!state.lastRunDuration.has(runState.sessionId) && Number.isFinite(runState.startedAtMilliseconds)) {
    const elapsed = Date.now() - runState.startedAtMilliseconds;
    if (elapsed > 0) state.lastRunDuration.set(runState.sessionId, elapsed);
  }
  if (runState.runId) state.runStartedAt.delete(runState.runId);
  setContextCompactionState(runState.sessionId, false);
  if (state.session?.id === runState.sessionId) {
    setAgentProcessing(runState.liveMessage || state.liveMessage, false);
    if ($("approvalDialog").open && state.pendingApprovalSessionId === runState.sessionId) {
      $("approvalDialog").close();
    }
  }
  runState.liveMessage = null;
  syncRunStateAliases();
  syncSelectedRunUI();
  renderNavigation();
  if (state.session?.id === runState.sessionId) await loadMessages().catch(() => {});
  await loadSessions().catch(() => {});
  if (hasLaunchablePromptQueue()) void drainPromptQueue();
}

function syncSessionUI() {
  const session = state.session;
  if (!session) return;
  $("sessionTitle").textContent = session.title || "未命名";
  const providerID = session.provider_id || state.workspace?.default_provider_id || "";
  // 只留 Provider：模型名稱右上角的膠囊已經顯示，重複列在這裡只會把標題列撐長，
  // 長模型名（本機模型動輒三四十個字元）會直接壓到搜尋框上。
  $("agentLabel").textContent = providerDisplayName(providerID) || providerID;
  renderSessionUsage();
  syncSessionRuntimeControls();
  syncPlanButton();
  const contextIdentity = activeContextIdentity(session);
  if (state.contextUsage && (state.contextUsage.providerID !== contextIdentity.providerID || state.contextUsage.model !== contextIdentity.model)) {
    state.contextUsage = null;
  }
  renderContextUsage();
  renderContextCompactionState();
  void loadContextCapabilities(session.id);
  syncSelectedRunUI();
  $("sessionSettings").classList.add("hidden");
  $("noSessionManagement").classList.add("hidden");
  $("settingTitle").value = session.title || "";
  renderProjectOptions();
  renderSessionProviderOptions();
  $("settingProject").value = session.project_id || "";
  $("settingProvider").value = session.provider_id || "";
  $("settingModel").value = session.model || "";
  $("settingPermission").value = session.permission_profile || "default";
  $("settingMemoryScope").value = session.metadata?.memory_scope || "";
  $("settingPinned").checked = Boolean(session.pinned);
  $("managementSubtitle").textContent = session.title || "Session";
  syncPlanLockControl();
  refreshOpenProviderUsage();
}

const planStatusLabels = {
  queued: "排隊中",
  active: "進行中",
  completed: "已完成",
  canceled: "已取消",
  pending: "待處理",
  in_progress: "執行中",
  verifying: "驗證中",
  blocked: "受阻",
  skipped: "已略過",
};

const terminalPlanStatuses = new Set(["completed", "canceled"]);

function sessionRunIsActive(sessionID = state.session?.id) {
  return Boolean(sessionID && state.activeRuns.has(sessionID));
}

function activeRunFor(sessionID = state.session?.id) {
  return sessionID ? state.activeRuns.get(sessionID) || null : null;
}

function hasActiveRuns() {
  return state.activeRuns.size > 0;
}

function newRunState(sessionID, run = {}) {
  const runID = String(run.id || "");
  return {
    sessionId: sessionID,
    runId: runID,
    status: run.status || "queued",
    activityText: run.status === "paused" ? "Run 已暫停" : "",
    startedAtMilliseconds: Date.now(),
    canceling: false,
    terminal: false,
    terminalHandled: false,
    eventController: new AbortController(),
    pendingApproval: run.pending_approval || null,
    runDraft: {
      sessionId: sessionID,
      operationId: runID,
      messageId: "",
      content: "",
      reasoning: "",
      internal: false,
      processing: true,
    },
    liveMessage: null,
  };
}

function syncRunStateAliases() {
  const selected = activeRunFor();
  const retryable = state.session?.id ? state.retryableRuns.get(state.session.id) : null;
  state.running = hasActiveRuns();
  state.runningSessionId = selected?.sessionId || "";
  state.trackedRunId = selected?.runId || "";
  state.currentRunId = selected?.runId || "";
  state.currentRunStatus = selected?.status || "";
  state.runActivityText = selected?.activityText || "";
  state.canceling = Boolean(selected?.canceling);
  state.pendingApproval = selected?.pendingApproval || null;
  state.pendingApprovalSessionId = selected?.pendingApproval ? selected.sessionId : "";
  state.liveMessage = selected?.liveMessage || null;
  state.runDraft = selected?.runDraft || null;
  state.retryableRunId = selected ? "" : (retryable?.id || "");
  state.retryableSessionId = selected ? "" : (retryable ? state.session.id : "");
}

function addActiveRun(runState) {
  if (!runState?.sessionId) return null;
  state.lastRunDuration.delete(runState.sessionId);
  state.activeRuns.set(runState.sessionId, runState);
  syncRunStateAliases();
  return runState;
}

function removeActiveRun(runState) {
  if (!runState || state.activeRuns.get(runState.sessionId) !== runState) return false;
  state.activeRuns.delete(runState.sessionId);
  syncRunStateAliases();
  return true;
}

async function loadPlans(sessionID = state.session?.id) {
  if (!sessionID) return;
  const values = await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/plans`);
  if (state.session?.id !== sessionID) return;
  state.plans = Array.isArray(values) ? values : [];
  const active = state.plans.find((plan) => plan.status === "active");
  if (!state.planExpansionInitialized) {
    state.expandedPlanIDs.clear();
    state.planExpansionInitialized = true;
  }
  state.activePlanID = active?.id || "";
  const validIDs = new Set(state.plans.map((plan) => plan.id));
  for (const planID of state.expandedPlanIDs) {
    if (!validIDs.has(planID)) state.expandedPlanIDs.delete(planID);
  }
  syncPlanButton();
  if ($("planDialog").open && $("planForm").classList.contains("hidden")) renderPlanDialog();
}

function syncPlanButton() {
  const button = $("openPlan");
  const plans = state.plans;
  button.classList.toggle("hidden", !state.session);
  button.parentElement.classList.toggle("hidden", !state.session);
  const active = plans.find((plan) => plan.status === "active");
  const completed = plans.filter((plan) => ["completed", "canceled"].includes(plan.status)).length;
  button.dataset.status = active ? "active" : plans.length ? "completed" : "empty";
  button.classList.toggle("is-processing", Boolean(active && sessionRunIsActive()));
  button.setAttribute("aria-busy", String(Boolean(active && sessionRunIsActive())));
  $("planProgress").textContent = plans.length ? `${completed}/${plans.length}` : "建立";
}

function renderPlanDialog() {
  state.planEditingID = "";
  $("planForm").classList.add("hidden");
  $("planView").classList.remove("hidden");
  const plans = state.plans;
  syncPlanLockControl();
  const active = plans.find((plan) => plan.status === "active");
  const ongoingPlans = plans.filter((plan) => !terminalPlanStatuses.has(plan.status));
  const completedPlans = plans.filter((plan) => terminalPlanStatuses.has(plan.status));
  const showingCompleted = state.planTab === "completed";
  const visiblePlans = showingCompleted ? completedPlans : ongoingPlans;
  const activeTab = $("activePlanTab");
  const completedTab = $("completedPlanTab");
  activeTab.classList.toggle("is-selected", !showingCompleted);
  activeTab.setAttribute("aria-selected", String(!showingCompleted));
  activeTab.tabIndex = showingCompleted ? -1 : 0;
  completedTab.classList.toggle("is-selected", showingCompleted);
  completedTab.setAttribute("aria-selected", String(showingCompleted));
  completedTab.tabIndex = showingCompleted ? 0 : -1;
  $("planListPanel").setAttribute("aria-labelledby", showingCompleted ? "completedPlanTab" : "activePlanTab");
  $("activePlanCount").textContent = String(ongoingPlans.length);
  $("completedPlanCount").textContent = String(completedPlans.length);
  $("planEmpty").classList.toggle("hidden", visiblePlans.length > 0);
  $("planList").classList.toggle("hidden", visiblePlans.length === 0);
  $("createPlan").classList.toggle("hidden", plans.length === 0);
  $("clearCompletedPlans").classList.toggle("hidden", !showingCompleted || completedPlans.length === 0);
  $("clearCompletedPlans").disabled = sessionRunIsActive();
  $("planListHint").textContent = showingCompleted
    ? "已完成與已取消的計畫"
    : state.session?.lock_plans
      ? "已鎖定：未完成計畫會依序執行"
      : "未鎖定：可依需求選擇未完成計畫";
  if (plans.length === 0) {
    $("planEmptyTitle").textContent = "尚未建立計畫";
    $("planEmptyDescription").textContent = "你可以先拆解長任務；Agent 遇到多步驟工作時也能自行建立計畫。";
  } else if (showingCompleted) {
    $("planEmptyTitle").textContent = "尚無已完成計畫";
    $("planEmptyDescription").textContent = "完成或取消的計畫會集中顯示在這裡。";
  } else {
    $("planEmptyTitle").textContent = "目前沒有進行中的計畫";
    $("planEmptyDescription").textContent = "可以新增計畫，或到「已完成」查看歷史計畫。";
  }
  $("createPlanFromEmpty").classList.toggle("hidden", showingCompleted && plans.length > 0);
  $("planDialogMeta").textContent = plans.length
    ? `${ongoingPlans.length} 份進行中 · ${completedPlans.length} 份已完成${active ? ` · 目前執行「${active.title}」` : ""}`
    : "依序執行並驗證每一個步驟";
  const list = $("planList");
  list.replaceChildren();
  visiblePlans.forEach((plan, index) => list.append(renderPlanCard(plan, index, visiblePlans.length)));
}

function syncPlanLockControl() {
  const control = $("lockPlansSwitch");
  if (!control) return;
  const disabled = !state.session || selectedSessionIsRunning() || state.sessionRuntimeSaving;
  control.checked = Boolean(state.session?.lock_plans);
  control.disabled = disabled;
  control.setAttribute("aria-checked", String(control.checked));
  control.closest(".switch-label")?.classList.toggle("switch-disabled", disabled);
}

async function changePlanLock(event) {
  const sessionID = state.session?.id;
  if (!sessionID || selectedSessionIsRunning()) return;
  const value = Boolean(event.target.checked);
  const updated = await updateSessionRuntime(sessionID, { lock_plans: value });
  if (!updated) return;
  await loadPlans(sessionID).catch(() => {});
  if ($("planDialog").open && $("planForm").classList.contains("hidden")) renderPlanDialog();
}

function renderPlanCard(plan, index, visiblePlanCount) {
  const locked = sessionRunIsActive();
  const terminal = terminalPlanStatuses.has(plan.status);
  const sortable = !terminal && state.planTab === "active";
  const expanded = state.expandedPlanIDs.has(plan.id);
  const steps = Array.isArray(plan.steps) ? plan.steps : [];
  const executedStepCount = steps.filter((step) => ["completed", "skipped"].includes(step.status)).length;
  const card = document.createElement("article");
  card.className = "plan-card";
  card.dataset.planId = plan.id;
  card.dataset.status = plan.status;

  const header = document.createElement("header");
  header.className = "plan-card-header";
  const handle = document.createElement("span");
  handle.className = "plan-drag-handle";
  handle.title = sortable ? (locked ? "執行期間不能調整順序" : "拖曳調整執行順序") : "已完成計畫不參與執行排序";
  handle.setAttribute("aria-hidden", "true");
  handle.textContent = sortable ? "⠿" : "•";
  handle.classList.toggle("is-disabled", !sortable || locked || visiblePlanCount <= 1);
  if (sortable && !locked && visiblePlanCount > 1) {
    handle.addEventListener("pointerdown", (event) => startPlanDrag(event, card, plan.id, "pointer"));
    handle.addEventListener("mousedown", (event) => startPlanDrag(event, card, plan.id, "mouse"));
  }
  const order = document.createElement("span");
  order.className = "plan-order";
  order.textContent = String(index + 1);
  const summary = document.createElement("div");
  summary.className = "plan-card-summary";
  const title = document.createElement("h3");
  title.textContent = plan.title;
  const objective = document.createElement("p");
  objective.textContent = plan.objective || "未另外說明整體目標";
  summary.append(title, objective);
  const progress = document.createElement("span");
  progress.className = "plan-step-progress";
  progress.textContent = `${executedStepCount}/${steps.length}`;
  progress.title = `已執行 ${executedStepCount}/${steps.length} 個步驟`;
  progress.setAttribute("aria-label", progress.title);
  const status = document.createElement("span");
  status.className = "plan-status";
  status.textContent = planStatusLabels[plan.status] || plan.status;
  const quickActions = document.createElement("div");
  quickActions.className = "plan-card-quick-actions";
  if (terminal) {
    const quickRemove = document.createElement("button");
    quickRemove.type = "button";
    quickRemove.className = "plan-delete-quick danger ghost";
    quickRemove.textContent = "刪除";
    quickRemove.disabled = locked;
    quickRemove.addEventListener("click", () => deletePlan(plan.id));
    quickActions.append(quickRemove);
  }
  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "plan-collapse icon-button";
  toggle.title = expanded ? "收合計畫步驟" : "展開計畫步驟";
  toggle.setAttribute("aria-label", toggle.title);
  toggle.setAttribute("aria-expanded", String(expanded));
  toggle.textContent = "⌄";
  toggle.addEventListener("click", () => {
    if (state.expandedPlanIDs.has(plan.id)) state.expandedPlanIDs.delete(plan.id);
    else state.expandedPlanIDs.add(plan.id);
    renderPlanDialog();
  });
  header.append(handle, order, summary, progress, status, quickActions, toggle);
  card.append(header);

  const details = document.createElement("div");
  details.className = "plan-card-details";
  details.classList.toggle("hidden", !expanded);
  const stepList = document.createElement("ol");
  stepList.className = "plan-step-list";
  for (const step of steps) stepList.append(renderPlanStep(step));
  const actions = document.createElement("div");
  actions.className = "plan-card-actions";
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "small danger ghost";
  remove.textContent = "刪除";
  remove.disabled = locked;
  remove.addEventListener("click", () => deletePlan(plan.id));
  const rebuild = document.createElement("button");
  rebuild.type = "button";
  rebuild.className = "small ghost";
  rebuild.textContent = "重建";
  rebuild.disabled = locked;
  rebuild.addEventListener("click", () => editPlan(plan));
  if (!terminal) actions.append(remove);
  actions.append(rebuild);
  details.append(stepList, actions);
  card.append(details);
  return card;
}

function renderPlanStep(step) {
  const item = document.createElement("li");
  item.className = "plan-step-item";
  item.dataset.status = step.status;
  const index = document.createElement("span");
  index.className = "plan-step-index";
  const copy = document.createElement("div");
  copy.className = "plan-step-copy";
  const title = document.createElement("strong");
  title.textContent = step.title;
  copy.append(title);
  if (step.description) {
    const description = document.createElement("small");
    description.textContent = step.description;
    copy.append(description);
  }
  const verification = document.createElement("small");
  verification.textContent = `驗證：${step.verification}`;
  copy.append(verification);
  if (step.evidence) {
    const evidence = document.createElement("p");
    evidence.className = "plan-step-evidence";
    evidence.textContent = `${step.status === "blocked" ? "原因" : "證據"}：${step.evidence}`;
    copy.append(evidence);
  }
  const status = document.createElement("span");
  status.className = "plan-step-state";
  status.textContent = planStatusLabels[step.status] || step.status;
  item.append(index, copy, status);
  return item;
}

function openPlanDialog() {
  if (!state.session) return;
  renderPlanDialog();
  $("planDialog").showModal();
}

function selectPlanTab(tab, focus = false) {
  if (tab !== "active" && tab !== "completed") return;
  if (state.planDragID) return;
  state.planTab = tab;
  renderPlanDialog();
  if (focus) $(tab === "completed" ? "completedPlanTab" : "activePlanTab").focus();
}

function editPlan(plan = null) {
  if (!state.session || sessionRunIsActive()) {
    toast("這個對話正在執行，完成後才能修改計畫。");
    return;
  }
  state.planEditingID = plan?.id || "";
  $("planView").classList.add("hidden");
  $("planForm").classList.remove("hidden");
  $("newPlanTitle").value = plan?.title || "";
  $("newPlanObjective").value = plan?.objective || "";
  $("planStepEditor").replaceChildren();
  const steps = plan?.steps?.length ? plan.steps : [{ title: "", description: "", verification: "" }];
  for (const step of steps) addPlanStepEditorRow(step);
  $("planDialogMeta").textContent = plan ? "儲存後會重設這份計畫的步驟進度" : "新增到計畫列表尾端，並依順序執行";
  $("newPlanTitle").focus();
}

function addPlanStepEditorRow(step = {}) {
  const editor = $("planStepEditor");
  if (editor.children.length >= 50) {
    toast("一份計畫最多 50 個步驟。");
    return;
  }
  const row = document.createElement("div");
  row.className = "plan-step-edit-row";
  const number = document.createElement("span");
  number.className = "plan-step-edit-number";
  const fields = document.createElement("div");
  fields.className = "plan-step-edit-fields";
  const title = document.createElement("input");
  title.dataset.field = "title";
  title.placeholder = "步驟名稱";
  title.required = true;
  title.maxLength = 200;
  title.value = step.title || "";
  const description = document.createElement("input");
  description.dataset.field = "description";
  description.placeholder = "執行說明（選填）";
  description.value = step.description || "";
  const verification = document.createElement("input");
  verification.dataset.field = "verification";
  verification.placeholder = "驗證條件，例如：go test ./... 通過";
  verification.required = true;
  verification.value = step.verification || "";
  fields.append(title, description, verification);
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "plan-step-remove";
  remove.title = "移除步驟";
  remove.setAttribute("aria-label", "移除步驟");
  remove.textContent = "×";
  remove.addEventListener("click", () => {
    if (editor.children.length <= 1) {
      toast("計畫至少需要一個步驟。");
      return;
    }
    row.remove();
    refreshPlanStepEditorNumbers();
  });
  row.append(number, fields, remove);
  editor.append(row);
  refreshPlanStepEditorNumbers();
}

function refreshPlanStepEditorNumbers() {
  [...$("planStepEditor").children].forEach((row, index) => {
    row.querySelector(".plan-step-edit-number").textContent = String(index + 1);
  });
}

function cancelPlanEdit() { renderPlanDialog(); }

async function savePlan(event) {
  event.preventDefault();
  if (!state.session || sessionRunIsActive()) return;
  const rows = [...$("planStepEditor").children];
  const steps = rows.map((row) => ({
    title: row.querySelector('[data-field="title"]').value.trim(),
    description: row.querySelector('[data-field="description"]').value.trim(),
    verification: row.querySelector('[data-field="verification"]').value.trim(),
  }));
  if (!steps.length || steps.some((step) => !step.title || !step.verification)) {
    toast("每個步驟都要填寫名稱與驗證條件。");
    return;
  }
  if (state.planEditingID && !(await confirmAction("重建計畫會清除這份計畫目前的步驟進度，確定繼續嗎？"))) return;
  const sessionID = state.session.id;
  const editingPlanID = state.planEditingID;
  $("savePlan").disabled = true;
  try {
    const path = editingPlanID
      ? `/api/v1/sessions/${encodeURIComponent(sessionID)}/plans/${encodeURIComponent(editingPlanID)}`
      : `/api/v1/sessions/${encodeURIComponent(sessionID)}/plans`;
    await request(path, {
      method: editingPlanID ? "PUT" : "POST",
      body: JSON.stringify({ title: $("newPlanTitle").value.trim(), objective: $("newPlanObjective").value.trim(), steps }),
    });
    if (state.session?.id !== sessionID) return;
    state.planEditingID = "";
    state.planTab = "active";
    await loadPlans(sessionID);
    renderPlanDialog();
  } catch (error) {
    toast(error.message);
  } finally {
    $("savePlan").disabled = false;
  }
}

async function deletePlan(planID) {
  if (!state.session || !planID || sessionRunIsActive()) return;
  if (!(await confirmAction("確定刪除這份計畫嗎？"))) return;
  const sessionID = state.session.id;
  try {
    await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/plans/${encodeURIComponent(planID)}`, { method: "DELETE" });
    if (state.session?.id !== sessionID) return;
    state.expandedPlanIDs.delete(planID);
    await loadPlans(sessionID);
    renderPlanDialog();
  } catch (error) {
    toast(error.message);
  }
}

async function clearCompletedPlans() {
  if (!state.session || sessionRunIsActive()) return;
  const terminalPlans = state.plans.filter((plan) => terminalPlanStatuses.has(plan.status));
  if (!terminalPlans.length) return;
  if (!(await confirmAction(translate("確定清除所有已完成與已取消的計畫嗎？")))) return;
  const sessionID = state.session.id;
  const button = $("clearCompletedPlans");
  button.disabled = true;
  try {
    for (const plan of terminalPlans) {
      await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/plans/${encodeURIComponent(plan.id)}`, { method: "DELETE" });
      state.expandedPlanIDs.delete(plan.id);
    }
    if (state.session?.id !== sessionID) return;
    await loadPlans(sessionID);
    renderPlanDialog();
    toast(translate("已清除完成的計畫"));
  } catch (error) {
    if (state.session?.id === sessionID) {
      await loadPlans(sessionID).catch(() => {});
      renderPlanDialog();
    }
    toast(error.message);
  } finally {
    button.disabled = false;
  }
}

function handlePlanDragOver(event) {
  if (state.planTab !== "active" || !state.planDragID || sessionRunIsActive()) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "move";
  const target = event.target.closest(".plan-card");
  const dragging = $("planList").querySelector(`[data-plan-id="${CSS.escape(state.planDragID)}"]`);
  if (!target || !dragging || target === dragging) return;
  const rect = target.getBoundingClientRect();
  const before = event.clientY < rect.top + rect.height / 2;
  $("planList").insertBefore(dragging, before ? target : target.nextSibling);
}

function startPlanDrag(event, card, planID, inputKind) {
  if (state.planTab !== "active" || event.button !== 0 || state.planDragID || sessionRunIsActive()) return;
  event.preventDefault();
  const handle = event.currentTarget;
  state.planDragID = planID;
  card.classList.add("dragging");
  if (inputKind === "pointer") handle.setPointerCapture?.(event.pointerId);
  const move = (moveEvent) => {
    const target = document.elementFromPoint(moveEvent.clientX, moveEvent.clientY)?.closest(".plan-card");
    if (!target || target === card || !$("planList").contains(target)) return;
    const rect = target.getBoundingClientRect();
    const before = moveEvent.clientY < rect.top + rect.height / 2;
    $("planList").insertBefore(card, before ? target : target.nextSibling);
  };
  const cleanup = () => {
    const moveEvent = inputKind === "pointer" ? "pointermove" : "mousemove";
    const finishEvent = inputKind === "pointer" ? "pointerup" : "mouseup";
    document.removeEventListener(moveEvent, move, true);
    document.removeEventListener(finishEvent, finish, true);
    if (inputKind === "pointer") {
      document.removeEventListener("pointercancel", cancel, true);
      if (handle.hasPointerCapture?.(event.pointerId)) handle.releasePointerCapture(event.pointerId);
    }
    window.removeEventListener("blur", cancel);
  };
  const finish = () => {
    cleanup();
    persistPlanOrderFromDOM();
  };
  const cancel = () => {
    cleanup();
    state.planDragID = "";
    renderPlanDialog();
  };
  document.addEventListener(inputKind === "pointer" ? "pointermove" : "mousemove", move, true);
  document.addEventListener(inputKind === "pointer" ? "pointerup" : "mouseup", finish, true);
  if (inputKind === "pointer") document.addEventListener("pointercancel", cancel, true);
  window.addEventListener("blur", cancel);
}

async function persistPlanOrder(event) {
  event.preventDefault();
  await persistPlanOrderFromDOM();
}

async function persistPlanOrderFromDOM() {
  if (!state.session || state.planTab !== "active" || !state.planDragID || sessionRunIsActive()) return;
  const sessionID = state.session.id;
  const ongoingPlanIDs = [...$("planList").querySelectorAll(".plan-card")].map((card) => card.dataset.planId);
  const previousOngoingPlanIDs = state.plans.filter((plan) => !terminalPlanStatuses.has(plan.status)).map((plan) => plan.id);
  state.planDragID = "";
  if (ongoingPlanIDs.every((planID, index) => planID === previousOngoingPlanIDs[index])) {
    renderPlanDialog();
    return;
  }
  let ongoingIndex = 0;
  const planIDs = state.plans.map((plan) => terminalPlanStatuses.has(plan.status) ? plan.id : ongoingPlanIDs[ongoingIndex++]);
  try {
    const values = await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/plans/order`, {
      method: "PUT",
      body: JSON.stringify({ plan_ids: planIDs }),
    });
    if (state.session?.id !== sessionID) return;
    state.plans = values;
    syncPlanButton();
    renderPlanDialog();
  } catch (error) {
    renderPlanDialog();
    toast(error.message);
  }
}

async function loadMessages() {
  if (!state.session) return;
  const selectedID = state.session.id;
  const entries = [];
  let afterSequence = 0;
  while (true) {
    const page = await request(`/api/v1/sessions/${encodeURIComponent(selectedID)}/entries?after_sequence=${afterSequence}&limit=1000`);
    entries.push(...(page.items || []));
    const nextSequence = Number(page.next_after_sequence || afterSequence);
    if (!page.has_more || nextSequence <= afterSequence) break;
    afterSequence = nextSequence;
  }
  if (state.session?.id !== selectedID) return;
  const container = $("messages");
  container.replaceChildren();
  state.contextUsage = null;
  let visibleEntries = 0;
  let activeOperationID = "";
  const operationStartedAt = new Map();
  for (const entry of entries) {
    if (entry.type === "operation_started") {
      activeOperationID = String(entry.data?.operation_id || entry.data?.run_id || "");
      operationStartedAt.set(activeOperationID, Date.parse(entry.created_at));
      continue;
    }
    if (entry.type === "message" && entry.message) {
      if (entry.message.role === "assistant" && entry.message.usage) {
        const identity = activeContextIdentity();
        recordContextUsage(
          entry.message.usage,
          selectedID,
          entry.message.provider_id || identity.providerID,
          entry.message.model || identity.model,
        );
      }
      if (appendMessage(entry.message, { operationId: activeOperationID })) visibleEntries += 1;
      continue;
    }
    if (entry.type === "messages_retracted") {
      // 撤回：這一段不再屬於對話，連同該問題的回答、處理過程與失敗提示一起移除。
      const from = String(entry.data?.from_message_id || "").trim();
      if (from) visibleEntries -= removeMessagesFrom(container, from);
      continue;
    }
    if (entry.type === "operation_finished") {
      const operationID = String(entry.data?.operation_id || entry.data?.run_id || activeOperationID || "");
      const recordedDuration = Number(entry.data?.duration_ms);
      const startedAt = operationStartedAt.get(operationID);
      const measuredDuration = Number.isFinite(startedAt) ? Date.parse(entry.created_at) - startedAt : 0;
      finalizeReasoningGroup(operationID, Number.isFinite(recordedDuration) ? recordedDuration : measuredDuration);
      operationStartedAt.delete(operationID);
      if (operationID === activeOperationID) activeOperationID = "";
      if (["failed", "canceled"].includes(entry.data?.status)) {
        if (appendRunOutcome({
          id: operationID,
          status: entry.data.status,
          error: entry.data.error,
        })) visibleEntries += 1;
      }
    }
  }
  $("emptyState").classList.toggle("hidden", visibleEntries > 0);
  container.classList.toggle("hidden", visibleEntries === 0);
  renderContextUsage();
  syncReaskButton();
  scrollMessages({ force: true });
}

// removeMessagesFrom 移除指定訊息與其後的所有節點，回傳移除的數量。
function removeMessagesFrom(container, messageID) {
  const escaped = window.CSS?.escape ? window.CSS.escape(messageID) : messageID.replace(/["\\]/g, "\\$&");
  let node = container.querySelector(`[data-message-id="${escaped}"]`);
  let removed = 0;
  while (node) {
    const next = node.nextElementSibling;
    node.remove();
    removed += 1;
    node = next;
  }
  return removed;
}

// syncReaskButton 只讓最後一則使用者訊息顯示重新提問，而且執行中不顯示：
// 那個位置在執行中屬於停止按鈕，撤回也會被後端以衝突擋下來。
function syncReaskButton() {
  const buttons = [...document.querySelectorAll(".message.user .message-action-reask")];
  const running = selectedSessionIsRunning();
  buttons.forEach((button, index) => {
    button.classList.toggle("hidden", running || index !== buttons.length - 1);
  });
}

// reaskMessage 把這則提問之後的內容移出對話，再用同樣的內容重問一次。
async function reaskMessage(message) {
  const sessionID = state.session?.id;
  const messageID = String(message?.id || "").trim();
  if (!sessionID || !messageID || selectedSessionIsRunning()) return;
  const attachmentIds = (Array.isArray(message.metadata?.attachments) ? message.metadata.attachments : [])
    .map((attachment) => String(attachment?.id || "").trim())
    .filter(Boolean);
  try {
    await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/messages/${encodeURIComponent(messageID)}/retract`, { method: "POST" });
  } catch (error) {
    toast(error.message);
    return;
  }
  await loadMessages().catch(() => {});
  const input = String(message.content || "").trim() || "請檢視附件。";
  await enqueuePrompt(input, { attachmentIds });
}

function splitTaggedThinkingContent(value) {
  const source = String(value || "");
  const matches = [...source.matchAll(/<\/?think\s*>/gi)];
  if (matches.length === 0) return { content: source, reasoning: "", found: false };
  let content = "";
  let reasoning = "";
  let cursor = 0;
  let inThinking = false;
  let sawMarker = false;
  for (const match of matches) {
    const segment = source.slice(cursor, match.index);
    const closing = /^<\//.test(match[0]);
    if (closing && !inThinking && !sawMarker) reasoning += segment;
    else if (inThinking) reasoning += segment;
    else content += segment;
    sawMarker = true;
    inThinking = !closing;
    cursor = match.index + match[0].length;
  }
  if (inThinking) reasoning += source.slice(cursor);
  else content += source.slice(cursor);
  return { content: content.trim(), reasoning: reasoning.trim(), found: true };
}

function normalizeAssistantThinking(message) {
  if (!message || message.role !== "assistant") return message;
  const tagged = splitTaggedThinkingContent(message.content);
  if (!tagged.found) return message;
  const existing = String(message.reasoning || "").trim();
  const reasoning = joinReasoningParts(existing, tagged.reasoning);
  return { ...message, content: tagged.content, reasoning };
}

function reasoningSegmentKey(value) {
  return String(value || "")
    .replace(/\u00a0/gu, " ")
    .replace(/\s+/gu, " ")
    .trim();
}

function reasoningSegments(value) {
  return String(value || "")
    .trim()
    .split(/\n\s*-{3,}\s*(?:\n|$)/u)
    .map((segment) => segment.trim())
    .filter(Boolean);
}

function uniqueReasoningSegments(values) {
  const segments = [];
  const seen = new Set();
  for (const value of values) {
    for (const segment of reasoningSegments(value)) {
      const key = reasoningSegmentKey(segment);
      if (!key || seen.has(key)) continue;
      seen.add(key);
      segments.push(segment);
    }
  }
  return segments;
}

function dedupeRepeatedReasoningBlocks(value) {
  const blocks = String(value || "")
    .trim()
    .split(/\n{2,}/u)
    .map((block) => block.trim())
    .filter(Boolean);
  const result = [];
  for (let index = 0; index < blocks.length;) {
    let repeatedLength = 0;
    const maximumLength = Math.min(result.length, blocks.length - index);
    // Provider 有時會把同一組「標題＋說明」連續送兩次；只比對目前
    // 結尾的連續區塊，避免誤刪不同階段中刻意重複的單句。
    for (let length = maximumLength; length > 0; length -= 1) {
      let matched = true;
      for (let offset = 0; offset < length; offset += 1) {
        if (reasoningSegmentKey(result[result.length - length + offset])
          !== reasoningSegmentKey(blocks[index + offset])) {
          matched = false;
          break;
        }
      }
      if (matched) {
        repeatedLength = length;
        break;
      }
    }
    if (repeatedLength > 0) {
      index += repeatedLength;
      continue;
    }
    result.push(blocks[index]);
    index += 1;
  }
  return result.join("\n\n");
}

function dedupeReasoningContent(value) {
  return uniqueReasoningSegments([dedupeRepeatedReasoningBlocks(value)]).join("\n\n---\n\n");
}

function mergeReasoningText(currentValue, nextValue, separator = "\n\n") {
  const current = dedupeReasoningContent(currentValue);
  const next = dedupeReasoningContent(nextValue);
  if (!current) return next;
  if (!next) return current;
  const currentKey = reasoningSegmentKey(current);
  const nextKey = reasoningSegmentKey(next);
  if (currentKey === nextKey) return current.length >= next.length ? current : next;
  // message.end 通常帶完整 reasoning；若串流草稿已是其中一部分，直接
  // 採用較完整的值，避免把同一輪內容再附加一次。
  if (nextKey.includes(currentKey)) return next;
  if (currentKey.includes(nextKey)) return current;
  return `${current}${separator}${next}`;
}

function appendReasoningDelta(currentValue, deltaValue) {
  const current = String(currentValue || "");
  const delta = String(deltaValue || "");
  if (!current || !delta) return current + delta;
  const currentKey = reasoningSegmentKey(current);
  const deltaKey = reasoningSegmentKey(delta);
  if (!deltaKey) return current;
  // 有些相容 Provider 會同時從 reasoning 欄位與 <think> content 欄位送出
  // 同一片段；也有 Provider 會重送完整累積值。這兩種都不能再次附加。
  if (currentKey === deltaKey) return current;
  if (deltaKey.startsWith(currentKey)) return delta;
  // 只對具有足夠內容或 Markdown 結構的重疊片段去重，避免模型正常連續
  // 輸出單一中文字元或短詞時，被誤認為是重送。
  if ((deltaKey.length >= 8 || /[\n*#]/u.test(deltaKey)) && currentKey.endsWith(deltaKey)) return current;
  return current + delta;
}

function joinReasoningParts(...values) {
  return values.reduce((current, value) => mergeReasoningText(current, value), "");
}

function appendMessage(message, options = {}) {
  if (!message) return null;
  message = normalizeAssistantThinking(message);
  const hasToolCalls = Array.isArray(message.tool_calls) && message.tool_calls.length > 0;
  if (!message.content && !message.reasoning && !hasToolCalls && !options.placeholder) return null;
  // tool 與內部階段訊息是 Harness transcript，不是對使用者顯示的聊天內容。
  // 它們仍保留在後端稽核與下一輪 LLM context 中。
  if (message.role === "tool") return null;
  const internalAssistant = message.role === "assistant"
    && (message.metadata?.internal === true || (Array.isArray(message.tool_calls) && message.tool_calls.length > 0));
  if (internalAssistant) {
    const intermediateReasoning = joinReasoningParts(message.reasoning, message.content);
    if (!intermediateReasoning) return null;
    message = { ...message, content: "", reasoning: intermediateReasoning };
  }
  const article = document.createElement("article");
  article.className = `message ${message.role}`;
  if (message.variant) article.classList.add(message.variant);
  article.dataset.messageId = message.id || "";
  if (options.operationId) article.dataset.operationId = options.operationId;
  const content = document.createElement("div");
  content.className = "content";
  renderRichContent(content, message.content || "");
  if (message.role === "user") {
    const bubble = document.createElement("div");
    bubble.className = "message-bubble";
    bubble.append(content);
    appendMessageAttachments(bubble, message.metadata?.attachments);
    const actions = document.createElement("div");
    actions.className = "message-actions";
    const copy = iconButton(messageCopyIcon(), translate("複製訊息"), () => copyMessage(message.content));
    copy.classList.add("message-action-button");
    actions.append(copy);
    // 重新提問只出現在最後一則提問上；由 syncReaskButton 決定顯示哪一個。
    const reask = iconButton(messageReaskIcon(), translate("重新提問"), () => void reaskMessage(message));
    reask.classList.add("message-action-button", "message-action-reask", "hidden");
    actions.append(reask);
    article.append(bubble, actions);
  } else if (message.role === "assistant") {
    const reasoning = createReasoningBlock(message.reasoning || "");
    if (internalAssistant) {
      article.classList.add("reasoning-only");
      renderRichContent(content, "");
    }
    article.append(reasoning, content);
    const indicator = document.createElement("div");
    indicator.className = "agent-processing";
    indicator.setAttribute("role", "status");
    indicator.setAttribute("aria-label", "Agent 正在處理");
    indicator.setAttribute("aria-hidden", "true");
    for (let index = 0; index < 3; index += 1) {
      const dot = document.createElement("span");
      dot.setAttribute("aria-hidden", "true");
      indicator.append(dot);
    }
    article.append(indicator);
  } else {
    article.append(content);
  }
  $("messages").append(article);
  if (message.role === "assistant" && message.reasoning) {
    return mergeReasoningIntoPrevious(article, options.operationId || "");
  }
  return article;
}

function appendMessageAttachments(container, attachments) {
  if (!Array.isArray(attachments) || attachments.length === 0) return;
  const list = document.createElement("div");
  list.className = "message-attachments";
  for (const attachment of attachments) {
    const target = String(attachment?.path || "").trim();
    const item = document.createElement(target ? "button" : "span");
    if (target) item.type = "button";
    item.className = "message-attachment";
    if (target) {
      item.classList.add("resource-link");
      item.dataset.resourceKind = "path";
      item.dataset.resourceTarget = target;
      item.title = target;
    }
    const icon = document.createElement("span");
    icon.className = "pending-attachment-icon";
    icon.textContent = attachmentIcon(attachment?.media_type);
    const copy = document.createElement("span");
    copy.className = "attachment-copy";
    const name = document.createElement("strong");
    name.textContent = attachment?.name || "附件";
    const detail = document.createElement("small");
    detail.textContent = formatBytes(Number(attachment?.size || 0));
    copy.append(name, detail);
    item.append(icon, copy);
    list.append(item);
  }
  container.append(list);
}

function createReasoningBlock(value, className = "message-reasoning") {
  const source = dedupeReasoningContent(value);
  const block = document.createElement("details");
  block.className = className;
  block.dataset.segmentCount = String(reasoningSegments(source).length);
  const summary = document.createElement("summary");
  summary.className = "reasoning-summary";
  const summaryLabel = document.createElement("span");
  summaryLabel.className = "reasoning-summary-label";
  summary.append(summaryLabel);
  summary.addEventListener("click", () => {
    // 只記錄使用者主動操作；自動揭露最新段落不能把較早段落的狀態重設。
    block.dataset.userOpen = String(!block.open);
  });
  const content = document.createElement("div");
  content.className = "reasoning-content";
  renderRichContent(content, normalizeReasoningMarkdown(source));
  const footerToggle = document.createElement("button");
  footerToggle.type = "button";
  footerToggle.className = "reasoning-footer-toggle";
  footerToggle.innerHTML = '<span class="reasoning-footer-label"></span><span class="reasoning-footer-icon" aria-hidden="true"></span>';
  footerToggle.addEventListener("click", (event) => {
    event.preventDefault();
    event.stopPropagation();
    block.dataset.userOpen = "false";
    block.open = false;
  });
  block.append(summary, content, footerToggle);
  block.addEventListener("toggle", () => updateReasoningSummary(block));
  block.classList.toggle("hidden", !source);
  updateReasoningSummary(block);
  return block;
}

function setMessageReasoning(messageNode, value, append = false) {
  if (!messageNode || !value) return;
  let block = messageNode.querySelector(".message-reasoning");
  if (!block) {
    block = createReasoningBlock("");
    messageNode.prepend(block);
  }
  const content = block.querySelector(".reasoning-content");
  const existing = richContentSource(content).trim();
  const hasExistingSegment = Boolean(existing);
  const source = normalizeReasoningMarkdown(append && hasExistingSegment
    ? mergeReasoningText(existing, value, "\n\n---\n\n")
    : dedupeReasoningContent(value));
  if (append) scheduleRichContent(content, source);
  else renderRichContent(content, source);
  block.dataset.segmentCount = String(reasoningSegments(source).length);
  block.classList.remove("hidden");
  updateReasoningSummary(block);
  updateLiveReasoningDuration(messageNode.dataset.operationId || "");
}

function normalizeReasoningMarkdown(value) {
  // Provider 常把多個獨立的 **粗體階段** 無分隔串接成 ****，這不是有效的
  // Markdown 邊界。只在過程說明層補上段落分隔，避免改動正式回答內容。
  return (value || "").replace(/\*{4}/g, "**\n\n**");
}

function mergeReasoningIntoPrevious(messageNode, operationID = "") {
  if (!messageNode) return messageNode;
  const block = messageNode.querySelector(":scope > .message-reasoning");
  const content = block?.querySelector(".reasoning-content");
  const previous = messageNode.previousElementSibling;
  if (!block || !content || !previous?.classList.contains("reasoning-only")) return messageNode;
  const previousOperationID = previous.dataset.operationId || "";
  if (operationID && previousOperationID && operationID !== previousOperationID) return messageNode;
  if (operationID && !previousOperationID) previous.dataset.operationId = operationID;
  const addition = richContentSource(content).trim();
  if (addition) setMessageReasoning(previous, addition, true);
  block.remove();
  if (messageNode.classList.contains("reasoning-only")) {
    messageNode.remove();
    return previous;
  }
  return messageNode;
}

function finalizeReasoningGroup(operationID, durationMilliseconds) {
  if (!operationID || !Number.isFinite(durationMilliseconds) || durationMilliseconds < 0) return;
  for (const node of $("messages").querySelectorAll(".message.assistant[data-operation-id]")) {
    if (node.dataset.operationId !== operationID) continue;
    const block = node.querySelector(".message-reasoning");
    if (block) {
      block.dataset.durationMilliseconds = String(Math.round(durationMilliseconds));
      updateReasoningSummary(block, durationMilliseconds);
    }
  }
}

function updateLiveReasoningDuration(operationID, now = Date.now()) {
  const startedAt = state.runStartedAt.get(operationID);
  if (!operationID || !Number.isFinite(startedAt) || !Number.isFinite(now) || now < startedAt) return;
  for (const node of $("messages").querySelectorAll(".message.assistant[data-operation-id]")) {
    if (node.dataset.operationId !== operationID) continue;
    const block = node.querySelector(".message-reasoning:not(.hidden)");
    if (block) updateReasoningSummary(block, now - startedAt);
  }
}

function updateReasoningSummary(block, durationMilliseconds = null) {
  const summary = block?.querySelector(".reasoning-summary");
  if (!summary) return;
  const segmentCount = Math.max(0, Number(block.dataset.segmentCount) || 0);
  let label = translate("處理過程");
  if (segmentCount > 0) label += ` · ${segmentCount}段`;
  const storedDuration = Number(block.dataset.durationMilliseconds);
  const duration = Number.isFinite(durationMilliseconds) && durationMilliseconds >= 0
    ? durationMilliseconds
    : storedDuration;
  if (Number.isFinite(duration) && duration >= 0) label += ` · ${formatProcessingDuration(duration)}`;
  const summaryLabel = summary.querySelector(".reasoning-summary-label") || summary;
  summaryLabel.textContent = label;
  const actionLabel = translate(block.open ? "收合處理過程" : "展開處理過程");
  summary.title = actionLabel;
  summary.setAttribute("aria-label", actionLabel);
  const footerToggle = block.querySelector(".reasoning-footer-toggle");
  if (footerToggle) {
    const footerLabel = footerToggle.querySelector(".reasoning-footer-label");
    if (footerLabel) footerLabel.textContent = translate("收合處理過程");
    footerToggle.title = translate("收合處理過程");
    footerToggle.setAttribute("aria-label", translate("收合處理過程"));
  }
}

function updateLiveReasoningDurations() {
  updateWaitingForModelActivity();
  if (state.runStartedAt.size === 0) return;
  const now = Date.now();
  for (const operationID of state.runStartedAt.keys()) updateLiveReasoningDuration(operationID, now);
}

// updateWaitingForModelActivity 在模型還沒吐出任何文字或工具指令時交代「還在等模型」。
//
// 這段期間畫面上只有三個跳動的點：模型想很久跟真的卡死看起來一模一樣，使用者只能自己
// 猜要不要重來。狀態列改成帶秒數的等待說明，其他更具體的狀態（重連、等待 MCP、等待核准）
// 一旦出現就不覆蓋。
function updateWaitingForModelActivity() {
  const sessionID = state.session?.id;
  const runState = activeRunFor(sessionID);
  if (!runState) return;
  const draft = runState.runDraft;
  const started = state.runStartedAt.get(draft?.operationId || runState.runId || "");
  const current = runState.activityText || "";
  if (current && !current.startsWith(waitingForModelPrefix)) return;
  if (!started || (draft && (draft.content || draft.reasoning))) {
    if (current.startsWith(waitingForModelPrefix)) setRunActivity("", sessionID);
    return;
  }
  const elapsed = Date.now() - started;
  if (elapsed < waitingForModelDelayMilliseconds) return;
  setRunActivity(`${waitingForModelPrefix}（${formatProcessingDuration(elapsed)}）`, sessionID);
}

function formatProcessingDuration(durationMilliseconds) {
  const totalSeconds = Math.max(1, Math.round(durationMilliseconds / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  // 單位跟著介面語言走：這個字串會出現在完整句子裡（「本次回應共使用 …」），
  // 英文句子配中文單位讀起來就是壞的。日文沿用 分／秒，沒有對照就是原文。
  return `${minutes}${translate("分")}${String(seconds).padStart(2, "0")}${translate("秒")}`;
}

function appendRunOutcome(outcome) {
  const outcomeID = `run-outcome-${outcome.id || "unknown"}`;
  if (findMessage(outcomeID)) return null;
  const detail = typeof outcome.error === "string" ? outcome.error : outcome.error?.message;
  const canceled = outcome.status === "canceled";
  return appendMessage({
    id: outcomeID,
    role: "assistant",
    variant: canceled ? "run-canceled" : "run-failed",
    content: canceled
      ? (detail || "此 Run 已取消，未產生回答。")
      : `此 Run 未產生回答：${detail || "執行失敗"}`,
  });
}

function messageCopyIcon() {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.classList.add("message-action-icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(namespace, "path");
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.8");
  path.setAttribute("stroke-linecap", "round");
  path.setAttribute("stroke-linejoin", "round");
  path.setAttribute("d", "M9 8h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1Zm-5 8H3V4a1 1 0 0 1 1-1h12v1");
  svg.append(path);
  return svg;
}

// messageReaskIcon 是「重新提問」的循環箭頭。
function messageReaskIcon() {
  const namespace = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.classList.add("message-action-icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(namespace, "path");
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "1.8");
  path.setAttribute("stroke-linecap", "round");
  path.setAttribute("stroke-linejoin", "round");
  path.setAttribute("d", "M20 12a8 8 0 1 1-2.34-5.66M20 4v4h-4");
  svg.append(path);
  return svg;
}

async function copyText(value, successMessage) {
  try {
    if (!navigator.clipboard?.writeText) throw new Error("Clipboard API unavailable");
    await navigator.clipboard.writeText(value);
    toast(successMessage);
  } catch (_) {
    const textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.setAttribute("readonly", "");
    textarea.className = "clipboard-fallback";
    document.body.append(textarea);
    textarea.select();
    const copied = document.execCommand("copy");
    textarea.remove();
    toast(copied ? successMessage : "無法存取剪貼簿");
  }
}

async function copyMessage(content) {
  await copyText(content || "", "已複製訊息");
}

function setAgentProcessing(messageElement, active) {
  if (!messageElement?.classList.contains("assistant")) return;
  const indicator = messageElement.querySelector(".agent-processing");
  messageElement.classList.toggle("processing", Boolean(active));
  if (indicator) indicator.setAttribute("aria-hidden", active ? "false" : "true");
}

function showActivity(text) {
  $("activityText").textContent = text;
  $("activity").classList.toggle("hidden", !text);
  const retryVisible = Boolean(state.retryableRunId)
    && Boolean(text)
    && !selectedSessionIsRunning()
    && (!state.retryableSessionId || state.retryableSessionId === state.session?.id);
  $("retryRun").classList.toggle("hidden", !retryVisible);
}

function selectedSessionIsRunning() {
  return Boolean(state.session?.id && activeRunFor(state.session.id));
}

function ensureRunDraft(sessionID, operationID = "", messageID = "") {
  const runState = activeRunFor(sessionID);
  if (!runState) return null;
  const current = runState.runDraft;
  const startsNewMessage = Boolean(messageID && current?.messageId && current.messageId !== messageID);
  if (!current || current.sessionId !== sessionID || startsNewMessage) {
    runState.runDraft = {
      sessionId: sessionID,
      operationId: operationID,
      messageId: messageID,
      content: "",
      reasoning: "",
      internal: false,
      processing: true,
    };
    syncRunStateAliases();
    return runState.runDraft;
  }
  if (operationID) current.operationId = operationID;
  if (messageID) current.messageId = messageID;
  return current;
}

function continueRunProcessing(sessionID, operationID = activeRunFor(sessionID)?.runId || "") {
  const runState = activeRunFor(sessionID);
  if (!runState) return;
  runState.liveMessage = null;
  runState.runDraft = {
    sessionId: sessionID,
    operationId: operationID,
    messageId: "",
    content: "",
    reasoning: "",
    internal: false,
    processing: true,
  };
  syncRunStateAliases();
  if (state.session?.id === sessionID) {
    renderSelectedRunDraft();
    scrollMessages();
  }
}

function renderSelectedRunDraft() {
  if (!selectedSessionIsRunning()) return null;
  const sessionID = state.session.id;
  const runState = activeRunFor(sessionID);
  const draft = runState?.runDraft || ensureRunDraft(sessionID, runState?.runId || "");
  if (!runState || !draft) return null;
  const messageID = draft.messageId || `active-run-${draft.operationId || draft.sessionId}`;
  let messageNode = findMessage(messageID);
  if (!messageNode
    && runState.liveMessage?.isConnected
    && runState.liveMessage.dataset.runDraftPlaceholder === "true") {
    messageNode = runState.liveMessage;
    messageNode.dataset.messageId = messageID;
  }
  if (!messageNode) {
    messageNode = appendMessage(
      { role: "assistant", id: messageID, content: "" },
      { operationId: draft.operationId, placeholder: true },
    );
  }
  if (!messageNode) return null;
  messageNode.dataset.runDraftPlaceholder = draft.messageId ? "false" : "true";
  if (draft.operationId) messageNode.dataset.operationId = draft.operationId;
  const content = messageNode.querySelector(".content");
  if (content && richContentSource(content) !== draft.content) scheduleRichContent(content, draft.content);
  if (draft.reasoning) {
    setMessageReasoning(messageNode, draft.reasoning);
    revealLatestReasoning(messageNode, draft.operationId);
  }
  messageNode.classList.toggle("reasoning-only", Boolean(draft.internal));
  setAgentProcessing(messageNode, draft.processing);
  runState.liveMessage = messageNode;
  state.liveMessage = messageNode;
  $("emptyState").classList.add("hidden");
  $("messages").classList.remove("hidden");
  return messageNode;
}

function revealLatestReasoning(messageNode, operationID = "") {
  const current = messageNode?.querySelector(":scope > .message-reasoning:not(.hidden)");
  if (!current) return;
  // 最新段落預設展開，但使用者一旦主動收合，就尊重該選擇。
  if (current.dataset.userOpen === "false") return;
  current.open = true;
  updateReasoningSummary(current);
}

function setRunActivity(text, sessionID = state.runningSessionId) {
  const runState = activeRunFor(sessionID);
  if (!runState) return;
  runState.activityText = text || "";
  syncRunStateAliases();
  // 等待狀態只放在對話區底部的狀態列：處理過程要留給實際做了什麼，
  // 每 15 秒更新一次的等待秒數放進去會把版面吃掉。
  if (state.session?.id === sessionID) showActivity(runState.activityText);
}

function syncSelectedRunUI() {
  syncRunStateAliases();
  const selectedRunning = selectedSessionIsRunning();
  syncReaskButton();
	// 每個 Session 各自執行；目前選取的 Session 可繼續輸入並排入自身佇列，
	// 其他 Session 的 Run 只在側欄顯示背景執行狀態。
  $("prompt").disabled = !state.session;
	syncSessionRuntimeControlState();
	syncRunActionButton();
	renderPromptQueue();
	if (selectedRunning) {
		// 一般執行狀態由訊息區動畫與停止按鈕表達；這一列只保留給斷線
		// 重連等需要使用者注意的狀態。
		showActivity(state.runActivityText);
    renderSelectedRunDraft();
    if (state.pendingApproval && state.pendingApprovalSessionId === state.session?.id) {
      showApproval(state.pendingApproval, state.session.id);
    }
    return;
  }
  if ($("approvalDialog").open && state.pendingApprovalSessionId !== state.session?.id) $("approvalDialog").close();
  if (state.retryableRunId && (!state.retryableSessionId || state.retryableSessionId === state.session?.id)) {
    // 可重試狀態保留供內部恢復流程使用，但不在對話區顯示重複的提示列。
    showActivity("");
    return;
  }
  showActivity(runDurationText(state.session?.id || ""));
}

function syncRunActionButton() {
  const button = $("send");
  if (!button) return;
  const selectedRunning = selectedSessionIsRunning();
  const label = selectedRunning ? "加入待送佇列" : "送出訊息";
  button.setAttribute("aria-label", label);
  button.title = label;
  button.disabled = !state.session;
  // 執行中由停止按鈕占用同一個操作位置；使用者仍可按 Enter
  // 送出輸入內容，讓 sendPrompt 將它加入目前對話的待送佇列。
  button.classList.toggle("hidden", selectedRunning);
  const stopButton = $("stopRun");
  stopButton.classList.toggle("hidden", !selectedRunning);
  stopButton.disabled = !selectedRunning || !state.currentRunId || state.canceling;
  const pauseButton = $("pauseRun");
  const resumeButton = $("resumeRun");
  if (pauseButton) pauseButton.disabled = !selectedRunning || !state.currentRunId || state.canceling || !["queued", "running"].includes(state.currentRunStatus);
  if (resumeButton) resumeButton.disabled = !selectedRunning || !state.currentRunId || state.canceling || state.currentRunStatus !== "paused";
  syncPlanButton();
  syncNativeConversationActivity();
}

function setContextCompactionState(sessionID, active) {
  if (active) {
    state.contextCompactionSessions.add(sessionID);
  } else if (!sessionID) {
    state.contextCompactionSessions.clear();
  } else {
    state.contextCompactionSessions.delete(sessionID);
  }
  state.contextCompactionSessionId = state.contextCompactionSessions.has(state.session?.id)
    ? state.session.id
    : "";
  renderContextCompactionState();
}

function renderContextCompactionState() {
  const active = Boolean(state.session?.id && state.contextCompactionSessions.has(state.session.id));
  $("contextCompactionIndicator")?.classList.toggle("hidden", !active);
  $("contextUsageButton")?.classList.toggle("is-compacting", active);
}

function syncNativeConversationActivity() {
  const active = Boolean(state.running || state.queueDraining || hasLaunchablePromptQueue());
  if (nativeConversationActivity === active) return;
  nativeConversationActivity = active;
  if (typeof window.nrInternSetConversationActive !== "function") return;
  Promise.resolve(window.nrInternSetConversationActive(active)).catch(() => {
    nativeConversationActivity = null;
  });
}

function activateRunAction() {
  $("composer").requestSubmit();
}

function attachmentIcon(mediaType = "") {
  if (mediaType.startsWith("image/")) return "▧";
  if (mediaType.startsWith("audio/")) return "♫";
  if (mediaType.startsWith("video/")) return "▶";
  return "▤";
}

function formatBytes(value) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size >= 10 || index === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[index]}`;
}

function defaultAttachmentName(file, index) {
  const original = normalizeFullwidthASCII(file?.name || "").trim();
  if (original) return original;
  const extension = {
    "image/png": ".png",
    "image/jpeg": ".jpg",
    "image/gif": ".gif",
    "image/webp": ".webp",
    "audio/mpeg": ".mp3",
    "audio/wav": ".wav",
    "video/mp4": ".mp4",
  }[file?.type] || "";
  return `clipboard-${Date.now()}-${index + 1}${extension}`;
}

function addPendingAttachments(files) {
  if (!state.session) return;
  const existing = new Set(state.pendingAttachments.map((item) => item.key));
  let rejectedSize = 0;
  let rejectedCount = 0;
  for (const [index, file] of [...(files || [])].entries()) {
    if (!(file instanceof File)) continue;
    if (file.size > maxAttachmentBytes) {
      rejectedSize += 1;
      continue;
    }
    if (state.pendingAttachments.length >= maxPendingAttachments) {
      rejectedCount += 1;
      continue;
    }
    const name = defaultAttachmentName(file, index);
    const key = `${name}\u0000${file.size}\u0000${file.lastModified}\u0000${file.type}`;
    if (existing.has(key)) continue;
    existing.add(key);
    state.pendingAttachments.push({
      file,
      name,
      key,
      previewURL: file.type.startsWith("image/") ? URL.createObjectURL(file) : "",
    });
  }
  state.attachmentSessionId = state.session.id;
  renderPendingAttachments();
  if (rejectedSize) toast(`${rejectedSize} 個附件超過單檔 8 MB 上限`);
  else if (rejectedCount) toast("一次最多加入 16 個附件");
}

function removePendingAttachment(key) {
  const item = state.pendingAttachments.find((value) => value.key === key);
  if (item?.previewURL) URL.revokeObjectURL(item.previewURL);
  state.pendingAttachments = state.pendingAttachments.filter((value) => value.key !== key);
  renderPendingAttachments();
}

function clearPendingAttachments() {
  for (const item of state.pendingAttachments || []) {
    if (item.previewURL) URL.revokeObjectURL(item.previewURL);
  }
  state.pendingAttachments = [];
  state.attachmentSessionId = "";
  if ($("attachmentTray")) renderPendingAttachments();
}

function renderPendingAttachments() {
  const tray = $("attachmentTray");
  if (!tray) return;
  tray.replaceChildren();
  for (const item of state.pendingAttachments) {
    const row = document.createElement("div");
    row.className = "pending-attachment";
    let preview;
    if (item.previewURL) {
      preview = document.createElement("img");
      preview.src = item.previewURL;
      preview.alt = "";
      preview.className = "pending-attachment-preview";
    } else {
      preview = document.createElement("span");
      preview.className = "pending-attachment-icon";
      preview.textContent = attachmentIcon(item.file.type);
    }
    const copy = document.createElement("span");
    copy.className = "attachment-copy";
    const name = document.createElement("strong");
    name.textContent = item.name;
    name.title = item.name;
    const size = document.createElement("small");
    size.textContent = formatBytes(item.file.size);
    copy.append(name, size);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "remove-attachment";
    remove.setAttribute("aria-label", `移除 ${item.name}`);
    remove.title = "移除附件";
    remove.textContent = "×";
    remove.addEventListener("click", () => removePendingAttachment(item.key));
    row.append(preview, copy, remove);
    tray.append(row);
  }
  tray.classList.toggle("hidden", state.pendingAttachments.length === 0);
}

async function uploadPendingAttachments(sessionID, pending) {
  if (!pending.length) return [];
  const body = new FormData();
  for (const item of pending) body.append("files", item.file, item.name);
  const response = await fetch(`/backend/api/v1/sessions/${encodeURIComponent(sessionID)}/attachments`, {
    method: "POST",
    body,
  });
  if (!response.ok) {
    let detail = response.statusText;
    try { detail = (await response.json()).detail || detail; } catch (_) {}
    throw new Error(detail);
  }
  return (await response.json()).data || [];
}

function openPromptOutbox() {
  if (!window.indexedDB) return Promise.reject(new Error("此瀏覽器不支援待送訊息持久化"));
  if (outboxDatabasePromise) return outboxDatabasePromise;
  outboxDatabasePromise = new Promise((resolve, reject) => {
    const request = window.indexedDB.open("nr-intern-outbox", 1);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains("prompts")) request.result.createObjectStore("prompts", { keyPath: "id" });
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("無法開啟待送訊息儲存"));
  });
  return outboxDatabasePromise;
}

function outboxTransaction(mode, operation) {
  return openPromptOutbox().then((database) => new Promise((resolve, reject) => {
    const transaction = database.transaction("prompts", mode);
    const store = transaction.objectStore("prompts");
    let request;
    try {
      request = operation(store);
    } catch (error) {
      reject(error);
      return;
    }
    let result;
    if (request) {
      request.onsuccess = () => { result = request.result; };
      request.onerror = () => reject(request.error || new Error("待送訊息儲存失敗"));
    }
    transaction.oncomplete = () => resolve(result);
    transaction.onerror = () => reject(transaction.error || new Error("待送訊息儲存失敗"));
    transaction.onabort = () => reject(transaction.error || new Error("待送訊息儲存已中止"));
  }));
}

function serializablePromptItem(item) {
  return {
    id: item.id,
    sessionId: item.sessionId,
    input: item.input,
    idempotencyKey: item.idempotencyKey,
    attachmentIds: Array.isArray(item.attachmentIds) ? [...item.attachmentIds] : [],
    attachments: (item.attachments || []).map((attachment) => ({
      key: attachment.key,
      name: attachment.name,
      file: attachment.file,
    })),
    status: item.status || "pending",
    runId: item.runId || "",
    lastSequence: Number(item.lastSequence || 0),
    error: item.error || "",
    createdAt: item.createdAt || new Date().toISOString(),
  };
}

async function savePromptOutboxItem(item) {
  if (!state.outboxAvailable) return;
  try {
    await outboxTransaction("readwrite", (store) => store.put(serializablePromptItem(item)));
  } catch (error) {
    state.outboxAvailable = false;
    throw new Error(`待送訊息無法保存：${error.message}`);
  }
}

async function deletePromptOutboxItem(id) {
  if (!state.outboxAvailable) return;
  try {
    await outboxTransaction("readwrite", (store) => store.delete(id));
  } catch (error) {
    state.outboxAvailable = false;
    throw new Error(`待送訊息無法移除：${error.message}`);
  }
}

async function loadPromptOutbox() {
  try {
    const items = await outboxTransaction("readonly", (store) => store.getAll());
    state.promptQueue = (Array.isArray(items) ? items : [])
      .map((item) => ({
        ...item,
        status: item.status === "sending" ? "pending" : (item.status || "pending"),
        attachments: Array.isArray(item.attachments) ? item.attachments : [],
        attachmentIds: Array.isArray(item.attachmentIds) ? item.attachmentIds : [],
      }))
      .sort((left, right) => String(left.createdAt).localeCompare(String(right.createdAt)));
    for (const item of state.promptQueue) {
      if (item.status === "pending") await savePromptOutboxItem(item);
    }
  } catch (error) {
    state.outboxAvailable = false;
    toast(`待送訊息無法恢復：${error.message}`);
  } finally {
    state.outboxLoaded = true;
    renderPromptQueue();
    if (state.backendHealthy) void drainPromptQueue();
  }
}

function promptQueueCanAutoLaunch(item) {
  return item?.status === "pending"
    && !state.restoreSkippedSessionIDs.has(item.sessionId);
}

function hasLaunchablePromptQueue() {
  return state.promptQueue.some(promptQueueCanAutoLaunch);
}

function dataTransferHasFiles(dataTransfer) {
  return [...(dataTransfer?.types || [])].includes("Files") || (dataTransfer?.files?.length || 0) > 0;
}

function renderPromptQueue() {
  const tray = $("promptQueue");
  if (!tray) return;
  tray.replaceChildren();
  const sessionID = state.session?.id || "";
  // 執行中的輸入已經顯示在對話區；只保留真正等待送出或需要重試的項目，
  // 避免同一段文字同時出現在使用者訊息與輸入框佇列中。
  const queued = state.promptQueue.filter((item) => item.sessionId === sessionID && item.status !== "sending");
  for (const [index, item] of queued.entries()) {
    const row = document.createElement("div");
    row.className = "queued-prompt";
    const status = document.createElement("span");
    status.className = "queued-prompt-status";
    status.textContent = item.status === "failed" ? "需要重試" : item.status === "sending" ? "傳送中" : `排隊 ${index + 1}`;
    status.classList.toggle("error", item.status === "failed");
    const content = document.createElement("span");
    content.className = "queued-prompt-content";
    const attachmentCount = item.attachments.length;
    content.textContent = `${item.input}${attachmentCount ? ` · ${attachmentCount} 個附件` : ""}`;
    content.title = content.textContent;
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "remove-queued-prompt";
    remove.textContent = "×";
    remove.title = "移除待送訊息";
    remove.setAttribute("aria-label", `移除排隊中的第 ${index + 1} 則訊息`);
    remove.disabled = item.status === "sending";
    remove.addEventListener("click", () => { void removeQueuedPrompt(item.id); });
    if (item.status === "failed") {
      const retry = document.createElement("button");
      retry.type = "button";
      retry.className = "retry-queued-prompt";
      retry.textContent = "↻";
      retry.title = "重試待送訊息";
      retry.setAttribute("aria-label", "重試待送訊息");
      retry.addEventListener("click", () => { void retryQueuedPrompt(item.id); });
      row.append(status, content, retry, remove);
    } else {
      row.append(status, content, remove);
    }
    tray.append(row);
  }
  tray.classList.toggle("hidden", queued.length === 0);
  syncNativeConversationActivity();
}

async function removeQueuedPrompt(queueID) {
  const item = state.promptQueue.find((value) => value.id === queueID);
  if (!item || item.status === "sending") return;
  state.promptQueue = state.promptQueue.filter((value) => value.id !== queueID);
  try {
    await deletePromptOutboxItem(queueID);
  } catch (error) {
    state.promptQueue.push(item);
    state.promptQueue.sort((left, right) => String(left.createdAt).localeCompare(String(right.createdAt)));
    toast(error.message);
  }
  renderPromptQueue();
}

function clearPromptQueue({ persist = false } = {}) {
  state.promptQueue = [];
  if ($("promptQueue")) renderPromptQueue();
  if (persist && state.outboxAvailable) {
    void outboxTransaction("readwrite", (store) => store.clear()).catch((error) => toast(`清除待送訊息失敗：${error.message}`));
  }
}

async function retryQueuedPrompt(queueID) {
  const item = state.promptQueue.find((value) => value.id === queueID);
  if (!item || item.status !== "failed") return;
  item.status = "pending";
  item.error = "";
  await savePromptOutboxItem(item).catch((error) => toast(error.message));
  renderPromptQueue();
  void drainPromptQueue();
}

// enqueuePrompt 是所有送出路徑的共同入口（輸入框、重新提問、佇列補送）：
// outbox 與佇列規則只寫一次，重新提問才不會有一套自己的送出流程。
async function enqueuePrompt(input, { attachments = [], attachmentIds = [] } = {}) {
  if (!state.session) return false;
  const queued = selectedSessionIsRunning()
    || state.promptQueue.some((item) => item.sessionId === state.session.id && item.status !== "failed");
  const item = {
    id: crypto.randomUUID(),
    sessionId: state.session.id,
    input,
    attachments,
    attachmentIds,
    idempotencyKey: crypto.randomUUID(),
    status: "pending",
    runId: "",
    lastSequence: 0,
    error: "",
    createdAt: new Date().toISOString(),
  };
  try {
    await savePromptOutboxItem(item);
  } catch (error) {
    toast(error.message);
    return false;
  }
  state.promptQueue.push(item);
  renderPromptQueue();
  syncRunActionButton();
  if (queued) toast(`已加入待送佇列（共 ${state.promptQueue.length} 則）`);
  void drainPromptQueue();
  return true;
}

async function sendPrompt(event) {
  event.preventDefault();
  let input = normalizeFullwidthASCII($("prompt").value).trim();
  const pending = [...state.pendingAttachments];
  if ((!input && pending.length === 0) || !state.session) return;
  if (!input) input = "請檢視附件。";
  if (!(await enqueuePrompt(input, { attachments: pending }))) return;
  $("prompt").value = "";
  if (pending.length) clearPendingAttachments();
}

async function drainPromptQueue() {
  if (!state.outboxLoaded || state.queueDraining || !state.backendHealthy || !hasLaunchablePromptQueue()) return;
  state.queueDraining = true;
  try {
    const occupied = new Set([
      ...state.activeRuns.keys(),
      ...state.promptQueue.filter((item) => item.status === "sending").map((item) => item.sessionId),
    ]);
    const launchable = [];
    for (const item of state.promptQueue) {
      if (!promptQueueCanAutoLaunch(item) || occupied.has(item.sessionId)) continue;
      occupied.add(item.sessionId);
      item.status = "sending";
      launchable.push(item);
    }
    renderPromptQueue();
    // 每個 Session 同時最多一個 Run，但不同 Session 可以並行執行。
    for (const item of launchable) {
      void (async () => {
        const completed = await executePrompt(item);
        if (completed) {
          state.promptQueue = state.promptQueue.filter((value) => value.id !== item.id);
          await deletePromptOutboxItem(item.id).catch((error) => toast(error.message));
        }
        renderPromptQueue();
        void drainPromptQueue();
      })();
    }
  } finally {
    state.queueDraining = false;
    renderPromptQueue();
    syncRunActionButton();
    if (hasLaunchablePromptQueue()) void drainPromptQueue();
  }
}

async function executePrompt(item) {
  const sessionID = item.sessionId;
  const input = item.input;
  const pending = item.attachments;
  item.status = "sending";
  item.error = "";
  try {
    await savePromptOutboxItem(item);
  } catch (error) {
    item.status = "failed";
    item.error = error.message;
    renderPromptQueue();
    toast(error.message);
    return false;
  }
  const runState = addActiveRun(newRunState(sessionID));
  state.retryableRuns.delete(sessionID);
  syncRunStateAliases();
  if (state.session?.id === sessionID) {
    $("emptyState").classList.add("hidden");
    $("messages").classList.remove("hidden");
  }
  renderNavigation();
  try {
    const attachments = item.attachmentIds.length > 0
      ? item.attachmentIds.map((id) => ({ id }))
      : await uploadPendingAttachments(sessionID, pending);
    if (item.attachmentIds.length === 0) {
      item.attachmentIds = attachments.map((attachment) => attachment.id);
      item.attachments = [];
      await savePromptOutboxItem(item);
    }
    if (state.session?.id === sessionID) {
      appendMessage({ role: "user", content: input, metadata: { attachments }, id: `local-${Date.now()}` });
      // 使用者問題必須先進入 DOM，接著才建立本輪 assistant placeholder；
      // 否則 syncSelectedRunUI 會先插入處理區塊，造成動畫出現在問題上方。
      syncSelectedRunUI();
      renderSelectedRunDraft();
      scrollMessages({ force: true });
    } else {
      syncSelectedRunUI();
    }
    await runWithReconnect({
      sessionId: sessionID,
      input,
      attachmentIds: item.attachmentIds,
      idempotencyKey: item.idempotencyKey,
      runId: item.runId,
      lastSequence: item.lastSequence,
      runState,
      onRunIdentified: async (runID, lastSequence) => {
        item.runId = runID;
        item.lastSequence = lastSequence;
        await savePromptOutboxItem(item);
      },
      onSequence: async (sequence) => {
        item.lastSequence = sequence;
        await savePromptOutboxItem(item);
      },
    });
    return true;
  } catch (error) {
    item.status = "failed";
    item.error = error.message;
    await savePromptOutboxItem(item).catch(() => {});
    toast(error.message);
    return false;
  } finally {
    await finishActiveRun(runState);
  }
}

async function runWithReconnect(task) {
  const sessionID = task.sessionId || state.runningSessionId;
  const runState = task.runState || activeRunFor(sessionID);
  if (!runState) throw new Error("找不到此 Session 的執行狀態");
  const progress = { runId: task.runId || "", lastSequence: Number(task.lastSequence || 0), terminal: false };
  let reconnects = 0;
  while (!progress.terminal && !runState.terminal) {
    try {
      const response = await openRunStream(task, progress);
      progress.runId = response.headers.get("X-Run-ID") || progress.runId;
      if (!progress.runId) throw new Error("後端未回傳 Run ID，無法安全重連");
      runState.runId = progress.runId;
      runState.runDraft.operationId = progress.runId;
      if (typeof task.onRunIdentified === "function") await task.onRunIdentified(progress.runId, progress.lastSequence);
      if (!runState.status) runState.status = "running";
      syncRunStateAliases();
      syncRunActionButton();
		  setRunActivity("", sessionID);
      await consumeEvents(response.body, progress, sessionID);
      if (typeof task.onSequence === "function") await task.onSequence(progress.lastSequence);
      if (progress.terminal) return;
      const run = await request(`/api/v1/runs/${encodeURIComponent(progress.runId)}`);
      if (["completed", "failed", "canceled"].includes(run.status)) {
        handleTerminalRun(run, sessionID);
        progress.terminal = true;
        return;
      }
      throw new Error("事件連線提前結束");
    } catch (error) {
      if (progress.terminal || runState.terminal || error.name === "AbortError") return;
      if (reconnects >= 3) throw new Error(`重連 3 次仍失敗，連線已中斷：${error.message}`);
      reconnects += 1;
      setRunActivity(`連線中斷，正在重連並補回事件（${reconnects}/3）`, sessionID);
      await delay(400 * (2 ** (reconnects - 1)));
    }
  }
}

async function openRunStream(task, progress) {
  const reconnect = Boolean(progress.runId);
  const path = reconnect
    ? `/backend/api/v1/runs/${encodeURIComponent(progress.runId)}/events`
    : `/backend/api/v1/sessions/${encodeURIComponent(task.sessionId)}/runs:stream`;
  const headers = reconnect
    ? { "Last-Event-ID": String(progress.lastSequence) }
    : { "Content-Type": "application/json", "Idempotency-Key": task.idempotencyKey };
  const options = reconnect ? { headers } : {
    method: "POST",
    headers,
    body: JSON.stringify({ input: task.input, attachment_ids: task.attachmentIds || [] }),
  };
  if (task.runState?.eventController) options.signal = task.runState.eventController.signal;
  const response = await fetch(path, options);
  if (!response.ok) {
    let detail = response.statusText;
    try { detail = (await response.json()).detail || detail; } catch (_) {}
    throw new Error(detail);
  }
  return response;
}

async function consumeEvents(stream, progress, sessionID) {
  if (!stream) throw new Error("後端未提供事件串流");
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replaceAll("\r\n", "\n");
    let boundary;
    while ((boundary = buffer.indexOf("\n\n")) >= 0) {
      const block = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const data = block.split("\n")
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trimStart())
        .join("\n");
      if (!data) continue;
      const event = JSON.parse(data);
      if (Number.isInteger(event.sequence) && event.sequence > progress.lastSequence) progress.lastSequence = event.sequence;
      handleEvent(event, sessionID);
      if (["run.completed", "run.failed", "run.canceled"].includes(event.type)) {
        progress.terminal = true;
        // 終止事件已經是完整結果，不應繼續等待 SSE 自己斷線；部分後端／
        // 反向代理會保留連線，否則停止後 UI 永遠等不到 finishActiveRun。
        await reader.cancel().catch(() => {});
        return;
      }
    }
  }
}

function handleTerminalRun(run, sessionID) {
  const runState = activeRunFor(sessionID);
  if (runState?.terminalHandled) return;
  if (runState) {
    runState.runId = run.id || runState.runId;
    runState.status = run.status || runState.status;
    runState.terminal = true;
    runState.terminalHandled = true;
    runState.canceling = false;
  }
  const visible = state.session?.id === sessionID;
  const startedAt = Date.parse(run.started_at);
  const completedAt = Date.parse(run.completed_at);
  if (visible && Number.isFinite(startedAt) && Number.isFinite(completedAt)) {
    finalizeReasoningGroup(run.id, completedAt - startedAt);
  }
  state.runStartedAt.delete(run.id);
  setContextCompactionState(sessionID, false);
  if (visible) setAgentProcessing(runState?.liveMessage || state.liveMessage, false);
  if (run.status === "failed" || run.status === "canceled") {
    if (run.error?.retryable) {
      state.retryableRuns.set(sessionID, run);
    }
    if (visible) {
      appendRunOutcome({ id: run.id, status: run.status, error: run.error });
      scrollMessages();
    }
    toast(run.error?.message || `Run ${run.status}`);
  }
  syncRunStateAliases();
}

function handleEvent(event, sessionID) {
  const payload = event.payload || {};
  const visible = state.session?.id === sessionID;

  const runState = activeRunFor(sessionID);
  // 停止已被本地確認後，串流中尚未送達的 late event 不得重新點亮處理動畫。
  if (!runState || runState.terminalHandled) return;
  if (event.run_id) runState.runId = String(event.run_id);
	if (event.type === "context.compaction.started") {
	  setContextCompactionState(sessionID, true);
	} else if (["context.compacted", "context.compaction.failed"].includes(event.type)) {
	  setContextCompactionState(sessionID, false);
	}
	if (visible && event.type === "turn.usage" && payload.usage) {
	  const identity = activeContextIdentity();
	  recordContextUsage(payload.usage, sessionID, identity.providerID, identity.model);
	}
	if (visible && event.type === "tool.execution.end" && String(payload.result?.tool_name || "").startsWith("plan_")) {
	  loadPlans(sessionID).catch(() => {});
	}
	if (visible && event.type === "plan.completion_check") loadPlans(sessionID).catch(() => {});
	const operationID = String(event.run_id || runState.runId || "");
	if (event.type === "run.started" && operationID && !state.runStartedAt.has(operationID)) {
		runState.status = "running";
	  const startedAt = Date.parse(event.created_at);
	  state.runStartedAt.set(operationID, Number.isFinite(startedAt) ? startedAt : Date.now());
	  const draft = ensureRunDraft(sessionID, operationID);
	  if (draft) draft.processing = true;
	  syncRunStateAliases();
	  if (visible) renderSelectedRunDraft();
	}
	if (event.type === "run.paused") {
		runState.status = "paused";
		setRunActivity("Run 已暫停", sessionID);
		syncRunActionButton();
	} else if (event.type === "run.resumed") {
		runState.status = "running";
		setRunActivity("", sessionID);
		syncRunActionButton();
	}
	if (event.type === "message.start" && payload.message?.role === "assistant") {
    const message = normalizeAssistantThinking(payload.message);
    const draft = ensureRunDraft(sessionID, operationID, message.id);
    if (!draft) return;
    draft.content = message.content || "";
    draft.reasoning = message.reasoning || "";
    draft.processing = !draft.content;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "message.delta") {
    const draft = ensureRunDraft(sessionID, operationID, payload.message_id);
    if (!draft) return;
    const delta = payload.delta || "";
    draft.content += delta;
    // 第一個回答字元出現後，文字串流本身就是進度，不再同時顯示思考動畫。
    if (delta.length > 0) draft.processing = false;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "message.thinking_delta") {
    const draft = ensureRunDraft(sessionID, operationID, payload.message_id);
    if (!draft) return;
    draft.reasoning = appendReasoningDelta(draft.reasoning, payload.delta || "");
    draft.processing = true;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "tool_call.delta") {
    // 收到工具呼叫片段後，可以確定目前 Response 只是 Run 的中間階段。
    // 將先前已串流到主回答的文字即時移入處理過程，不等工具執行結束。
    const draft = ensureRunDraft(sessionID, operationID, payload.message_id);
    if (!draft) return;
    draft.reasoning = joinReasoningParts(draft.reasoning, draft.content);
    draft.content = "";
    draft.internal = true;
    draft.processing = true;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "message.end" && payload.message?.role === "assistant") {
    const message = normalizeAssistantThinking(payload.message);
    if (visible && message.usage) {
      const identity = activeContextIdentity();
      recordContextUsage(message.usage, sessionID, message.provider_id || identity.providerID, message.model || identity.model);
    }
    const draft = ensureRunDraft(sessionID, operationID, message.id);
    if (!draft) return;
    const internal = message.metadata?.internal === true
      || (Array.isArray(message.tool_calls) && message.tool_calls.length > 0);
    if (internal) {
      draft.reasoning = joinReasoningParts(draft.reasoning, message.reasoning, draft.content, message.content);
      draft.content = "";
      draft.internal = true;
    } else {
      draft.content = message.content || draft.content;
      draft.reasoning = message.reasoning || draft.reasoning;
      draft.internal = false;
    }
    // 一般回答已開始輸出時停止動畫；內部訊息結束後會建立下一個處理中區塊。
    draft.processing = internal;
    if (!visible) {
      if (internal) continueRunProcessing(sessionID, operationID);
      return;
    }
    runState.liveMessage = renderSelectedRunDraft() || findMessage(message.id) || runState.liveMessage;
    syncRunStateAliases();
    if (runState.liveMessage && operationID) runState.liveMessage.dataset.operationId = operationID;
    if (internal) {
      if (draft.reasoning && runState.liveMessage) {
        setMessageReasoning(runState.liveMessage, draft.reasoning);
        runState.liveMessage.classList.add("reasoning-only");
        renderRichContent(runState.liveMessage.querySelector(".content"), "");
        setAgentProcessing(runState.liveMessage, false);
        mergeReasoningIntoPrevious(runState.liveMessage, operationID);
      } else {
        runState.liveMessage?.remove();
      }
      runState.liveMessage = null;
      runState.runDraft = null;
      syncRunStateAliases();
      continueRunProcessing(sessionID, operationID);
    } else {
      if (runState.liveMessage) {
        const content = runState.liveMessage.querySelector(".content");
        renderRichContent(content, message.content || richContentSource(content));
        if (message.reasoning) setMessageReasoning(runState.liveMessage, message.reasoning);
        if (message.reasoning) mergeReasoningIntoPrevious(runState.liveMessage, operationID);
      }
      setAgentProcessing(runState.liveMessage, false);
    }
	} else if (event.type === "tool.execution.start") {
    // 工具呼叫本身就是執行過程的一部分：只註記呼叫了什麼，不展開參數。
    const draft = ensureRunDraft(sessionID, operationID);
    const toolName = String(payload.tool_name || "").trim();
    if (draft && toolName) {
      draft.reasoning = joinReasoningParts(draft.reasoning, `${translate("呼叫工具")}：\`${toolName}\``);
      draft.processing = true;
      if (visible) {
        renderSelectedRunDraft();
        scrollMessages();
      }
    }
    setRunActivity("", sessionID);
  } else if (event.type === "tool.execution.update") {
    // 只把「還在等什麼」放到狀態列；工具自己的進度內容不展開成段落。
    const phase = payload.update?.details?.phase;
    if (phase === "mcp_waiting" || phase === "waiting_approval") {
      setRunActivity(String(payload.update?.content || "").trim(), sessionID);
    }
  } else if (event.type === "tool.execution.end") {
    setRunActivity("", sessionID);
    const draft = ensureRunDraft(sessionID, operationID);
    const failedTool = payload.result?.is_error ? String(payload.result?.tool_name || "").trim() : "";
    if (draft && failedTool) {
      draft.reasoning = joinReasoningParts(draft.reasoning, `${translate("工具失敗")}：\`${failedTool}\``);
      if (visible) renderSelectedRunDraft();
    }
	} else if (event.type === "run.approval_required" && payload.approval) {
    // 核准對話框可能因為切到別的對話而沒被看到；狀態列要說出它在等什麼。
    setRunActivity(`${translate("等待人工核准")}：${String(payload.approval.tool_name || "").trim()}`, sessionID);
    if (visible) setAgentProcessing(runState.liveMessage, false);
    showApproval(payload.approval, sessionID);
  } else if (event.type === "run.approval_resolved") {
    runState.pendingApproval = null;
    syncRunStateAliases();
    if (visible && $("approvalDialog").open) $("approvalDialog").close();
		setRunActivity("", sessionID);
		continueRunProcessing(sessionID, operationID);
		} else if (event.type === "run.completed") {
    if (runState.terminalHandled) return;
    runState.terminal = true;
    runState.terminalHandled = true;
    runState.canceling = false;
    runState.status = "completed";
    setContextCompactionState(sessionID, false);
    finalizeLiveReasoningDuration(event, operationID, visible, sessionID);
    if (visible) setAgentProcessing(runState.liveMessage, false);
    if (visible) loadPlans(sessionID).catch(() => {});
		} else if (event.type === "run.failed" || event.type === "run.canceled") {
    if (runState.terminalHandled) return;
    runState.terminal = true;
    runState.terminalHandled = true;
    runState.canceling = false;
    runState.status = event.type === "run.canceled" ? "canceled" : "failed";
    setContextCompactionState(sessionID, false);
    finalizeLiveReasoningDuration(event, operationID, visible, sessionID);
    if (visible) setAgentProcessing(runState.liveMessage, false);
    if (payload.error?.retryable) {
      state.retryableRuns.set(sessionID, {
        id: runState.runId,
        status: runState.status,
        error: payload.error,
        session_id: sessionID,
      });
    }
    if (visible) {
      appendRunOutcome({
        id: runState.runId,
        status: event.type === "run.canceled" ? "canceled" : "failed",
        error: payload.error,
      });
      scrollMessages();
    }
    toast(payload.error?.message || event.type);
  }
  syncRunStateAliases();
}

function finalizeLiveReasoningDuration(event, operationID, visible, sessionID = "") {
  const startedAt = state.runStartedAt.get(operationID);
  const completedAt = Date.parse(event.created_at);
  if (Number.isFinite(startedAt) && Number.isFinite(completedAt) && completedAt > startedAt) {
    if (visible) finalizeReasoningGroup(operationID, completedAt - startedAt);
    if (sessionID) state.lastRunDuration.set(sessionID, completedAt - startedAt);
  }
  state.runStartedAt.delete(operationID);
}

// runDurationText 是 Run 結束後留在狀態列的完成訊息。
//
// 等待秒數在結束的瞬間消失，使用者就沒機會知道這次到底等了多久——用本機模型時
// 那個數字正是他們在評估的東西。
function runDurationText(sessionID) {
  const duration = state.lastRunDuration.get(sessionID);
  if (!Number.isFinite(duration) || duration <= 0) return "";
  return `${translate("本次回應共使用")} ${formatProcessingDuration(duration)}`;
}

async function retryCurrentRun() {
  const sessionID = state.session?.id;
  const retryable = sessionID ? state.retryableRuns.get(sessionID) : null;
  const sourceRunId = retryable?.id || state.retryableRunId;
  if (!sourceRunId || !sessionID || activeRunFor(sessionID)) return;
  const runState = addActiveRun(newRunState(sessionID));
  state.retryableRuns.delete(sessionID);
  syncRunStateAliases();
  if (!sessionID) return;
  syncSelectedRunUI();
  $("retryRun").disabled = true;
  renderSelectedRunDraft();
  scrollMessages({ force: true });
  try {
    const run = await request(`/api/v1/runs/${encodeURIComponent(sourceRunId)}/retry`, {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: "{}",
    });
    runState.runId = run.id;
    runState.status = run.status || "queued";
    runState.runDraft.operationId = run.id;
    syncRunStateAliases();
    syncRunActionButton();
	  setRunActivity("", sessionID);
    await runWithReconnect({ runId: run.id, sessionId: sessionID, runState });
  } catch (error) {
    state.retryableRuns.set(sessionID, { id: sourceRunId, status: "failed", error, session_id: sessionID });
    toast(error.message);
  } finally {
    await finishActiveRun(runState);
    $("retryRun").disabled = false;
    refreshOpenProviderUsage();
  }
}

function showApproval(approval, sessionID = state.runningSessionId) {
  const runState = activeRunFor(sessionID);
  if (!runState) return;
  runState.pendingApproval = approval;
  syncRunStateAliases();
  if (state.session?.id !== sessionID) return;
  $("approvalToolName").textContent = approval.tool_name || "未知工具";
  $("approvalReasonText").textContent = approval.reason || "此工具需要人工確認。";
  const argumentsValue = approval.arguments || {};
  $("approvalArguments").textContent = JSON.stringify(argumentsValue, null, 2);
  $("approvalArgumentsField").classList.toggle("hidden", Object.keys(argumentsValue).length === 0);
  $("approvalDecisionReason").value = "";
  $("permanentApproval").checked = false;
  $("permanentApproval").disabled = false;
  $("approveTool").disabled = false;
  $("denyTool").disabled = false;
	if (state.session?.id === sessionID && !$("approvalDialog").open) $("approvalDialog").show();
}

async function decideApproval(decision) {
  const sessionID = state.session?.id;
  const runState = activeRunFor(sessionID);
  const approval = runState?.pendingApproval;
  if (!approval || !runState?.runId) return;
  $("approveTool").disabled = true;
  $("denyTool").disabled = true;
  $("permanentApproval").disabled = true;
  const permanent = decision === "approve" && $("permanentApproval").checked;
  try {
    await request(`/api/v1/runs/${encodeURIComponent(runState.runId)}/decision`, {
      method: "POST",
      body: JSON.stringify({
        approval_id: approval.id,
        decision,
        reason: $("approvalDecisionReason").value.trim(),
        permanent,
      }),
    });
	  setRunActivity("", sessionID);
  } catch (error) {
    toast(error.message);
    $("approveTool").disabled = false;
    $("denyTool").disabled = false;
    $("permanentApproval").disabled = false;
  }
}

async function cancelCurrentRun() {
  const sessionID = state.session?.id;
  const runState = activeRunFor(sessionID);
  if (!runState?.runId || runState.canceling) return;
  runState.canceling = true;
  setRunActivity(translate("正在停止…"), sessionID);
  syncRunStateAliases();
  syncRunActionButton();
  try {
    const canceledRun = await request(`/api/v1/runs/${encodeURIComponent(runState.runId)}/cancel`, { method: "POST", body: "{}" });
    if (activeRunFor(sessionID) !== runState) return;
    if (canceledRun?.status && ["completed", "failed", "canceled"].includes(canceledRun.status)) {
      handleTerminalRun(canceledRun, sessionID);
    } else {
      // 舊版或反向代理可能只回覆「已接受」而沒有帶回完整 Run；取消要求
      // 已送達時仍須停止本地串流，避免 UI 一直顯示執行中。
      runState.status = "canceled";
      runState.terminal = true;
      runState.terminalHandled = true;
      runState.canceling = false;
      setContextCompactionState(sessionID, false);
      if (state.session?.id === sessionID) {
        setAgentProcessing(runState.liveMessage, false);
        appendRunOutcome({ id: runState.runId, status: "canceled" });
      }
      syncRunStateAliases();
      syncSelectedRunUI();
    }
    runState.eventController?.abort();
    await finishActiveRun(runState);
  } catch (error) {
    if (activeRunFor(sessionID) !== runState) return;
    runState.canceling = false;
    syncRunStateAliases();
    syncRunActionButton();
    toast(error.message);
  }
}

async function pauseCurrentRun() {
  const sessionID = state.session?.id;
  const runState = activeRunFor(sessionID);
  if (!runState?.runId || runState.status === "paused") return;
  try {
    await request("/api/v1/runs/" + encodeURIComponent(runState.runId) + "/pause", { method: "POST", body: "{}" });
    runState.status = "paused"; setRunActivity("Run 已暫停", sessionID); syncRunActionButton();
  } catch (error) { toast(error.message); }
}

async function resumeCurrentRun() {
  const sessionID = state.session?.id;
  const runState = activeRunFor(sessionID);
  if (!runState?.runId || runState.status !== "paused") return;
  try {
    await request("/api/v1/runs/" + encodeURIComponent(runState.runId) + "/resume", { method: "POST", body: "{}" });
    runState.status = "running"; setRunActivity("", sessionID); syncRunActionButton();
  } catch (error) { toast(error.message); }
}

function findMessage(id) {
  if (!id) return null;
  return [...$("messages").querySelectorAll(".message")].find((element) => element.dataset.messageId === id) || null;
}

function messageDistanceFromBottom(container = $("messages")) {
  return Math.max(0, container.scrollHeight - container.clientHeight - container.scrollTop);
}

function messageAutoScrollThreshold(container = $("messages")) {
  return container.clientHeight * messageAutoScrollThresholdRatio;
}

function updateMessageAutoScroll() {
  const container = $("messages");
  state.messageAutoScroll = messageDistanceFromBottom(container) <= messageAutoScrollThreshold(container);
}

function moveMessagesToBottom(container) {
  // 避免 CSS smooth scrolling 產生的中途 scroll 事件被誤判為使用者向上捲動。
  const previousBehavior = container.style.scrollBehavior;
  container.style.scrollBehavior = "auto";
  container.scrollTop = container.scrollHeight;
  container.style.scrollBehavior = previousBehavior;
}

function scrollMessages({ force = false } = {}) {
  const container = $("messages");
  if (force) state.messageAutoScroll = true;
  if (!state.messageAutoScroll) return;
  moveMessagesToBottom(container);
  if (messageScrollFrame) return;
  // Markdown 會在下一個 animation frame 才完成版面更新；更新後再校正一次底部。
  messageScrollFrame = requestAnimationFrame(() => {
    messageScrollFrame = 0;
    if (state.messageAutoScroll) moveMessagesToBottom(container);
  });
}

function defaultSessionTitle(value = new Date()) {
  const month = String(value.getMonth() + 1).padStart(2, "0");
  const day = String(value.getDate()).padStart(2, "0");
  const hours = String(value.getHours()).padStart(2, "0");
  const minutes = String(value.getMinutes()).padStart(2, "0");
  return `${month}/${day} ${hours}:${minutes}`;
}

async function setPinned(session, pinned) {
  try {
    const updated = await request(`/api/v1/sessions/${encodeURIComponent(session.id)}`, {
      method: "PATCH",
      reconnects: 3,
      body: JSON.stringify({ pinned }),
    });
    state.sessions = state.sessions.map((item) => item.id === updated.id ? updated : item);
    if (state.session?.id === updated.id) state.session = updated;
    syncSessionUI();
    renderNavigation();
  } catch (error) {
    toast(error.message);
  }
}

function openRenameSessionDialog(session) {
  if (!session || sessionRunIsActive(session.id)) return;
  state.renamingSession = session;
  $("renameSessionTitle").value = session.title || "";
  $("renameSessionDialog").showModal();
  $("renameSessionTitle").focus();
  $("renameSessionTitle").select();
}

async function renameSession(event) {
  event.preventDefault();
  const session = state.renamingSession;
  const title = $("renameSessionTitle").value.trim();
  if (!session || !title || sessionRunIsActive(session.id)) return;
  $("saveRenameSession").disabled = true;
  try {
    const updated = await request(`/api/v1/sessions/${encodeURIComponent(session.id)}`, {
      method: "PATCH",
      reconnects: 3,
      body: JSON.stringify({ title }),
    });
    state.sessions = state.sessions.map((item) => item.id === updated.id ? updated : item);
    if (state.session?.id === updated.id) {
      state.session = updated;
      syncSessionUI();
    }
    renderNavigation();
    $("renameSessionDialog").close();
    toast(`已將對話改名為「${updated.title}」`);
  } catch (error) {
    toast(error.message);
  } finally {
    $("saveRenameSession").disabled = false;
  }
}

async function openSessionContent(session) {
  if (!session) return;
  state.inspectingSession = session;
  $("sessionContentTitle").textContent = session.title || "未命名對話";
  $("sessionContentMeta").textContent = session.id;
  const list = $("sessionContentList");
  list.replaceChildren();
  const loading = document.createElement("p");
  loading.className = "quiet session-content-state";
  loading.textContent = "正在載入對話內容…";
  list.append(loading);
  $("sessionContentDialog").showModal();
  try {
    const messages = await request(`/api/v1/sessions/${encodeURIComponent(session.id)}/messages`);
    if (state.inspectingSession?.id !== session.id || !$("sessionContentDialog").open) return;
    renderSessionContent(messages || []);
  } catch (error) {
    if (state.inspectingSession?.id !== session.id) return;
    list.replaceChildren();
    const failure = document.createElement("p");
    failure.className = "warning session-content-state";
    failure.textContent = `無法載入對話內容：${error.message}`;
    list.append(failure);
  }
}

function renderSessionContent(messages) {
  const list = $("sessionContentList");
  list.replaceChildren();
  const visibleMessages = messages.filter((message) => {
    if (!message || message.role !== "assistant") return true;
    return Boolean(
      String(message.content || "").trim()
      || (Array.isArray(message.tool_calls) && message.tool_calls.length > 0),
    );
  });
  $("sessionContentMeta").textContent = `${state.inspectingSession?.id || ""} · ${visibleMessages.length} 則訊息`;
  if (visibleMessages.length === 0) {
    const empty = document.createElement("p");
    empty.className = "quiet session-content-state";
    empty.textContent = "這個對話目前沒有內容。";
    list.append(empty);
    return;
  }
  list.append(...visibleMessages.map(sessionContentNode));
}

function sessionContentNode(message) {
  message = normalizeAssistantThinking(message);
  const article = document.createElement("article");
  article.className = "session-content-item message";
  const header = document.createElement("header");
  const role = document.createElement("strong");
  const time = document.createElement("time");
  const roleLabels = { user: "使用者", assistant: "Agent", tool: `工具${message.tool_name ? ` · ${message.tool_name}` : ""}` };
  role.textContent = roleLabels[message.role] || message.role || "訊息";
  time.textContent = message.created_at ? new Date(message.created_at).toLocaleString("zh-TW") : "";
  header.append(role, time);
  article.append(header);

  if (message.reasoning) {
    article.append(createReasoningBlock(message.reasoning, "session-content-reasoning"));
  }
  if (message.content) {
    if (message.role === "tool") {
      const pre = document.createElement("pre");
      pre.className = "session-tool-content";
      pre.textContent = message.content;
      article.append(pre);
    } else {
      const content = document.createElement("div");
      content.className = "content";
      renderRichContent(content, message.content);
      article.append(content);
    }
  }
  if (Array.isArray(message.tool_calls) && message.tool_calls.length > 0) {
    const details = document.createElement("details");
    const summary = document.createElement("summary");
    const pre = document.createElement("pre");
    summary.textContent = `${message.tool_calls.length} 個工具呼叫`;
    pre.className = "session-tool-content";
    pre.textContent = JSON.stringify(message.tool_calls, null, 2);
    details.append(summary, pre);
    article.append(details);
  }
  if (article.children.length === 1) {
    const empty = document.createElement("p");
    empty.className = "quiet";
    empty.textContent = "此訊息沒有文字內容。";
    article.append(empty);
  }
  return article;
}

function openDeleteSessionDialog(session) {
  if (!session || sessionRunIsActive(session.id)) return;
  state.deletingSession = session;
  $("deleteSessionMessage").textContent = `確定要刪除對話「${session.title || "未命名"}」嗎？`;
  $("deleteSessionDialog").showModal();
  $("cancelDeleteSession").focus();
}

async function deleteSession(event) {
  event.preventDefault();
  const session = state.deletingSession;
  if (!session || sessionRunIsActive(session.id)) return;
  $("confirmDeleteSession").disabled = true;
  try {
    await request(`/api/v1/sessions/${encodeURIComponent(session.id)}`, { method: "DELETE" });
    state.sessions = state.sessions.filter((item) => item.id !== session.id);
    if (state.session?.id === session.id) {
      state.session = null;
      clearSessionUI();
    }
    renderNavigation();
    $("deleteSessionDialog").close();
    toast(`已刪除對話「${session.title || "未命名"}」`);
  } catch (error) {
    toast(error.message);
  } finally {
    $("confirmDeleteSession").disabled = false;
  }
}

function openSessionDialog(projectID = state.session?.project_id || "") {
  $("sessionForm").reset();
  renderProjectOptions();
  renderSessionProviderOptions();
  renderPermissionOptions();
  $("newSessionProject").value = projectID;
  $("newTitle").value = defaultSessionTitle();
  $("newProvider").value = state.workspace?.default_provider_id || "";
  $("newModel").value = defaultModelForProvider($("newProvider").value);
  $("sessionDialog").showModal();
  $("newTitle").focus();
}

async function createSession(event) {
  event.preventDefault();
  if (!state.agent || !state.workspace) return;
  const memoryScope = $("newMemoryScope").value.trim();
  const input = {
    title: $("newTitle").value.trim() || defaultSessionTitle(),
    workspace_id: state.workspace.id,
    project_id: $("newSessionProject").value,
    provider_id: $("newProvider").value.trim(),
    model: $("newModel").value.trim(),
    permission_profile: $("newPermission").value,
    pinned: $("newPinned").checked,
  };
  if (memoryScope) input.metadata = { memory_scope: memoryScope };
  try {
    const session = await request(`/api/v1/agents/${encodeURIComponent(state.agent.id)}/sessions`, {
      method: "POST",
      body: JSON.stringify(input),
    });
    $("sessionDialog").close();
    $("sessionForm").reset();
    await loadSessions();
    await selectSession(session);
  } catch (error) {
    toast(error.message);
  }
}

async function createDefaultProjectSession(projectID) {
  const session = await request(`/api/v1/agents/${encodeURIComponent(state.agent.id)}/sessions`, {
    method: "POST",
    body: JSON.stringify({
      title: defaultSessionTitle(),
      workspace_id: state.workspace.id,
      project_id: projectID,
    }),
  });
  await loadSessions();
  await selectSession(session);
}

async function createProject(event) {
  event.preventDefault();
  if (!state.workspace) return;
  try {
    const project = await request("/api/v1/projects", {
      method: "POST",
      body: JSON.stringify({
        name: $("newProjectName").value.trim(),
        workspace_id: state.workspace.id,
        description: $("newProjectDescription").value.trim(),
        instructions: $("newProjectInstructions").value.trim(),
        sandbox_roots: [...state.newProjectSandboxRoots],
      }),
    });
    $("projectDialog").close();
    resetProjectForm();
    await loadProjects();
    await createDefaultProjectSession(project.id);
  } catch (error) {
    toast(error.message);
  }
}

function openScheduleDialog(schedule) {
  state.editingSchedule = schedule || null;
  const form = $("scheduleForm");
  form.reset();
  const recurrence = schedule?.recurrence || {};
  const frequency = ["interval", "daily", "weekly"].includes(recurrence.frequency) ? recurrence.frequency : "interval";
  $("scheduleDialogTitle").textContent = schedule ? "排程設定" : "建立排程";
  $("saveSchedule").textContent = schedule ? "儲存" : "建立";
  $("scheduleDialogState").textContent = "";
  $("scheduleName").value = schedule?.name || "";
  $("schedulePrompt").value = schedule?.prompt || "";
  $("scheduleFrequency").value = frequency;
  $("scheduleIntervalMinutes").value = String(recurrence.interval_minutes || 60);
  $("scheduleTimeOfDay").value = recurrence.time_of_day || "09:00";
  const weekdays = new Set(recurrence.weekdays || []);
  for (const input of document.querySelectorAll("#scheduleWeekdayField input[data-weekday]")) {
    input.checked = weekdays.has(Number(input.dataset.weekday));
  }
  $("scheduleEnabled").checked = schedule ? Boolean(schedule.enabled) : true;
  $("deleteSchedule").classList.toggle("hidden", !schedule);
  $("runScheduleNow").classList.toggle("hidden", !schedule);
  $("runScheduleNow").disabled = !state.backendHealthy;
  state.scheduleSandboxRoots = [...(schedule?.sandbox_roots || [])];
  renderProjectSandboxRoots("schedule");
  syncScheduleFrequencyFields();
  $("scheduleDialog").showModal();
  $("scheduleName").focus();
}

function closeScheduleDialog() {
  $("scheduleDialog").close();
  $("scheduleForm").reset();
  state.editingSchedule = null;
  state.scheduleSandboxRoots = [];
}

function syncScheduleFrequencyFields() {
  const frequency = $("scheduleFrequency").value;
  $("scheduleIntervalField").classList.toggle("hidden", frequency !== "interval");
  $("scheduleTimeField").classList.toggle("hidden", frequency === "interval");
  $("scheduleWeekdayField").classList.toggle("hidden", frequency !== "weekly");
}

// scheduleRecurrencePayload 只負責把表單整理成後端契約；真正的週期驗證仍在後端。
function scheduleRecurrencePayload() {
  const frequency = $("scheduleFrequency").value;
  if (frequency === "interval") {
    const minutes = Math.trunc(Number($("scheduleIntervalMinutes").value));
    if (!Number.isFinite(minutes) || minutes < 1 || minutes > 10080) {
      throw new Error(translate("間隔必須介於 1 到 10080 分鐘"));
    }
    return { frequency, interval_minutes: minutes };
  }
  const timeOfDay = $("scheduleTimeOfDay").value;
  if (!/^\d{1,2}:\d{2}$/.test(timeOfDay)) throw new Error(translate("請設定執行時間"));
  if (frequency === "daily") return { frequency, time_of_day: timeOfDay };
  const weekdays = [...document.querySelectorAll("#scheduleWeekdayField input[data-weekday]")]
    .filter((input) => input.checked)
    .map((input) => Number(input.dataset.weekday));
  if (weekdays.length === 0) throw new Error(translate("請至少選擇一天"));
  return { frequency, time_of_day: timeOfDay, weekdays };
}

async function saveSchedule(event) {
  event.preventDefault();
  if (!state.workspace || state.scheduleSaving) return;
  const name = $("scheduleName").value.trim();
  const prompt = $("schedulePrompt").value.trim();
  if (!name || !prompt) {
    toast(translate("排程名稱與交辦內容都要填寫"));
    return;
  }
  let recurrence;
  try {
    recurrence = scheduleRecurrencePayload();
  } catch (error) {
    toast(error.message);
    return;
  }
  const payload = {
    name,
    prompt,
    enabled: $("scheduleEnabled").checked,
    recurrence,
    sandbox_roots: [...state.scheduleSandboxRoots],
  };
  const schedule = state.editingSchedule;
  state.scheduleSaving = true;
  $("saveSchedule").disabled = true;
  try {
    if (schedule) {
      await request(`/api/v1/schedules/${encodeURIComponent(schedule.id)}`, { method: "PATCH", body: JSON.stringify(payload) });
    } else {
      await request("/api/v1/schedules", {
        method: "POST",
        body: JSON.stringify({ ...payload, workspace_id: state.workspace.id }),
      });
    }
    closeScheduleDialog();
    await loadSchedules();
  } catch (error) {
    toast(error.message);
  } finally {
    state.scheduleSaving = false;
    $("saveSchedule").disabled = false;
  }
}

async function deleteScheduleFromDialog() {
  const schedule = state.editingSchedule;
  if (!schedule || !(await confirmAction(`確定刪除排程「${schedule.name}」？已建立的對話會保留。`))) return;
  $("deleteSchedule").disabled = true;
  try {
    await request(`/api/v1/schedules/${encodeURIComponent(schedule.id)}`, { method: "DELETE" });
    closeScheduleDialog();
    await loadSchedules();
    toast(`已刪除排程「${schedule.name}」`);
  } catch (error) {
    toast(error.message);
  } finally {
    $("deleteSchedule").disabled = false;
  }
}

// 立即執行一次：建立新的對話並開始 Run，排程原本的下一次時間不變。
async function runScheduleNow(schedule) {
  if (!schedule) return;
  try {
    const run = await request(`/api/v1/schedules/${encodeURIComponent(schedule.id)}/run`, { method: "POST", body: "{}" });
    if ($("scheduleDialog").open) closeScheduleDialog();
    await Promise.all([loadSessions(), loadSchedules()]);
    const session = state.sessions.find((item) => item.id === run?.session_id);
    if (session) await selectSession(session);
    toast(`已依排程「${schedule.name}」建立新對話`);
  } catch (error) {
    toast(error.message);
  }
}

function resetProjectForm() {
  $("projectForm").reset();
  state.newProjectSandboxRoots = [];
  renderProjectSandboxRoots("create");
}

function projectSandboxEditor(mode) {
  if (mode === "schedule") {
    return {
      buttonID: "pickScheduleFolders",
      emptyID: "scheduleSandboxEmpty",
      listID: "scheduleSandboxRoots",
      roots: state.scheduleSandboxRoots,
    };
  }
  if (mode === "settings") {
    return {
      buttonID: "pickEditProjectFolders",
      emptyID: "editProjectSandboxEmpty",
      listID: "editProjectSandboxRoots",
      roots: state.editProjectSandboxRoots,
    };
  }
  return {
    buttonID: "pickProjectFolders",
    emptyID: "projectSandboxEmpty",
    listID: "projectSandboxRoots",
    roots: state.newProjectSandboxRoots,
  };
}

function setProjectSandboxRoots(mode, roots) {
  if (mode === "schedule") state.scheduleSandboxRoots = roots;
  else if (mode === "settings") state.editProjectSandboxRoots = roots;
  else state.newProjectSandboxRoots = roots;
}

function addProjectSandboxRoots(mode, values) {
  const roots = new Set(projectSandboxEditor(mode).roots);
  for (const value of values || []) {
    const root = String(value || "").trim();
    if (root) roots.add(root);
  }
  setProjectSandboxRoots(mode, [...roots]);
  renderProjectSandboxRoots(mode);
}

function renderProjectSandboxRoots(mode) {
  const editor = projectSandboxEditor(mode);
  const container = $(editor.listID);
  container.replaceChildren();
  for (const root of editor.roots) {
    const row = document.createElement("div");
    row.className = "sandbox-root-row";
    const path = document.createElement("span");
    path.textContent = root;
    path.title = root;
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "icon-button";
    remove.title = "移除此目錄";
    remove.setAttribute("aria-label", `移除 ${root}`);
    remove.textContent = "×";
    remove.addEventListener("click", () => {
      setProjectSandboxRoots(mode, projectSandboxEditor(mode).roots.filter((value) => value !== root));
      renderProjectSandboxRoots(mode);
    });
    row.append(path, remove);
    container.append(row);
  }
  $(editor.emptyID).classList.toggle("hidden", editor.roots.length > 0);
}

async function pickProjectSandboxRoots(mode) {
  const editor = projectSandboxEditor(mode);
  $(editor.buttonID).disabled = true;
  try {
    const roots = await desktop("folders/pick", { method: "POST", body: "{}" });
    if (roots) addProjectSandboxRoots(mode, roots);
  } catch (error) {
    toast(error.message);
  } finally {
    $(editor.buttonID).disabled = false;
  }
}

function pathsFromDrop(dataTransfer) {
  const candidates = [];
  for (const format of ["text/uri-list", "text/plain"]) {
    const value = dataTransfer.getData(format);
    for (const line of value.split(/\r?\n/)) {
      const candidate = droppedPath(line);
      if (candidate) candidates.push(candidate);
    }
  }
  for (const file of dataTransfer.files || []) {
    // Desktop WebView／Electron 類型的容器可能提供非標準 path；一般 Browser 通常會刻意隱藏。
    const candidate = droppedPath(file.path || "");
    if (candidate) candidates.push(candidate);
  }
  return [...new Set(candidates)];
}

function droppedPath(value) {
  value = String(value || "").trim();
  if (!value || value.startsWith("#")) return "";
  if (value.startsWith("file://")) {
    try {
      const url = new URL(value);
      if (url.protocol !== "file:") return "";
      let path = decodeURIComponent(url.pathname);
      if (/^\/[A-Za-z]:[\\/]/.test(path)) path = path.slice(1);
      return path;
    } catch (_) {
      return "";
    }
  }
  if (value.startsWith("/") || /^[A-Za-z]:[\\/]/.test(value) || value.startsWith("\\\\")) return value;
  return "";
}

function handleProjectFolderDrag(event, mode) {
  event.preventDefault();
  const button = $(projectSandboxEditor(mode).buttonID);
  if (button.disabled) return;
  event.dataTransfer.dropEffect = "copy";
  button.classList.add("drag-over");
}

async function handleProjectFolderDrop(event, mode) {
  event.preventDefault();
  $(projectSandboxEditor(mode).buttonID).classList.remove("drag-over");
  const roots = pathsFromDrop(event.dataTransfer);
  try {
    const nativeRoots = await desktop("folders/dropped", { method: "POST", body: "{}" });
    if (nativeRoots) roots.push(...nativeRoots);
  } catch (error) {
    if (roots.length === 0) {
      toast(error.message);
      return;
    }
  }
  if (roots.length === 0) {
    toast("系統未提供拖入目錄的絕對路徑，請改用目錄選擇器");
    return;
  }
  addProjectSandboxRoots(mode, [...new Set(roots)]);
}

async function createWorkspace(event) {
  event.preventDefault();
  try {
    const workspace = await request("/api/v1/workspaces", {
      method: "POST",
      body: JSON.stringify({
        name: $("newWorkspaceName").value.trim(),
        description: $("newWorkspaceDescription").value.trim(),
        provider_ids: [$("newWorkspaceProvider").value],
        default_provider_id: $("newWorkspaceProvider").value,
        model: $("newWorkspaceModel").value.trim(),
      }),
    });
    $("workspaceDialog").close();
    $("workspaceForm").reset();
    state.workspaces = await request("/api/v1/workspaces");
    renderWorkspaceOptions();
    await switchWorkspace(workspace.id);
    syncWorkspaceSelectorValues(workspace.id);
  } catch (error) {
    toast(error.message);
  }
}

async function saveWorkspaceSettings(event) {
  event.preventDefault();
  if (!state.workspace) return;
  $("saveWorkspaceSettings").disabled = true;
  $("workspaceSettingsState").textContent = "儲存中…";
  try {
    const updated = await request(`/api/v1/workspaces/${encodeURIComponent(state.workspace.id)}`, {
      method: "PATCH",
      reconnects: 3,
      body: JSON.stringify({
        name: $("settingWorkspaceName").value.trim(),
        description: $("settingWorkspaceDescription").value.trim(),
        provider_ids: [$("settingWorkspaceProvider").value],
        default_provider_id: $("settingWorkspaceProvider").value,
        model: $("settingWorkspaceModel").value.trim(),
        instructions: $("settingWorkspaceInstructions").value,
      }),
    });
    state.workspace = updated;
    state.workspaces = state.workspaces.map((item) => item.id === updated.id ? updated : item);
    renderWorkspaceOptions();
    syncWorkspaceSelectorValues(updated.id);
    syncWorkspaceSettings();
    $("workspaceSettingsState").textContent = "已儲存";
  } catch (error) {
    $("workspaceSettingsState").textContent = "儲存失敗";
    toast(error.message);
  } finally {
    $("saveWorkspaceSettings").disabled = false;
  }
}

// saveToolSettings 只送工具相關欄位。更新 API 的欄位是指標，省略即保留原值，
// 因此兩個設定頁各自儲存不會互相覆蓋；service_name 是必填，帶上目前值。
async function saveToolSettings(event) {
  event.preventDefault();
  const serviceName = ($("settingServiceName").value || state.serviceSettings?.service_name || "").trim();
  if (!serviceName) {
    $("toolSettingsState").textContent = translate("儲存失敗");
    toast(translate("尚未載入服務設定，請稍後再試"));
    return;
  }
  $("saveToolSettings").disabled = true;
  $("toolSettingsState").textContent = translate("儲存中…");
  try {
    const updated = await request("/api/v1/admin/service-settings", {
      method: "PUT",
      body: JSON.stringify({
        service_name: serviceName,
        extended_tools: $("settingExtendedTools").checked,
        tool_call_mode: $("settingInstructionToolCalls").checked ? "instruction" : "native",
        tool_retrieval: $("settingToolRetrieval").checked,
        http_fetch_enabled: $("settingHTTPFetchEnabled").checked,
        http_fetch_allow_private_networks: $("settingHTTPFetchPrivateNetworks").checked,
      }),
    });
    applyServiceSettings(updated);
    $("toolSettingsState").textContent = translate("已儲存");
  } catch (error) {
    $("toolSettingsState").textContent = translate("儲存失敗");
    toast(error.message);
  } finally {
    $("saveToolSettings").disabled = false;
  }
}

async function saveServiceSettings(event) {
  event.preventDefault();
  const serviceName = $("settingServiceName").value.trim();
  const serviceNameIsDefault = serviceName === localizedDefaultServiceName()
    || (state.serviceSettings?.service_name_is_default && knownDefaultServiceNames.has(serviceName));
  if (state.serviceSettings) state.serviceSettings.service_name_is_default = serviceNameIsDefault;
  const uiLanguage = normalizedUILanguage($("settingUILanguage").value);
  const wallClockMinutes = $("settingMaxWallClockMinutes").valueAsNumber;
  const maxTokens = $("settingMaxTokens").valueAsNumber;
  const maxToolCalls = $("settingMaxToolCalls").valueAsNumber;
  if (!Number.isInteger(wallClockMinutes) || wallClockMinutes < 1 || wallClockMinutes > 1440
      || !Number.isInteger(maxTokens) || maxTokens < 0
      || !Number.isInteger(maxToolCalls) || maxToolCalls < 0 || maxToolCalls > 10000) {
    $("serviceSettingsState").textContent = "設定值無效";
    toast("請確認時間、Token 與工具呼叫上限的數值範圍");
    return;
  }
  $("saveServiceSettings").disabled = true;
  $("serviceSettingsState").textContent = "儲存中…";
  try {
    const updated = await request("/api/v1/admin/service-settings", {
      method: "PUT",
      body: JSON.stringify({
        service_name: serviceName,
        ui_language: uiLanguage,
        notifications_enabled: $("settingNotificationsEnabled").checked,
        max_wall_clock_seconds: wallClockMinutes * 60,
        max_tokens: maxTokens,
        max_tool_calls: maxToolCalls,
      }),
    });
    applyServiceSettings(updated);
    $("serviceSettingsState").textContent = "已儲存";
  } catch (error) {
    $("serviceSettingsState").textContent = "儲存失敗";
    toast(error.message);
  } finally {
    $("saveServiceSettings").disabled = false;
  }
}

function manageProject(project) {
  state.editingProject = project;
  state.editProjectSandboxRoots = [...(project.sandbox_roots || [])];
  $("editProjectName").value = project.name || "";
  $("editProjectDescription").value = project.description || "";
  $("editProjectInstructions").value = project.instructions || "";
  $("projectSettingsState").textContent = "";
  renderProjectSandboxRoots("settings");
  $("projectSettingsDialog").showModal();
  $("editProjectName").focus();
}

function closeProjectSettings() {
  $("projectSettingsDialog").close();
  $("projectSettingsForm").reset();
  state.editingProject = null;
  state.editProjectSandboxRoots = [];
}

async function saveProjectSettings(event) {
  event.preventDefault();
  if (!state.editingProject) return;
  $("saveProjectSettings").disabled = true;
  $("projectSettingsState").textContent = "儲存中…";
  try {
    const updated = await request(`/api/v1/projects/${encodeURIComponent(state.editingProject.id)}`, {
      method: "PATCH",
      reconnects: 3,
      body: JSON.stringify({
        name: $("editProjectName").value.trim(),
        description: $("editProjectDescription").value.trim(),
        instructions: $("editProjectInstructions").value,
        sandbox_roots: [...state.editProjectSandboxRoots],
      }),
    });
    state.projects = state.projects.map((project) => project.id === updated.id ? updated : project);
    renderProjectOptions();
    renderNavigation();
    closeProjectSettings();
  } catch (error) {
    $("projectSettingsState").textContent = "儲存失敗";
    toast(error.message);
  } finally {
    $("saveProjectSettings").disabled = false;
  }
}

async function deleteProjectSettings() {
  const project = state.editingProject;
  if (!project) return;
  $("deleteProjectSettings").disabled = true;
  try {
    await deleteProject(project, { closeSettings: true });
  } finally {
    $("deleteProjectSettings").disabled = false;
  }
}

async function deleteProject(project, { closeSettings = false } = {}) {
  if (!project || !(await confirmAction(`確定刪除專案「${project.name}」？含有對話時後端會拒絕。`))) return false;
  try {
    await request(`/api/v1/projects/${encodeURIComponent(project.id)}`, { method: "DELETE" });
    if (closeSettings && state.editingProject?.id === project.id) closeProjectSettings();
    await loadProjects();
    toast(`已刪除專案「${project.name}」`);
    return true;
  } catch (error) {
    toast(error.message);
    return false;
  }
}

async function saveSessionSettings(event) {
  event.preventDefault();
  if (!state.session) return;
  $("saveSessionSettings").disabled = true;
  $("settingsState").textContent = "儲存中…";
  try {
    const updated = await request(`/api/v1/sessions/${encodeURIComponent(state.session.id)}`, {
      method: "PATCH",
      reconnects: 3,
      body: JSON.stringify({
        title: $("settingTitle").value.trim(),
        project_id: $("settingProject").value,
        provider_id: $("settingProvider").value.trim(),
        model: $("settingModel").value.trim(),
        permission_profile: $("settingPermission").value,
        memory_scope: $("settingMemoryScope").value.trim(),
        pinned: $("settingPinned").checked,
      }),
    });
    state.session = updated;
    state.sessions = state.sessions.map((item) => item.id === updated.id ? updated : item);
    syncSessionUI();
    renderNavigation();
    $("settingsState").textContent = "已儲存";
    await loadTools();
  } catch (error) {
    $("settingsState").textContent = "儲存失敗";
    if (sessionBusyError(error)) {
      const restored = await resyncActiveRun(state.session?.id);
      toast(restored
        ? translate("這個對話還有進行中的工作，完成或取消後才能調整設定")
        : translate("後端仍在收尾上一個工作，稍待幾秒再試一次"));
    } else {
      toast(error.message);
    }
  } finally {
    $("saveSessionSettings").disabled = false;
  }
}

function openManagement(panel = "overview") {
  document.querySelector(".shell").classList.add("management-open");
  $("managementPanel").classList.remove("hidden");
  activatePanel(panel);
}

function closeManagement() {
  $("managementPanel").classList.add("hidden");
  document.querySelector(".shell").classList.remove("management-open");
}

async function activatePanel(name) {
  for (const button of document.querySelectorAll(".panel-tab")) {
    const selected = button.dataset.panel === name;
    button.classList.toggle("active", selected);
    button.setAttribute("aria-selected", String(selected));
  }
  for (const value of ["overview", "workspace", "systemTools", "providers", "mcp", "reverseProxy", "toolSettings", "tools", "permissions", "about", "audit"]) $(`${value}Panel`).classList.toggle("hidden", value !== name);
  if (name === "workspace") {
    renderWorkspaceOptions();
    syncWorkspaceSettings();
  }
  if (name === "systemTools") await loadDiagnostics();
  if (name === "providers") await loadProviderSettings();
  if (name === "mcp") await loadMCPSettings();
  if (name === "reverseProxy") await loadReverseProxyStatus({ hydrate: !state.reverseProxyHydrated });
  if (name === "tools") await loadTools();
  if (name === "permissions") await loadPermissionCenter();
  if (name === "about") {
    renderAboutVersionInfo();
    renderUpdateStatus();
  }
  if (name === "audit") await loadAudit(true);
}

async function loadDiagnostics() {
  try {
    const value = await request("/api/v1/admin/diagnostics");
    applyServiceSettings(value.config || {});
    state.permissions = value.config?.permissions || state.permissions;
    renderPermissionOptions();
    const cards = [
      ["版本", value.status?.version || "—"],
      ["Workspaces", value.workspace_count ?? 0],
      ["Projects", value.project_count ?? 0],
      ["Sessions", value.session_count ?? 0],
      ["執行中", value.runs?.running ?? 0],
      ["工具", value.tool_count ?? 0],
      ["失敗 Runs", value.runs?.failed ?? 0],
    ];
    const container = $("diagnosticCards");
    container.replaceChildren();
    for (const [label, data] of cards) {
      const card = document.createElement("div");
      const strong = document.createElement("strong");
      const small = document.createElement("small");
      strong.textContent = data;
      small.textContent = label;
      card.append(strong, small);
      container.append(card);
    }
    const providers = value.config?.providers || [];
    const activeProvider = providers.find((item) => item.id === state.workspace?.default_provider_id)
      || providers.find((item) => item.id === value.config?.default_provider_id)
      || providers[0]
      || {};
    const summary = $("providerSummary");
    summary.replaceChildren();
    appendSummary(summary, "目前 Workspace", state.workspace?.name || "—");
    appendSummary(summary, "Provider", providerDisplayName(activeProvider) || "—");
    appendSummary(summary, "協定", activeProvider.protocol || "—");
    appendSummary(summary, "Endpoint", activeProvider.endpoint || "—");
    appendSummary(summary, "預設模型", activeProvider.default_model || "—");
    appendSummary(summary, "Streaming", activeProvider.streaming ? "啟用" : "停用");
    appendSummary(summary, "API key", activeProvider.has_api_key ? "已設定" : "未設定");
    $("diagnosticConfig").textContent = JSON.stringify(value.config || {}, null, 2);
  } catch (error) {
    toast(error.message);
  }
}

function appendSummary(container, term, description) {
  const dt = document.createElement("dt");
  const dd = document.createElement("dd");
  dt.textContent = term;
  dd.textContent = description;
  container.append(dt, dd);
}

async function loadNotifications() {
  if (!notificationCenterEnabled()) {
    state.notifications = [];
    renderNotifications();
    return;
  }
  try {
    state.notifications = await request("/api/v1/notifications?limit=80");
    renderNotifications();
  } catch (_) {
    // 通知中心不可用不應阻斷主要對話介面。
  }
}

function closeNotificationPopover() {
  $("notificationPopover")?.classList.add("hidden");
  $("notificationButton")?.setAttribute("aria-expanded", "false");
}

function toggleNotificationPopover() {
  const popover = $("notificationPopover");
  if (!popover || !notificationCenterEnabled()) return;
  const open = popover.classList.toggle("hidden");
  $("notificationButton").setAttribute("aria-expanded", String(!open));
  if (!open) void loadNotifications();
}

function renderNotifications() {
  const values = notificationCenterEnabled() && Array.isArray(state.notifications) ? state.notifications : [];
  const unread = values.filter((item) => !item.read).length;
  const badge = $("notificationBadge");
  badge.textContent = unread > 99 ? "99+" : String(unread);
  badge.classList.toggle("hidden", unread === 0);
  const list = $("notificationList");
  if (!list) return;
  list.replaceChildren();
  if (!values.length) {
    const empty = document.createElement("p");
    empty.className = "quiet";
    empty.textContent = "目前沒有通知。";
    list.append(empty);
    return;
  }
  for (const item of values) {
    const article = document.createElement("article");
    article.className = "notification-item" + (item.read ? " is-read" : "");
    const head = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = item.title || "通知";
    const time = document.createElement("time");
    time.textContent = item.created_at ? new Date(item.created_at).toLocaleString() : "";
    head.append(title, time);
    const message = document.createElement("p");
    message.textContent = item.message || "";
    article.append(head, message);
    if (!item.read) {
      const mark = document.createElement("button");
      mark.className = "small ghost";
      mark.type = "button";
      mark.textContent = "標為已讀";
      mark.addEventListener("click", async () => {
        try { await request("/api/v1/notifications/" + encodeURIComponent(item.id) + "/read", { method: "POST", body: "{}" }); await loadNotifications(); } catch (error) { toast(error.message); }
      });
      article.append(mark);
    }
		if (item.type === "system.update_available" && item.metadata?.release_url) {
			const release = document.createElement("button");
			release.className = "small ghost";
			release.type = "button";
			release.textContent = "查看 Release";
			release.addEventListener("click", () => void openResource({ kind: "url", target: item.metadata.release_url }, "open"));
			article.append(release);
		}
    list.append(article);
  }
}

async function loadGlobalSearch() {
  const query = $("globalSearchInput").value.trim();
  if (!query) { $("globalSearchPopover").classList.add("hidden"); return; }
  try {
    state.globalSearchResults = await request("/api/v1/search?q=" + encodeURIComponent(query) + "&limit=30");
    renderGlobalSearchResults();
  } catch (error) { toast(error.message); }
}

function renderGlobalSearchResults() {
  const popover = $("globalSearchPopover");
  const list = $("globalSearchResults");
  list.replaceChildren();
  popover.classList.remove("hidden");
  const values = Array.isArray(state.globalSearchResults) ? state.globalSearchResults : [];
  if (!values.length) { const empty = document.createElement("p"); empty.className = "quiet"; empty.textContent = "找不到符合的內容。"; list.append(empty); return; }
  for (const item of values) {
    const button = document.createElement("button");
    button.className = "global-search-result";
    button.type = "button";
    const title = document.createElement("strong"); title.textContent = item.title || item.kind;
    const kind = document.createElement("small"); kind.textContent = item.kind;
    const snippet = document.createElement("span"); snippet.textContent = item.snippet || "";
    button.append(title, kind, snippet);
    button.addEventListener("click", () => {
      popover.classList.add("hidden");
      if (item.session_id) void selectSession({ id: item.session_id });
    });
    list.append(button);
  }
}

async function downloadAdminFile(path, filename) {
  try {
    const response = await fetch("/backend" + path);
    if (!response.ok) throw new Error(response.statusText);
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a"); link.href = url; link.download = filename; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) { toast(error.message); }
}

async function loadUpdateStatus() {
	try {
		state.updateStatus = await request("/api/v1/admin/update");
		renderUpdateStatus();
	} catch (_) {
		// 更新檢查不可用時不應影響對話與其他管理功能。
	}
}

function renderUpdateStatus() {
	const status = state.updateStatus;
	const label = $("updateStatus");
	const releaseButton = $("openLatestRelease");
	if (!label || !releaseButton) return;
	renderAboutVersionInfo();
	if (!status?.checked_at) {
		label.textContent = "尚未檢查更新。";
		releaseButton.classList.add("hidden");
		return;
	}
	if (status.check_error) {
		label.textContent = status.check_error + "，稍後可再試。";
		releaseButton.classList.add("hidden");
		return;
	}
	if (status.available) {
		label.textContent = `有新版本 ${status.latest_version}（目前 ${status.current_version}）。`;
		releaseButton.classList.toggle("hidden", !status.release_url);
		return;
	}
	label.textContent = `目前已是最新版本（${status.current_version || "—"}）。`;
	releaseButton.classList.add("hidden");
}

function renderAboutVersionInfo() {
  const summary = $("aboutVersionSummary");
  if (!summary) return;
  const update = state.updateStatus || {};
  const backend = state.backendStatus || {};
  summary.replaceChildren();
  appendSummary(summary, "目前版本", update.current_version || backend.version || state.agent?.version || "—");
  appendSummary(summary, "後端 API", backend.api_version || "—");
  appendSummary(summary, "事件格式", backend.event_schema_version || "—");
  appendSummary(summary, "通知中心", notificationCenterEnabled() ? "已啟用" : "已關閉");
}

async function checkForUpdates() {
	const button = $("checkForUpdates");
	if (button) {
		button.disabled = true;
		button.textContent = "檢查中…";
	}
	try {
		state.updateStatus = await request("/api/v1/admin/update/check", { method: "POST", body: "{}" });
		renderUpdateStatus();
		await loadNotifications();
		toast(state.updateStatus?.available ? `發現新版本 ${state.updateStatus.latest_version}` : "目前已是最新版本");
	} catch (error) {
		toast(error.message);
	} finally {
		if (button) {
			button.disabled = false;
			button.textContent = "檢查更新";
		}
	}
}

async function restoreAdminBackup(file) {
  if (!file || !(await confirmAction("確定還原備份嗎？目前資料會先保留一份備份，還原後必須重新啟動後端。"))) return;
  const form = new FormData(); form.append("file", file);
  try {
    $("adminDataState").textContent = "還原中…";
    const response = await fetch("/backend/api/v1/admin/restore", { method: "POST", body: form });
    const body = await response.json();
    if (!response.ok) throw new Error(body.detail || response.statusText);
    $("adminDataState").textContent = "還原完成，請重新啟動後端";
    toast("備份已還原；請重新啟動後端套用資料");
  } catch (error) { $("adminDataState").textContent = "還原失敗"; toast(error.message); }
}

async function loadPermissionCenter() {
  try {
    const value = await request("/api/v1/admin/permissions");
    appendPermissionSummary(value);
    const list = $("permissionToolList"); list.replaceChildren();
    for (const item of value.tools || []) {
      const article = document.createElement("article"); article.className = "tool-item" + (item.available ? "" : " unavailable");
      const head = document.createElement("div"); const name = document.createElement("strong"); name.textContent = item.name;
      const badge = document.createElement("span"); badge.className = "tool-status"; badge.textContent = item.permission === "elevated" ? "信任權限" : "標準權限"; head.append(name, badge);
      const detail = document.createElement("p"); detail.textContent = (item.read_only ? "唯讀" : "可修改") + (item.available ? " · 目前可用" : " · 目前不可用"); article.append(head, detail); list.append(article);
    }
  } catch (error) { toast(error.message); }
}

function appendPermissionSummary(value) {
  const summary = $("permissionSummary"); summary.replaceChildren();
  const policy = value.policy || {};
  appendSummary(summary, "預設 Profile", policy.default_profile || "default");
  appendSummary(summary, "允許 Client 選擇", policy.allow_client_choice ? "是" : "否");
  appendSummary(summary, "Elevated Profiles", (policy.elevated_profiles || []).join(", ") || "無");
  appendSummary(summary, "待人工核准 Run", value.waiting_approval_runs || 0);
}

async function loadProviderSettings(preferredID = state.selectedProviderSettingsID) {
  try {
    state.providerSettings = await request("/api/v1/admin/provider-settings");
    state.providerSettingsDraft = null;
    const providers = state.providerSettings?.providers || [];
    state.selectedProviderSettingsID = providers.some((item) => item.id === preferredID)
      ? preferredID
      : providers[0]?.id || "";
    renderProviderSettings();
    if (state.selectedProviderSettingsID) await loadProviderModels(state.selectedProviderSettingsID, { notify: false });
  } catch (error) {
    toast(error.message);
  }
}

function newProviderSetting() {
  return {
    id: "",
    display_name: "",
    type: "openai-compatible",
    enabled: true,
    openai_compatible: {
      base_url: "https://api.openai.com/v1",
      has_api_key: false,
      model: "gpt-4o-mini",
      instruction_role: "system",
      disable_streaming: false,
      stream_include_usage: true,
      omit_tool_choice: false,
      max_attempts: 3,
      timeout_seconds: 1800,
      connect_timeout_seconds: 20,
      response_header_timeout_seconds: 120,
      context_window: 0,
      max_output_tokens: 0,
    },
  };
}

function renderProviderSettings() {
  const providers = state.providerSettings?.providers || [];
  const list = $("providerSettingsList");
  list.replaceChildren();
  for (const provider of providers) {
    const isCodex = provider.type === "openai-codex-responses";
    const settings = isCodex ? provider.openai_codex_responses || {} : provider.openai_compatible || {};
    const button = document.createElement("button");
    button.type = "button";
    const enabled = provider.enabled !== false;
    button.className = `provider-setting-card ${enabled ? "" : "provider-disabled"} ${!state.providerSettingsDraft && provider.id === state.selectedProviderSettingsID ? "active" : ""}`;
    const title = document.createElement("strong");
    title.textContent = provider.display_name || provider.id;
    const detail = document.createElement("small");
    detail.textContent = `${settings.model || "未設定模型"} · ${isCodex ? "OpenAI Codex Responses" : settings.base_url || "未設定 URL"}`;
    button.append(title);
    if (provider.id === state.providerSettings.default_provider_id) {
      const badge = document.createElement("span");
      badge.className = "provider-default-badge";
      badge.textContent = "預設";
      button.append(badge);
    } else if (!enabled) {
      const badge = document.createElement("span");
      badge.className = "provider-disabled-badge";
      badge.textContent = "已停用";
      button.append(badge);
    }
    button.append(detail);
    button.addEventListener("click", () => {
      state.providerSettingsDraft = null;
      state.selectedProviderSettingsID = provider.id;
      renderProviderSettings();
      void loadProviderModels(provider.id, { notify: false });
    });
    list.append(button);
  }
  if (providers.length === 0) {
    const empty = document.createElement("p");
    empty.className = "quiet";
    empty.textContent = "尚未設定 Provider。";
    list.append(empty);
  }

  const selected = state.providerSettingsDraft
    || providers.find((item) => item.id === state.selectedProviderSettingsID)
    || null;
  $("providerSettingsForm").classList.toggle("hidden", !selected);
  $("noProviderSettings").classList.toggle("hidden", Boolean(selected));
  if (!selected) return;
  const isNew = selected === state.providerSettingsDraft;
  const providerType = selected.type === "openai-codex-responses" ? "openai-codex-responses" : "openai-compatible";
  const settings = providerType === "openai-codex-responses"
    ? selected.openai_codex_responses || {}
    : selected.openai_compatible || {};
  $("providerSettingsEditorTitle").textContent = isNew ? "新增 Provider" : `編輯 ${selected.display_name || selected.id}`;
  $("providerSettingsState").textContent = "";
  $("providerTestState").textContent = "";
  $("providerSettingID").value = selected.id || "";
  $("providerSettingID").disabled = !isNew;
  $("providerSettingDisplayName").value = selected.display_name || selected.id || "";
  $("providerSettingType").value = providerType;
  const isDefault = selected.id === state.providerSettings?.default_provider_id;
  $("providerSettingEnabled").checked = selected.enabled !== false;
  $("providerSettingEnabled").disabled = !isNew && isDefault;
  $("providerSettingEnabledLabel").classList.toggle("switch-disabled", !isNew && isDefault);
  $("providerSettingEnabledLabel").title = !isNew && isDefault ? "請先將其他 Provider 設為系統預設" : "";
  $("providerSettingBaseURL").value = settings.base_url || "";
  $("providerSettingAPIKey").value = "";
  $("providerSettingAPIKey").placeholder = settings.has_api_key ? "已設定；留空表示保留" : "尚未設定";
  $("providerSettingAPIKeyState").textContent = settings.has_api_key ? "API Key 已安全儲存，畫面不會顯示明文。" : "目前未設定 API Key；本機服務可保持空白。";
  $("providerSettingClearKeyRow").classList.toggle("hidden", !settings.has_api_key || isNew);
  $("providerSettingClearKey").checked = false;
  $("providerSettingModel").value = settings.model || "";
	$("providerSettingModel").dataset.configuredModel = settings.model || "";
  $("refreshProviderModels").disabled = isNew;
  renderProviderModelOptions(isNew ? "" : selected.id);
  $("providerSettingInstructionRole").value = settings.instruction_role || "system";
  $("providerSettingMaxAttempts").value = settings.max_attempts || 3;
  $("providerSettingDefault").checked = isDefault;
  $("providerSettingDisableStreaming").checked = Boolean(settings.disable_streaming);
  $("providerSettingStreamUsage").checked = Boolean(settings.stream_include_usage);
  $("providerSettingOmitToolChoice").checked = Boolean(settings.omit_tool_choice);
  $("providerSettingTimeout").value = settings.timeout_seconds || 1800;
  $("providerSettingConnectTimeout").value = settings.connect_timeout_seconds || 20;
  $("providerSettingHeaderTimeout").value = settings.response_header_timeout_seconds || 120;
  $("providerSettingContextWindow").value = settings.context_window || 0;
  $("providerSettingMaxOutputTokens").value = settings.max_output_tokens || 0;
  $("deleteProviderSetting").classList.toggle("hidden", isNew);
	renderProviderTypeFields(providerType, settings, isNew, selected.id || "");
}

function renderProviderTypeFields(providerType, settings, isNew, providerID) {
	const isCodex = providerType === "openai-codex-responses";
	$("providerAPIKeyFields").classList.toggle("hidden", isCodex);
	$("providerOAuthFields").classList.toggle("hidden", !isCodex);
	$("providerSettingBaseURLField").classList.toggle("hidden", isCodex);
	$("providerSettingBaseURL").required = !isCodex;
	$("providerSettingInstructionRoleField").classList.toggle("hidden", isCodex);
	$("providerSettingDisableStreamingField").classList.toggle("hidden", isCodex);
	$("providerSettingStreamUsageField").classList.toggle("hidden", isCodex);
	$("providerSettingOmitToolChoiceField").classList.toggle("hidden", isCodex);
	if (isCodex) $("providerSettingDisableStreaming").checked = false;
	$("connectProviderOAuth").disabled = isNew;
	const connected = Boolean(settings.has_oauth_token);
	$("disconnectProviderOAuth").classList.toggle("hidden", !connected || isNew);
	$("providerOAuthState").textContent = connected ? "已連線" : isNew ? "請先儲存 Provider" : "尚未連線";
	if (isCodex && !isNew && providerID) void loadProviderOAuthStatus(providerID, { quiet: true });
}

function renderProviderModelOptions(providerID, preferredModel = "") {
  // 所有模型選單只顯示 Provider /models 正式回傳的目錄；空目錄或讀取
  // 失敗時統一顯示「-」，不以設定值或單次測試結果冒充模型目錄。
  const models = [...new Set((state.providerModelLists[providerID] || [])
    .map((model) => String(model).trim()).filter(Boolean))];
  const input = $("providerSettingModel");
  const catalog = $("providerSettingModelCatalog");
  catalog.replaceChildren();
  if (models.length > 0) {
    const visibleModel = input.value.trim();
    const configuredModel = String(input.dataset.configuredModel || "").trim();
    const currentModel = visibleModel && visibleModel !== "-" ? visibleModel : configuredModel;
    const testedModel = String(preferredModel || "").trim();
    const selectedModel = models.includes(testedModel)
      ? testedModel
      : models.includes(currentModel) ? currentModel : models[0];
    for (const model of models) {
      const option = document.createElement("option");
      option.value = model;
      option.textContent = model;
      catalog.append(option);
    }
    const custom = document.createElement("option");
    custom.value = "__custom_model__";
    custom.textContent = "手動輸入其他模型…";
    catalog.append(custom);
    input.value = selectedModel;
	input.dataset.configuredModel = selectedModel;
	input.disabled = false;
    catalog.value = selectedModel;
    input.classList.add("hidden");
    catalog.classList.remove("hidden");
  } else {
    catalog.classList.add("hidden");
    input.classList.remove("hidden");
	const configuredModel = input.value.trim() && input.value.trim() !== "-"
	  ? input.value.trim()
	  : String(input.dataset.configuredModel || "").trim();
	input.dataset.configuredModel = configuredModel;
	input.value = "-";
	input.disabled = true;
  }
  if (!providerID) {
    $("providerModelState").textContent = "儲存 Provider 後會自動更新模型列表。";
  } else if (state.providerModelLists[providerID]) {
    $("providerModelState").textContent = models.length > 0
      ? `已載入 ${models.length} 個模型，可從輸入欄位選取。`
	  : "Provider 未回傳模型列表。";
  } else {
	$("providerModelState").textContent = "模型列表尚未載入。";
  }
}

async function loadProviderModels(providerID, { notify = true } = {}) {
  providerID = String(providerID || "").trim();
  if (!providerID) return null;
  const isVisible = !state.providerSettingsDraft && state.selectedProviderSettingsID === providerID;
  if (isVisible) {
    $("refreshProviderModels").disabled = true;
    $("providerModelState").textContent = "正在更新模型列表…";
  }
  try {
    const value = await request(`/api/v1/admin/provider-settings/${encodeURIComponent(providerID)}/models`);
    state.providerModelLists[providerID] = Array.isArray(value.models) ? value.models : [];
    invalidateProviderContextCapabilities(providerID);
    delete state.providerModelErrors[providerID];
    if (!state.providerSettingsDraft && state.selectedProviderSettingsID === providerID) renderProviderModelOptions(providerID);
    if (notify) toast(`已更新 ${providerID} 的模型列表`);
    return value;
  } catch (error) {
    state.providerModelErrors[providerID] = error.message;
    if (!state.providerSettingsDraft && state.selectedProviderSettingsID === providerID) {
      const retained = state.providerModelLists[providerID]?.length || 0;
      $("providerModelState").textContent = retained > 0
        ? `更新失敗，保留既有 ${retained} 個模型；仍可手動輸入。`
        : "無法讀取模型列表。";
	  renderProviderModelOptions(providerID);
    }
    if (notify) toast(error.message);
    return null;
  } finally {
    if (!state.providerSettingsDraft && state.selectedProviderSettingsID === providerID) $("refreshProviderModels").disabled = false;
  }
}

function invalidateProviderContextCapabilities(providerID) {
  providerID = String(providerID || "").trim();
  for (const key of Object.keys(state.contextCapabilityCache)) {
    if (key.startsWith(`${providerID}\u0000`)) delete state.contextCapabilityCache[key];
  }
  if (activeContextIdentity().providerID !== providerID) return;
  state.contextCapabilities = null;
  if (state.session) void loadContextCapabilities(state.session.id);
}

function providerSettingFormValue() {
  const providerType = $("providerSettingType").value === "openai-codex-responses"
    ? "openai-codex-responses"
    : "openai-compatible";
  const model = $("providerSettingModel").value.trim() === "-"
    ? String($("providerSettingModel").dataset.configuredModel || "").trim()
    : $("providerSettingModel").value.trim();
  const common = {
    model,
    max_attempts: Number($("providerSettingMaxAttempts").value),
    timeout_seconds: Number($("providerSettingTimeout").value),
    connect_timeout_seconds: Number($("providerSettingConnectTimeout").value),
    response_header_timeout_seconds: Number($("providerSettingHeaderTimeout").value),
    context_window: Number($("providerSettingContextWindow").value) || 0,
    max_output_tokens: Number($("providerSettingMaxOutputTokens").value) || 0,
  };
  const provider = {
    id: $("providerSettingID").value.trim(),
    display_name: $("providerSettingDisplayName").value.trim(),
    type: providerType,
    enabled: $("providerSettingDefault").checked || $("providerSettingEnabled").checked,
  };
  if (providerType === "openai-codex-responses") {
    provider.openai_codex_responses = common;
  } else {
    provider.openai_compatible = {
      base_url: $("providerSettingBaseURL").value.trim(),
      ...common,
      instruction_role: $("providerSettingInstructionRole").value,
      disable_streaming: $("providerSettingDisableStreaming").checked,
      stream_include_usage: $("providerSettingStreamUsage").checked,
      omit_tool_choice: $("providerSettingOmitToolChoice").checked,
    };
    const apiKey = $("providerSettingAPIKey").value.trim();
    if (apiKey) provider.openai_compatible.api_key = apiKey;
    if ($("providerSettingClearKey").checked) provider.openai_compatible.api_key = "";
  }
  return provider;
}

function providerSettingsPayload(providers, defaultProviderID) {
  return {
    default_provider_id: defaultProviderID,
    providers: providers.map((provider) => {
      const value = JSON.parse(JSON.stringify(provider));
      if (value.openai_compatible) delete value.openai_compatible.has_api_key;
      if (value.openai_codex_responses) delete value.openai_codex_responses.has_oauth_token;
      return value;
    }),
  };
}

async function persistProviderSetting({ showToast = true, refreshModels = true } = {}) {
  if (!state.providerSettings) return;
  if (!$("providerSettingsForm").reportValidity()) return null;
  const provider = providerSettingFormValue();
  const isNew = Boolean(state.providerSettingsDraft);
  const settings = provider.type === "openai-codex-responses"
    ? provider.openai_codex_responses
    : provider.openai_compatible;
  if (!provider.id || !settings?.model
    || (provider.type === "openai-compatible" && !provider.openai_compatible.base_url)) return;
  if (isNew && state.providerSettings.providers.some((item) => item.id === provider.id)) {
    toast(`Provider ID ${provider.id} 已存在`);
    return null;
  }
  const providers = isNew
    ? [...state.providerSettings.providers, provider]
    : state.providerSettings.providers.map((item) => item.id === state.selectedProviderSettingsID ? provider : item);
  const defaultProviderID = $("providerSettingDefault").checked
    ? provider.id
    : state.providerSettings.default_provider_id;
  $("saveProviderSetting").disabled = true;
  $("testProviderSetting").disabled = true;
  $("providerSettingsState").textContent = "儲存中…";
  try {
    state.providerSettings = await request("/api/v1/admin/provider-settings", {
      method: "PUT",
      body: JSON.stringify(providerSettingsPayload(providers, defaultProviderID)),
    });
    state.providerSettingsDraft = null;
    state.selectedProviderSettingsID = provider.id;
    state.contextCapabilities = null;
    state.contextCapabilityCache = {};
    delete state.providerModelLists[provider.id];
    delete state.providerModelErrors[provider.id];
    delete state.providerTestedModels[provider.id];
    await refreshProviderCatalog();
    renderProviderSettings();
    let models = null;
    if (refreshModels && provider.enabled) {
      $("providerSettingsState").textContent = "已套用，更新模型列表中…";
      models = await loadProviderModels(provider.id, { notify: false });
    }
    $("providerSettingsState").textContent = !provider.enabled
      ? "已停用"
      : models
      ? `已套用 · ${models.models.length} 個模型`
      : refreshModels ? "已套用 · 模型列表需手動更新" : "已套用";
    if (showToast) {
      toast(!provider.enabled
        ? `Provider ${provider.id} 已停用並從使用選單移除`
        : models
        ? "Provider 設定已儲存，模型列表已更新"
        : "Provider 設定已儲存；模型列表需手動更新");
    }
    return provider;
  } catch (error) {
    $("providerSettingsState").textContent = "儲存失敗";
    toast(error.message);
    return null;
  } finally {
    $("saveProviderSetting").disabled = false;
    $("testProviderSetting").disabled = false;
  }
}

async function saveProviderSetting(event) {
  event.preventDefault();
  await persistProviderSetting();
}

async function testProviderSetting() {
  const provider = await persistProviderSetting({ showToast: false, refreshModels: false });
  if (!provider) return;
  $("testProviderSetting").disabled = true;
  $("saveProviderSetting").disabled = true;
  $("providerTestState").textContent = "測試中…";
  try {
    const result = await request(`/api/v1/admin/provider-settings/${encodeURIComponent(provider.id)}/test`, {
      method: "POST",
      reconnects: 3,
    });
    state.providerModelLists[provider.id] = [...new Set((Array.isArray(result.models) ? result.models : [])
      .map((model) => String(model).trim()).filter(Boolean))];
    state.providerTestedModels[provider.id] = result.model ? [String(result.model).trim()] : [];
    delete state.providerModelErrors[provider.id];
    renderProviderModelOptions(provider.id, result.model);
    if (result.warning) $("providerModelState").textContent = "模型測試成功，但未取得模型列表。";
    const duration = Number(result.duration_milliseconds) || 0;
    const toolState = result.tool_calling ? "原生工具正常" : "未驗證原生工具";
    $("providerTestState").textContent = result.warning
      ? `成功 · ${duration} ms · ${toolState} · 模型列表警告`
      : `成功 · ${duration} ms · ${toolState}`;
    $("providerSettingsState").textContent = "測試成功";
    toast(result.warning || `Provider ${provider.id} 測試成功，原生工具呼叫正常，使用模型 ${result.model}`);
  } catch (error) {
    $("providerTestState").textContent = "測試失敗";
    $("providerSettingsState").textContent = "測試失敗";
    toast(error.message);
  } finally {
    $("testProviderSetting").disabled = false;
    $("saveProviderSetting").disabled = false;
  }
}

async function deleteProviderSetting() {
  if (!state.providerSettings || !state.selectedProviderSettingsID) return;
  const id = state.selectedProviderSettingsID;
  if (state.providerSettings.providers.length <= 1) {
    toast("系統至少必須保留一個 Provider");
    return;
  }
  if (id === state.providerSettings.default_provider_id) {
    toast("請先將其他 Provider 設為系統預設，再刪除此 Provider");
    return;
  }
  if (!(await confirmAction(`確定刪除 Provider「${id}」？`))) return;
  const providers = state.providerSettings.providers.filter((item) => item.id !== id);
  $("deleteProviderSetting").disabled = true;
  try {
    state.providerSettings = await request("/api/v1/admin/provider-settings", {
      method: "PUT",
      body: JSON.stringify(providerSettingsPayload(providers, state.providerSettings.default_provider_id)),
    });
    delete state.providerModelLists[id];
    delete state.providerModelErrors[id];
    delete state.providerTestedModels[id];
    state.contextCapabilities = null;
    state.contextCapabilityCache = {};
    state.selectedProviderSettingsID = state.providerSettings.providers[0]?.id || "";
    await refreshProviderCatalog();
    renderProviderSettings();
    toast(`已刪除 Provider ${id}`);
  } catch (error) {
    toast(error.message);
  } finally {
    $("deleteProviderSetting").disabled = false;
  }
}

function updateProviderOAuthStatusUI(status = {}) {
	const connected = status.status === "connected";
	const pending = status.status === "pending";
	const account = status.account_email || status.account_name || "";
	$("providerOAuthState").textContent = connected
	  ? `已連線${account ? ` · ${account}` : ""}`
	  : pending ? "等待瀏覽器完成驗證…"
	  : status.status === "failed" ? `驗證失敗${status.message ? ` · ${status.message}` : ""}`
	  : "尚未連線";
	$("connectProviderOAuth").disabled = pending;
	$("connectProviderOAuth").textContent = connected ? "重新登入 ChatGPT／Codex" : "使用 ChatGPT 帳號登入";
	$("disconnectProviderOAuth").classList.toggle("hidden", !connected);
}

async function loadProviderOAuthStatus(providerID, { quiet = false } = {}) {
	providerID = String(providerID || "").trim();
	if (!providerID) return null;
	try {
	  const status = await request(`/api/v1/admin/provider-settings/${encodeURIComponent(providerID)}/oauth/status`, { reconnects: 0 });
	  if (!state.providerSettingsDraft && state.selectedProviderSettingsID === providerID && $("providerSettingType").value === "openai-codex-responses") {
		updateProviderOAuthStatusUI(status);
	  }
	  const provider = state.providerSettings?.providers?.find((item) => item.id === providerID);
	  if (provider?.openai_codex_responses) provider.openai_codex_responses.has_oauth_token = status.status === "connected";
	  return status;
	} catch (error) {
	  if (!quiet) toast(error.message);
	  return null;
	}
}

function pollProviderOAuth(providerID, expiresAt) {
	clearTimeout(providerOAuthPollTimer);
	const deadline = Date.parse(expiresAt || "") || Date.now() + 10 * 60 * 1000;
	const poll = async () => {
	  if (Date.now() >= deadline) {
		updateProviderOAuthStatusUI({ status: "failed", message: "驗證逾時" });
		return;
	  }
	  const status = await loadProviderOAuthStatus(providerID, { quiet: true });
	  if (status?.status === "connected") {
		toast(`Provider ${providerID} 已完成 ChatGPT／Codex OAuth 驗證`);
		await loadProviderModels(providerID, { notify: false });
		return;
	  }
	  if (status?.status === "failed") {
		toast(status.message || "ChatGPT／Codex OAuth 驗證失敗");
		return;
	  }
	  providerOAuthPollTimer = window.setTimeout(poll, 1000);
	};
	void poll();
}

async function connectProviderOAuth() {
	if ($("providerSettingType").value !== "openai-codex-responses") return;
	const provider = await persistProviderSetting({ showToast: false, refreshModels: false });
	if (!provider) return;
	$("connectProviderOAuth").disabled = true;
	updateProviderOAuthStatusUI({ status: "pending" });
	try {
	  const result = await request(`/api/v1/admin/provider-settings/${encodeURIComponent(provider.id)}/oauth/start`, { method: "POST" });
	  if (!result.browser_opened) {
		const opened = window.open(result.authorization_url, "_blank", "noopener,noreferrer");
		if (!opened) toast("無法自動開啟瀏覽器，請允許彈出式視窗後重試");
	  }
	  pollProviderOAuth(provider.id, result.expires_at);
	} catch (error) {
	  updateProviderOAuthStatusUI({ status: "failed", message: error.message });
	  toast(error.message);
	}
}

async function disconnectProviderOAuth() {
	const providerID = state.selectedProviderSettingsID;
	if (!providerID) return;
	$("disconnectProviderOAuth").disabled = true;
	try {
	  await request(`/api/v1/admin/provider-settings/${encodeURIComponent(providerID)}/oauth`, { method: "DELETE" });
	  clearTimeout(providerOAuthPollTimer);
	  const provider = state.providerSettings?.providers?.find((item) => item.id === providerID);
	  if (provider?.openai_codex_responses) provider.openai_codex_responses.has_oauth_token = false;
	  updateProviderOAuthStatusUI({ status: "idle" });
	  delete state.providerModelLists[providerID];
	  delete state.providerModelErrors[providerID];
	  renderProviderModelOptions(providerID);
	  toast(`Provider ${providerID} 已中斷 ChatGPT／Codex OAuth`);
	} catch (error) {
	  toast(error.message);
	} finally {
	  $("disconnectProviderOAuth").disabled = false;
	}
}

async function refreshProviderCatalog() {
  state.providers = await request("/api/v1/providers");
  renderProviderOptions();
  renderSessionProviderOptions();
  syncWorkspaceSettings();
  if (state.session) syncSessionUI();
}

function newMCPSetting() {
  return {
    id: "",
    display_name: "",
    enabled: true,
    transport: "stdio",
    command: "",
    args: [],
    work_dir: "",
    url: "",
    startup_timeout_seconds: 20,
    call_timeout_seconds: 1800,
    trust_annotations: true,
    status: "disconnected",
    tools: [],
  };
}

async function loadMCPSettings(preferredID = state.selectedMCPSettingsID) {
  try {
    state.mcpSettings = await request("/api/v1/admin/mcp-settings");
    state.mcpSettingsDraft = null;
    const servers = state.mcpSettings?.servers || [];
    state.selectedMCPSettingsID = servers.some((item) => item.id === preferredID)
      ? preferredID
      : servers[0]?.id || "";
    renderMCPSettings();
  } catch (error) {
    toast(error.message);
  }
}

function renderMCPTransportFields(transport) {
  const stdio = transport === "stdio";
  $("mcpStdioFields").classList.toggle("hidden", !stdio);
  $("mcpHTTPFields").classList.toggle("hidden", stdio);
  $("mcpSettingCommand").required = stdio;
  $("mcpSettingURL").required = !stdio;
}

function isMCPImportObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function mcpImportSensitiveName(value) {
  return /(authorization|api[-_]?key|access[-_]?token|refresh[-_]?token|token|secret|password|credential|username|user_name|account)/i.test(String(value || ""));
}

function mcpImportVariable(value) {
  return /\$\{[^}]+\}/.test(String(value || ""));
}

function mcpImportSourceValue(source, names) {
  for (const name of names) {
    if (source[name] !== undefined && source[name] !== null) return source[name];
  }
  return undefined;
}

function mcpImportStringMap(value, preserveSensitiveNames = false) {
  const result = {};
  let removedSensitive = false;
  if (!isMCPImportObject(value)) return { value: result, removedSensitive };
  for (const [key, raw] of Object.entries(value)) {
    if (typeof raw !== "string" && typeof raw !== "number" && typeof raw !== "boolean") continue;
    const text = String(raw);
    if (mcpImportSensitiveName(key) || mcpImportVariable(text) || /^(?:bearer|basic)\s+/i.test(text)) {
      removedSensitive = true;
      if (preserveSensitiveNames && String(key).trim()) result[key] = "";
      continue;
    }
    result[key] = text;
  }
  return { value: result, removedSensitive };
}

function mcpTransportValue(value, fallback = "stdio") {
  const transport = String(value || "").trim().toLowerCase();
  if (transport === "stdio") return "stdio";
  if (["sse", "server-sent-events", "server_sent_events"].includes(transport)) return "sse";
  if (transport === "streamable-http" || transport === "streamable_http" || transport === "http") return "streamable-http";
  return fallback;
}

function mcpImportAuthMethodValue(value) {
  const method = String(value || "").trim().toLowerCase().replace(/[_\s]+/g, "-");
  if (["none", "no-auth", "no-authentication", "anonymous", "unauthenticated"].includes(method)) return "none";
  if (["bearer", "bearer-token", "token", "api-key", "oauth", "oauth2"].includes(method)) return "bearer";
  if (["basic", "basic-auth", "password", "username-password"].includes(method)) return "basic";
  if (["header", "headers", "custom-header", "custom-headers", "http-headers"].includes(method)) return "headers";
  return "";
}

function mcpImportAuthMethods(source, rawAuth, rawHeaders, apiKey, authUsername, authPassword) {
  const methods = new Set();
  const add = (value) => {
    const method = mcpImportAuthMethodValue(value);
    if (method) methods.add(method);
  };
  const declared = mcpImportSourceValue(source, ["auth_methods", "authMethods", "authentication_methods", "authenticationMethods"])
    ?? (isMCPImportObject(rawAuth) ? mcpImportSourceValue(rawAuth, ["methods", "supported_methods", "supportedMethods"]) : undefined);
  if (Array.isArray(declared)) declared.forEach(add);
  else if (declared !== undefined) add(declared);
  const rawAuthType = isMCPImportObject(rawAuth) ? mcpImportSourceValue(rawAuth, ["type", "method"]) : rawAuth;
  add(mcpImportSourceValue(source, ["auth_method", "authMethod", "authentication_type", "authenticationType"]));
  add(rawAuthType);
  if (typeof apiKey === "string" && apiKey.trim()) add("bearer");
  if ((typeof authUsername === "string" && authUsername.trim()) || (typeof authPassword === "string" && authPassword)) add("basic");
  if (isMCPImportObject(rawHeaders) && Object.keys(rawHeaders).length > 0) add("headers");
  if (!methods.size) methods.add("none");
  if (methods.size > 1) methods.delete("none");
  return ["none", "bearer", "basic", "headers"].filter((method) => methods.has(method));
}

function mcpImportAuthOptions(methods = ["none", "bearer", "basic", "headers"]) {
  const allowed = new Set(methods);
  return [
    { value: "none", label: "不使用驗證" },
    { value: "bearer", label: "Bearer Token（金鑰）" },
    { value: "basic", label: "Basic Auth（帳號密碼）" },
    { value: "headers", label: "自訂 HTTP Headers" },
  ].filter((option) => allowed.has(option.value));
}

function mcpImportID(value, fallback, used) {
  let id = String(value || fallback || "mcp-server")
    .trim()
    .replace(/[^A-Za-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 80);
  if (!id) id = "mcp-server";
  const base = id;
  let suffix = 2;
  while (used.has(id)) {
    id = `${base.slice(0, Math.max(1, 80 - String(suffix).length - 1))}-${suffix}`;
    suffix += 1;
  }
  used.add(id);
  return id;
}

function mcpImportEntries(root, fileName) {
  if (Array.isArray(root)) return root.map((value, index) => [value?.id || value?.name || `mcp-server-${index + 1}`, value]);
  if (!isMCPImportObject(root)) throw new Error(".mcp 檔案的根內容必須是 JSON 物件");
  if (isMCPImportObject(root.mcpServers)) return Object.entries(root.mcpServers);
  if (Array.isArray(root.servers)) return root.servers.map((value, index) => [value?.id || value?.name || `mcp-server-${index + 1}`, value]);
  if (isMCPImportObject(root.servers)) return Object.entries(root.servers);
  if (isMCPImportObject(root.server)) return [[root.server.id || root.server.name || "mcp-server", root.server]];
  if (["command", "url", "server_url", "endpoint", "args"].some((key) => root[key] !== undefined)) {
    const fallback = String(fileName || "mcp-server").replace(/\.[^.]+$/, "");
    return [[root.id || root.name || fallback, root]];
  }
  throw new Error("找不到 MCP Server 設定；請使用 mcpServers、servers 或單一 Server JSON 格式");
}

function normalizeMCPImport(root, fileName) {
  const entries = mcpImportEntries(root, fileName);
  if (!entries.length) throw new Error(".mcp 檔案沒有可匯入的 MCP Server");
  const usedIDs = new Set();
  return entries.map(([entryID, raw], index) => {
    const source = isMCPImportObject(raw) ? raw : {};
    const id = mcpImportID(source.id || entryID, `mcp-server-${index + 1}`, usedIDs);
    const displayName = String(source.display_name || source.displayName || source.name || entryID || id).trim().slice(0, 80);
    const rawURL = mcpImportSourceValue(source, ["url", "server_url", "serverUrl", "endpoint", "base_url", "baseUrl"]);
    const url = typeof rawURL === "string" && !mcpImportVariable(rawURL) ? rawURL.trim() : "";
    const explicitTransport = String(source.transport || source.type || "").trim().toLowerCase();
    const transport = explicitTransport === "stdio" || source.command || source.executable
      ? "stdio"
      : mcpTransportValue(explicitTransport, "streamable-http");
    const rawEnvironment = mcpImportSourceValue(source, ["environment", "env"]);
    const environment = mcpImportStringMap(rawEnvironment, true);
    const rawHeaders = mcpImportSourceValue(source, ["headers", "http_headers", "httpHeaders"]);
    const headers = mcpImportStringMap(rawHeaders, true);
    let removedSensitive = environment.removedSensitive || headers.removedSensitive;
    const apiKey = mcpImportSourceValue(source, ["api_key", "apiKey", "access_token", "accessToken", "bearer_token", "bearerToken", "token"]);
    if (typeof apiKey === "string" && apiKey.trim()) removedSensitive = true;
    const rawAuth = mcpImportSourceValue(source, ["basic_auth", "basicAuth", "authentication", "auth"]);
    const authUsername = mcpImportSourceValue(source, ["username", "user_name", "account"])
      ?? (isMCPImportObject(rawAuth) ? mcpImportSourceValue(rawAuth, ["username", "user_name", "account"]) : undefined);
    const authPassword = mcpImportSourceValue(source, ["password", "pass"])
      ?? (isMCPImportObject(rawAuth) ? mcpImportSourceValue(rawAuth, ["password", "pass"]) : undefined);
    if ((typeof authUsername === "string" && authUsername.trim()) || (typeof authPassword === "string" && authPassword)) {
      removedSensitive = true;
    }
    if (typeof rawURL === "string" && mcpImportVariable(rawURL)) removedSensitive = true;
    const authMethods = transport === "stdio" ? ["none"] : mcpImportAuthMethods(source, rawAuth, rawHeaders, apiKey, authUsername, authPassword);
    const rawArgs = mcpImportSourceValue(source, ["args", "arguments"]);
    const args = Array.isArray(rawArgs) ? rawArgs.filter((value) => typeof value === "string") : [];
    const timeout = Number(source.startup_timeout_seconds ?? source.startupTimeoutSeconds);
    const callTimeout = Number(source.call_timeout_seconds ?? source.callTimeoutSeconds);
    return {
      id,
      display_name: displayName || id,
      enabled: source.enabled !== false,
      transport,
      command: String(source.command || source.executable || "").trim(),
      args,
      work_dir: String(source.work_dir || source.workDir || source.cwd || "").trim(),
      url,
      api_key: "",
      auth_methods: authMethods,
      auth_method: authMethods[0],
      headers: headers.value,
      environment: environment.value,
      startup_timeout_seconds: Number.isFinite(timeout) && timeout > 0 ? Math.min(300, timeout) : 20,
      call_timeout_seconds: Number.isFinite(callTimeout) && callTimeout > 0 ? Math.min(86400, callTimeout) : 1800,
      // 匯入的設定沒有明確關閉時採預設值（信任）。
      trust_annotations: source.trust_annotations !== false && source.trustAnnotations !== false,
      removedSensitive,
      missingURL: transport !== "stdio" && !url,
    };
  });
}

function mcpImportTextField(labelText, field, value, options = {}) {
  const label = document.createElement("label");
  if (options.full) label.classList.add("provider-field-full");
  label.append(document.createTextNode(labelText));
  const control = document.createElement(options.textarea ? "textarea" : "input");
  control.dataset.importField = field;
  if (options.textarea) {
    control.rows = options.rows || 3;
    control.spellcheck = false;
  } else {
    control.type = options.type || "text";
  }
  if (options.required) control.required = true;
  if (options.pattern) control.pattern = options.pattern;
  if (options.maxLength) control.maxLength = options.maxLength;
  if (options.placeholder) control.placeholder = options.placeholder;
  if (options.autocomplete) control.autocomplete = options.autocomplete;
  control.value = value ?? "";
  label.append(control);
  if (options.hint) {
    const hint = document.createElement("small");
    hint.className = "hint";
    hint.textContent = options.hint;
    label.append(hint);
  }
  return label;
}

function mcpImportSelectField(labelText, field, value, options = {}) {
  const label = document.createElement("label");
  if (options.full) label.classList.add("provider-field-full");
  label.append(document.createTextNode(labelText));
  const select = document.createElement("select");
  select.dataset.importField = field;
  for (const option of options.options || []) {
    const element = document.createElement("option");
    element.value = option.value;
    element.textContent = option.label;
    select.append(element);
  }
  select.value = value || options.options?.[0]?.value || "";
  label.append(select);
  return label;
}

function mcpImportCheckField(labelText, field, checked, full = false) {
  const label = document.createElement("label");
  label.className = "check-label";
  if (full) label.classList.add("provider-field-full");
  const input = document.createElement("input");
  input.type = "checkbox";
  input.checked = checked === true;
  input.dataset.importField = field;
  label.append(input, document.createTextNode(labelText));
  return label;
}

function renderMCPImportTransportFields(serverElement, transport) {
  const stdio = transport === "stdio";
  const stdioFields = serverElement.querySelector("[data-import-stdio]");
  const advancedDetails = serverElement.querySelector("[data-import-advanced-details]");
  const httpFields = serverElement.querySelector("[data-import-http]");
  const serverIndex = serverElement.dataset.importServer;
  const authMethod = serverElement.querySelector('[data-import-field="auth_method"]')
    || document.querySelector(`[data-import-server-field="${serverIndex}"][data-import-field="auth_method"]`);
  stdioFields?.classList.toggle("hidden", !stdio);
  advancedDetails?.classList.remove("hidden");
  httpFields?.classList.toggle("hidden", stdio);
  authMethod?.closest("label")?.classList.toggle("hidden", stdio);
  if (authMethod?.closest("#mcpImportAuthMethodBar")) {
    $("mcpImportAuthMethodBar").classList.toggle("hidden", stdio);
  }
  const command = serverElement.querySelector('[data-import-field="command"]');
  const url = serverElement.querySelector('[data-import-field="url"]');
  if (command) command.required = stdio;
  if (url) url.required = !stdio;
  renderMCPImportAuthFields(serverElement, authMethod?.value || "none");
}

function renderMCPImportAuthFields(serverElement, method) {
  const fieldMap = {
    bearer: ["api_key"],
    basic: ["username", "password"],
    headers: ["headers"],
  };
  const visibleFields = new Set(fieldMap[method] || []);
  for (const field of ["api_key", "username", "password", "headers"]) {
    const label = serverElement.querySelector(`[data-import-field="${field}"]`)?.closest("label");
    label?.classList.toggle("hidden", !visibleFields.has(field));
  }
}

function renderMCPImportDialog() {
  const draft = state.mcpImportDraft;
  if (!draft) return;
  $("mcpImportFileName").textContent = `${draft.fileName} · ${draft.servers.length} 個 Server`;
  $("mcpImportHint").textContent = draft.servers.some((server) => server.removedSensitive || server.missingURL)
    ? "請補填必要連線資訊；檔案中的金鑰、Token、Authorization 與變數值已清空。"
    : "請確認連線資訊；匯入只會在按下確認後安裝。";
  $("mcpImportOverwrite").checked = false;
  const list = $("mcpImportServerList");
  const authMethodBar = $("mcpImportAuthMethodBar");
  authMethodBar.replaceChildren();
  authMethodBar.classList.toggle("hidden", draft.servers.length !== 1);
  list.replaceChildren();
  draft.servers.forEach((server, index) => {
    const fieldset = document.createElement("fieldset");
    fieldset.className = "mcp-import-server";
    fieldset.dataset.importServer = String(index);
    const legend = document.createElement("legend");
    legend.textContent = server.display_name || server.id;
    fieldset.append(legend);
    if (server.removedSensitive || server.missingURL) {
      const note = document.createElement("small");
      note.className = "mcp-import-secret-note";
      note.textContent = server.missingURL
        ? "此 Server 尚未提供有效 URL，請填寫後才能安裝。"
        : "檔案中的敏感值已清空，請在下方重新輸入。";
      fieldset.append(note);
    }
    const grid = document.createElement("div");
    grid.className = "provider-field-grid";
    grid.append(
      mcpImportTextField("MCP ID", "id", server.id, { required: true, maxLength: 80, pattern: "[A-Za-z0-9_-]+" }),
      mcpImportTextField("顯示名稱", "display_name", server.display_name, { maxLength: 80 }),
      mcpImportSelectField("傳輸方式", "transport", server.transport, { options: [
        { value: "stdio", label: "Stdio" },
        { value: "sse", label: "SSE（舊版）" },
        { value: "streamable-http", label: "Streamable HTTP" },
      ] }),
      mcpImportCheckField("啟用這個 MCP Server", "enabled", server.enabled, true),
    );
    const transportSelect = grid.querySelector('[data-import-field="transport"]');
    const stdioAdvanced = document.createElement("details");
    stdioAdvanced.className = "provider-field-full mcp-import-advanced";
    stdioAdvanced.dataset.importAdvancedDetails = "true";
    stdioAdvanced.open = true;
    const stdioSummary = document.createElement("summary");
    stdioSummary.textContent = "進階設定";
    stdioAdvanced.append(stdioSummary);
    const stdioFields = document.createElement("div");
    stdioFields.className = "provider-field-grid mcp-import-transport-fields";
    stdioFields.dataset.importStdio = "true";
    stdioFields.append(
      mcpImportTextField("啟動命令", "command", server.command, { full: true, required: true, placeholder: "npx" }),
      mcpImportTextField("參數（JSON 陣列）", "args", JSON.stringify(server.args, null, 2), { full: true, textarea: true, rows: 3 }),
      mcpImportTextField("工作目錄", "work_dir", server.work_dir, { full: true, placeholder: "留空使用 APP 工作目錄" }),
      mcpImportTextField("環境變數（JSON 物件）", "environment", JSON.stringify(server.environment, null, 2), { full: true, textarea: true, rows: 3, hint: "敏感環境變數的名稱會保留，但值已清空。" }),
    );
    stdioAdvanced.append(stdioFields);
    const httpFields = document.createElement("div");
    httpFields.className = "provider-field-full provider-field-grid mcp-import-transport-fields";
    httpFields.dataset.importHttp = "true";
    httpFields.append(
      mcpImportTextField("Server URL", "url", server.url, { full: true, type: "url", required: true, placeholder: "https://example.com/mcp" }),
      mcpImportTextField("金鑰（Bearer Token）", "api_key", "", { full: true, type: "password", placeholder: "輸入 MCP Server 金鑰", hint: "留空代表不使用 Bearer Token。" }),
      mcpImportTextField("帳號", "username", "", { full: true, autocomplete: "username", placeholder: "輸入 MCP Server 帳號" }),
      mcpImportTextField("密碼", "password", "", { full: true, type: "password", autocomplete: "current-password", placeholder: "輸入 MCP Server 密碼", hint: "留空代表不使用 Basic Auth。" }),
      mcpImportTextField("HTTP Headers（JSON 物件）", "headers", JSON.stringify(server.headers, null, 2), { full: true, textarea: true, rows: 3, hint: "Authorization 等敏感標頭不會直接匯入。" }),
    );
    const authMethod = mcpImportSelectField("驗證方式", "auth_method", server.auth_method || "none", { options: mcpImportAuthOptions(server.auth_methods) });
    authMethod.classList.add("mcp-import-auth-method");
    authMethod.querySelector("select").dataset.importServerField = String(index);
    stdioAdvanced.append(httpFields);
    if (draft.servers.length === 1) {
      authMethodBar.append(authMethod);
    } else {
      fieldset.insertBefore(authMethod, grid);
    }
    grid.append(stdioAdvanced);
    fieldset.append(grid);
    transportSelect?.addEventListener("change", (event) => renderMCPImportTransportFields(fieldset, event.target.value));
    authMethod.querySelector("select")?.addEventListener("change", (event) => renderMCPImportAuthFields(fieldset, event.target.value));
    renderMCPImportTransportFields(fieldset, server.transport);
    list.append(fieldset);
  });
}

function mcpImportFormValue() {
  if (!state.mcpImportDraft) return null;
  const form = $("mcpImportForm");
  if (!form.checkValidity()) {
    for (const invalid of form.querySelectorAll(":invalid")) invalid.closest("details")?.setAttribute("open", "");
    form.reportValidity();
    return null;
  }
  const values = [];
  const ids = new Set();
  try {
    for (const fieldset of $("mcpImportServerList").querySelectorAll("[data-import-server]")) {
      const read = (field) => fieldset.querySelector(`[data-import-field="${field}"]`)
        || document.querySelector(`[data-import-server-field="${fieldset.dataset.importServer}"][data-import-field="${field}"]`);
      const id = read("id").value.trim();
      if (ids.has(id)) throw new Error(`MCP ID ${id} 在匯入檔中重複`);
      ids.add(id);
      const transport = mcpTransportValue(read("transport").value);
      const server = {
        id,
        display_name: read("display_name").value.trim(),
        enabled: read("enabled").checked,
        transport,
        startup_timeout_seconds: state.mcpImportDraft.servers[Number(fieldset.dataset.importServer)].startup_timeout_seconds,
        call_timeout_seconds: state.mcpImportDraft.servers[Number(fieldset.dataset.importServer)].call_timeout_seconds,
        trust_annotations: state.mcpImportDraft.servers[Number(fieldset.dataset.importServer)].trust_annotations,
      };
      if (transport === "stdio") {
        server.command = read("command").value.trim();
        server.args = parseMCPJSON(read("args").value, "參數", "array") || [];
        server.work_dir = read("work_dir").value.trim();
        server.environment = parseMCPJSON(read("environment").value, "環境變數", "object");
      } else {
        server.url = read("url").value.trim();
        const authMethod = read("auth_method")?.value || "none";
        if (authMethod === "bearer") {
          const apiKey = read("api_key").value.trim();
          if (apiKey) server.api_key = apiKey;
        } else if (authMethod === "basic") {
          const username = read("username").value.trim();
          const password = read("password").value;
          if (username) server.username = username;
          if (password) server.password = password;
        } else if (authMethod === "headers") {
          server.headers = parseMCPJSON(read("headers").value, "HTTP Headers", "object");
          if (server.headers) {
            for (const [name, value] of Object.entries(server.headers)) {
              if (!String(value).trim()) delete server.headers[name];
            }
          }
        }
      }
      values.push(server);
    }
  } catch (error) {
    toast(error.message);
    return null;
  }
  return values;
}

async function installMCPImport(event) {
  event.preventDefault();
  const imported = mcpImportFormValue();
  if (!imported || !state.mcpSettings) return;
  const existing = state.mcpSettings.servers || [];
  const existingIDs = new Set(existing.map((server) => server.id));
  const conflicts = imported.filter((server) => existingIDs.has(server.id)).map((server) => server.id);
  const overwrite = $("mcpImportOverwrite").checked;
  if (conflicts.length && !overwrite) {
    toast(`MCP ID 已存在：${conflicts.join("、")}；請勾選覆蓋既有設定`);
    return;
  }
  const importedIDs = new Set(imported.map((server) => server.id));
  const merged = [...(overwrite ? existing.filter((server) => !importedIDs.has(server.id)) : existing), ...imported];
  $("installMCPImport").disabled = true;
  try {
    state.mcpSettings = await request("/api/v1/admin/mcp-settings", {
      method: "PUT",
      body: JSON.stringify(mcpSettingsPayload(merged)),
    });
    const selectedID = imported[0]?.id || "";
    state.mcpImportDraft = null;
    $("mcpImportDialog").close();
    state.selectedMCPSettingsID = selectedID;
    await loadMCPSettings(selectedID);
    await loadTools();
    toast(`已安裝 ${imported.length} 個 MCP Server`);
  } catch (error) {
    toast(`MCP 安裝失敗：${error.message}`);
  } finally {
    $("installMCPImport").disabled = false;
  }
}

async function handleMCPImportContent(fileName, size, content) {
  if (Number(size) > 2 * 1024 * 1024) {
    toast(".mcp 檔案不得超過 2 MB");
    return;
  }
  const root = JSON.parse(content);
  const servers = normalizeMCPImport(root, fileName);
  if (!state.mcpSettings) await loadMCPSettings();
  if (!state.mcpSettings) throw new Error("MCP 設定尚未載入，請確認後端連線");
  state.mcpImportDraft = { fileName, servers };
  renderMCPImportDialog();
  if (!$("mcpImportDialog").open) $("mcpImportDialog").showModal();
}

async function handleMCPImportFile(file) {
  if (!file) return;
  try {
    await handleMCPImportContent(file.name, file.size, await file.text());
  } catch (error) {
    toast(`無法讀取 .mcp 檔案：${error.message}`);
  }
}

function mcpImportDroppedFile(dataTransfer) {
  return [...(dataTransfer?.files || [])].find((file) => /\.(?:mcp|json)$/i.test(file.name || ""));
}

async function handleMCPImportDrop(dataTransfer) {
  const browserFile = mcpImportDroppedFile(dataTransfer);
  if (browserFile) {
    await handleMCPImportFile(browserFile);
    return;
  }
  try {
    const dropped = await desktop("mcp/files/dropped", { method: "POST", body: "{}" });
    const file = Array.isArray(dropped) ? dropped[0] : null;
    if (!file?.name || typeof file.content !== "string") {
      throw new Error("系統沒有提供可讀取的 .mcp 檔案");
    }
    await handleMCPImportContent(file.name, file.size, file.content);
  } catch (error) {
    toast(`無法讀取系統拖入的 .mcp 檔案：${error.message}`);
  }
}

function renderMCPSettings() {
  const servers = state.mcpSettings?.servers || [];
  const list = $("mcpSettingsList");
  list.replaceChildren();
  for (const server of servers) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `provider-setting-card ${server.enabled ? "" : "provider-disabled"} ${!state.mcpSettingsDraft && server.id === state.selectedMCPSettingsID ? "active" : ""}`;
    const title = document.createElement("strong");
    title.textContent = server.display_name || server.id;
    const badge = document.createElement("span");
    badge.className = server.enabled ? "provider-default-badge" : "provider-disabled-badge";
    badge.textContent = server.enabled ? (server.status || "未連線") : "已停用";
    const detail = document.createElement("small");
    detail.textContent = `${server.transport || "-"} · ${Number(server.tool_count) || 0} 個工具`;
    button.append(title, badge, detail);
    button.addEventListener("click", () => {
      state.mcpSettingsDraft = null;
      state.selectedMCPSettingsID = server.id;
      renderMCPSettings();
    });
    list.append(button);
  }

  const selected = state.mcpSettingsDraft
    || servers.find((item) => item.id === state.selectedMCPSettingsID);
  $("mcpSettingsForm").classList.toggle("hidden", !selected);
  $("noMCPSettings").classList.toggle("hidden", Boolean(selected));
  if (!selected) return;

  const isNew = Boolean(state.mcpSettingsDraft);
  $("mcpSettingsEditorTitle").textContent = isNew ? "新增 MCP Server" : `編輯 ${selected.display_name || selected.id}`;
  $("mcpSettingID").value = selected.id || "";
  $("mcpSettingID").disabled = !isNew;
  $("mcpSettingDisplayName").value = selected.display_name || "";
  $("mcpSettingEnabled").checked = selected.enabled !== false;
  $("mcpSettingTransport").value = selected.transport || "stdio";
  $("mcpSettingCommand").value = selected.command || "";
  $("mcpSettingArgs").value = JSON.stringify(Array.isArray(selected.args) ? selected.args : [], null, 2);
  $("mcpSettingWorkDir").value = selected.work_dir || "";
  $("mcpSettingURL").value = selected.url || "";
  $("mcpSettingAPIKey").value = "";
  $("mcpSettingClearKey").setAttribute("aria-pressed", "false");
  $("mcpSettingClearKey").classList.toggle("hidden", !selected.has_api_key);
  $("mcpAPIKeyState").textContent = selected.has_api_key
    ? "已儲存 MCP 金鑰；留空會保留，勾選下方選項可清除。"
    : "尚未儲存 MCP 金鑰；金鑰不會從後端讀回。";
  $("mcpSettingUsername").value = "";
  $("mcpSettingPassword").value = "";
  $("mcpSettingClearBasicAuth").setAttribute("aria-pressed", "false");
  $("mcpSettingClearBasicAuth").classList.toggle("hidden", !selected.has_basic_auth);
  $("mcpBasicAuthState").textContent = selected.has_basic_auth
    ? "已儲存帳號密碼；留空會保留，勾選下方選項可清除。"
    : "尚未儲存帳號密碼；帳密不會從後端讀回。";
  $("mcpSettingEnvironment").value = "";
  $("mcpSettingHeaders").value = "";
  $("mcpEnvironmentState").textContent = selected.has_environment
    ? "已儲存環境變數；留空會保留，輸入 {} 會清除。"
    : "尚未儲存環境變數；敏感內容不會從後端讀回。";
  $("mcpHeadersState").textContent = selected.has_headers
    ? "已儲存 HTTP Headers；留空會保留，輸入 {} 會清除。"
    : "尚未儲存 HTTP Headers；敏感內容不會從後端讀回。";
  $("mcpSettingStartupTimeout").value = Number(selected.startup_timeout_seconds) || 20;
  $("mcpSettingCallTimeout").value = Number(selected.call_timeout_seconds) || 1800;
  // 預設信任：唯讀查詢逐次核准只會讓使用者對核准對話框麻木；不信任的 Server 再手動關閉。
  $("mcpSettingTrustAnnotations").checked = selected.trust_annotations !== false;
  $("mcpConnectionState").textContent = selected.enabled === false ? "已停用" : (selected.status || "未連線");
  // 憑證明文不會回傳，因此直接顯示後端實際送出的驗證方式，讓「金鑰有沒有被使用」
  // 不必靠猜。
  $("mcpAuthModeState").textContent = selected.transport === "stdio"
    ? ""
    : `${translate("目前送出的驗證方式")}：${translate(mcpAuthModeLabels[selected.auth_mode] || "不使用驗證")}`;
  $("mcpConnectionError").textContent = selected.error || "";
  $("mcpSettingsState").textContent = "";
  $("mcpTestState").textContent = "";
  $("deleteMCPSetting").classList.toggle("hidden", isNew);
  const toolList = $("mcpToolList");
  const exposed = Array.isArray(selected.tools) ? selected.tools : [];
  const available = Array.isArray(selected.available_tools) && selected.available_tools.length
    ? selected.available_tools
    : exposed;
  const enabled = Array.isArray(selected.enabled_tools) ? selected.enabled_tools : [];
  $("mcpToolListCount").textContent = available.length === exposed.length
    ? String(exposed.length)
    : `${exposed.length}/${available.length}`;
  toolList.classList.add("hidden");
  toolList.replaceChildren();
  // 工具定義會整份進入每一次請求。外掛型 MCP Server 動輒數十上百個工具，
  // 光是 schema 就可能佔掉數萬 token，因此這裡讓使用者只勾要用的。
  for (const tool of available) {
    const label = document.createElement("label");
    label.className = "check-label mcp-tool-option";
    const input = document.createElement("input");
    input.type = "checkbox";
    input.dataset.mcpTool = tool.name;
    input.checked = enabled.length === 0 || enabled.includes(tool.name);
    const name = document.createElement("code");
    name.textContent = tool.name;
    name.setAttribute("data-i18n-ignore", "");
    label.append(input, name);
    if (tool.display_name && tool.display_name !== tool.name) {
      const title = document.createElement("small");
      title.textContent = tool.display_name;
      title.setAttribute("data-i18n-ignore", "");
      label.append(title);
    }
    toolList.append(label);
  }
  if (!available.length) {
    const small = document.createElement("small");
    small.className = "hint";
    small.textContent = "尚未載入工具清單。";
    toolList.append(small);
  } else {
    const hint = document.createElement("small");
    hint.className = "hint";
    hint.textContent = "只勾選需要的工具：工具定義每次請求都會整份送給模型，數量越多提示越長、模型越慢。全部勾選代表不限制。";
    toolList.append(hint);
  }
  syncMCPToolListToggle();
  renderMCPTransportFields(selected.transport || "stdio");
}

function syncMCPToolListToggle() {
  const button = $("mcpToolListToggle");
  const list = $("mcpToolList");
  if (!button || !list) return;
  const expanded = !list.classList.contains("hidden");
  const label = expanded ? "收合功能表" : "展開功能表";
  button.setAttribute("aria-expanded", String(expanded));
  button.title = translate(label);
  button.setAttribute("aria-label", translate(label));
}

function parseMCPJSON(value, label, expected) {
  const source = String(value || "").trim();
  if (!source) return undefined;
  let parsed;
  try {
    parsed = JSON.parse(source);
  } catch {
    throw new Error(`${label} 必須是有效的 JSON`);
  }
  if (expected === "array" && (!Array.isArray(parsed) || parsed.some((item) => typeof item !== "string"))) {
    throw new Error(`${label} 必須是只包含字串的 JSON 陣列`);
  }
  if (expected === "object" && (parsed === null || Array.isArray(parsed) || typeof parsed !== "object"
    || Object.values(parsed).some((item) => typeof item !== "string"))) {
    throw new Error(`${label} 必須是字串對字串的 JSON 物件`);
  }
  return parsed;
}

function mcpSettingFormValue() {
  const transport = mcpTransportValue($("mcpSettingTransport").value);
  const server = {
    id: $("mcpSettingID").value.trim(),
    display_name: $("mcpSettingDisplayName").value.trim(),
    enabled: $("mcpSettingEnabled").checked,
    transport,
    startup_timeout_seconds: Number($("mcpSettingStartupTimeout").value),
    call_timeout_seconds: Number($("mcpSettingCallTimeout").value),
    trust_annotations: $("mcpSettingTrustAnnotations").checked,
  };
  if (transport === "stdio") {
    server.command = $("mcpSettingCommand").value.trim();
    server.args = parseMCPJSON($("mcpSettingArgs").value, "參數", "array") || [];
    server.work_dir = $("mcpSettingWorkDir").value.trim();
    server.environment = parseMCPJSON($("mcpSettingEnvironment").value, "環境變數", "object");
  } else {
    server.url = $("mcpSettingURL").value.trim();
    const apiKey = $("mcpSettingAPIKey").value.trim();
    if (apiKey) server.api_key = apiKey;
    if ($("mcpSettingClearKey").getAttribute("aria-pressed") === "true") server.api_key = "";
    const username = $("mcpSettingUsername").value.trim();
    const password = $("mcpSettingPassword").value;
    if (username) server.username = username;
    if (password) server.password = password;
    if ($("mcpSettingClearBasicAuth").getAttribute("aria-pressed") === "true") {
      server.username = "";
      server.password = "";
    }
    server.headers = parseMCPJSON($("mcpSettingHeaders").value, "HTTP Headers", "object");
  }
  const options = [...document.querySelectorAll("#mcpToolList input[data-mcp-tool]")];
  if (options.length > 0) {
    const checked = options.filter((input) => input.checked).map((input) => input.dataset.mcpTool);
    // 全選代表不限制：日後 Server 新增工具時自動納入，而不是被凍結在目前這份清單。
    server.enabled_tools = checked.length === options.length ? [] : checked;
  }
  return server;
}

function mcpSettingsPayload(servers) {
  return { servers: servers.map((server) => {
    const value = JSON.parse(JSON.stringify(server));
    delete value.has_environment;
    delete value.available_tools;
    delete value.has_api_key;
    delete value.has_basic_auth;
    delete value.has_headers;
    delete value.status;
    delete value.error;
    delete value.tool_count;
    delete value.tools;
    delete value.updated_at;
    return value;
  }) };
}

async function persistMCPSetting({ showToast = true } = {}) {
  if (!state.mcpSettings || !$("mcpSettingsForm").reportValidity()) return null;
  let server;
  try {
    server = mcpSettingFormValue();
  } catch (error) {
    toast(error.message);
    return null;
  }
  const isNew = Boolean(state.mcpSettingsDraft);
  if (isNew && state.mcpSettings.servers.some((item) => item.id === server.id)) {
    toast(`MCP ID ${server.id} 已存在`);
    return null;
  }
  const servers = isNew
    ? [...state.mcpSettings.servers, server]
    : state.mcpSettings.servers.map((item) => item.id === state.selectedMCPSettingsID ? server : item);
  $("saveMCPSetting").disabled = true;
  $("testMCPSetting").disabled = true;
  $("mcpSettingsState").textContent = "儲存中…";
  try {
    state.mcpSettings = await request("/api/v1/admin/mcp-settings", {
      method: "PUT",
      body: JSON.stringify(mcpSettingsPayload(servers)),
    });
    state.mcpSettingsDraft = null;
    state.selectedMCPSettingsID = server.id;
    renderMCPSettings();
    $("mcpSettingsState").textContent = server.enabled ? "已儲存，背景連線中" : "已停用";
    if (showToast) toast(`MCP Server ${server.id} 設定已儲存`);
    return server;
  } catch (error) {
    $("mcpSettingsState").textContent = "儲存失敗";
    toast(error.message);
    return null;
  } finally {
    $("saveMCPSetting").disabled = false;
    $("testMCPSetting").disabled = false;
  }
}

async function saveMCPSetting(event) {
  event.preventDefault();
  await persistMCPSetting();
}

async function testMCPSetting() {
  const server = await persistMCPSetting({ showToast: false });
  if (!server) return;
  $("testMCPSetting").disabled = true;
  $("saveMCPSetting").disabled = true;
  $("mcpTestState").textContent = "連線中…";
  try {
    const result = await request(`/api/v1/admin/mcp-settings/${encodeURIComponent(server.id)}/test`, { method: "POST", reconnects: 0 });
    $("mcpTestState").textContent = `成功 · ${result.tool_count || 0} 個工具`;
    toast(`MCP Server ${server.id} 連線成功`);
    await loadMCPSettings(server.id);
    await loadTools();
  } catch (error) {
    $("mcpTestState").textContent = "連線失敗";
    toast(error.message);
    await loadMCPSettings(server.id);
  } finally {
    $("testMCPSetting").disabled = false;
    $("saveMCPSetting").disabled = false;
  }
}

async function deleteMCPSetting() {
  if (!state.mcpSettings || !state.selectedMCPSettingsID) return;
  const id = state.selectedMCPSettingsID;
  if (!(await confirmAction(`確定刪除 MCP Server「${id}」？`))) return;
  $("deleteMCPSetting").disabled = true;
  try {
    state.mcpSettings = await request("/api/v1/admin/mcp-settings", {
      method: "PUT",
      body: JSON.stringify(mcpSettingsPayload(state.mcpSettings.servers.filter((item) => item.id !== id))),
    });
    state.selectedMCPSettingsID = state.mcpSettings.servers[0]?.id || "";
    renderMCPSettings();
    await loadTools();
    toast(`已刪除 MCP Server ${id}`);
  } catch (error) {
    toast(error.message);
  } finally {
    $("deleteMCPSetting").disabled = false;
  }
}

async function loadReverseProxyStatus({ hydrate = false, silent = false } = {}) {
  if (state.reverseProxyLoading) return state.reverseProxy;
  state.reverseProxyLoading = true;
  try {
    state.reverseProxy = await request("/api/v1/admin/reverse-proxy", { reconnects: silent ? 0 : 3 });
    renderReverseProxyStatus({ hydrate });
    return state.reverseProxy;
  } catch (error) {
    if (!silent) toast(error.message);
    return null;
  } finally {
    state.reverseProxyLoading = false;
    if (state.reverseProxy) renderReverseProxyStatus();
  }
}

function renderReverseProxyStatus({ hydrate = false } = {}) {
  const status = state.reverseProxy || {};
  const running = Boolean(status.running);
  const connected = Boolean(status.connected);
  const available = Boolean(status.available);
  const apiKeyEntered = Boolean($("reverseProxyAPIKey").value.trim());
  const policyAccepted = $("reverseProxyAcceptPolicy").checked;

  if (hydrate || !state.reverseProxyHydrated) {
    $("reverseProxyEndpoint").value = status.endpoint || "https://netpass.mars-cloud.com";
    $("reverseProxyName").value = status.name || "";
    $("reverseProxyAPIKey").value = "";
    $("reverseProxyClearKey").checked = false;
    state.reverseProxyHydrated = true;
  }
  $("reverseProxyTargetPort").value = status.target_port || "—";
  $("reverseProxyClearKeyRow").classList.toggle("hidden", !status.api_key_set);
  $("reverseProxyAPIKeyState").textContent = status.api_key_set
    ? "已儲存 NetPass API Key；留空會保留，勾選下方選項可清除。"
    : "尚未儲存 NetPass API Key；API Key 不會從後端讀回。";

  const badge = $("reverseProxyConnectionBadge");
  badge.textContent = connected ? "已連線" : (running ? "連線中" : "未啟動");
  badge.classList.toggle("warning", running && !connected);
  $("reverseProxyRuntimeState").textContent = available ? "可用" : "找不到 Runtime";
  $("reverseProxyConnectedState").textContent = connected ? "已連線" : (running ? "連線中" : "未連線");
  $("reverseProxyPID").textContent = status.pid || "—";
  $("reverseProxyClientID").textContent = status.client_id || "—";

  const publicURL = status.public_url || "";
  $("reverseProxyPublicURL").value = publicURL;
  $("reverseProxyPublicURLRow").classList.toggle("hidden", !publicURL);
  $("reverseProxyError").textContent = status.last_error || "";
  $("reverseProxyError").classList.toggle("hidden", !status.last_error);

  for (const id of ["reverseProxyEndpoint", "reverseProxyAPIKey", "reverseProxyClearKey", "reverseProxyName"]) {
    $(id).disabled = running;
  }
  $("saveReverseProxy").disabled = running || state.reverseProxyLoading;
  $("startReverseProxy").disabled = running || !available || !(status.api_key_set || apiKeyEntered) || !policyAccepted;
  $("startReverseProxy").classList.toggle("hidden", running);
  $("stopReverseProxy").classList.toggle("hidden", !running);
  $("stopReverseProxy").disabled = state.reverseProxyLoading;
}

async function persistReverseProxySettings({ showToast = true } = {}) {
  const form = $("reverseProxySettingsForm");
  if (!form.reportValidity() || state.reverseProxy?.running) return null;
  const apiKey = $("reverseProxyAPIKey").value.trim();
  $("saveReverseProxy").disabled = true;
  $("startReverseProxy").disabled = true;
  $("reverseProxySettingsState").textContent = "儲存中…";
  try {
    state.reverseProxy = await request("/api/v1/admin/reverse-proxy", {
      method: "PUT",
      body: JSON.stringify({
        endpoint: $("reverseProxyEndpoint").value.trim(),
        api_key: apiKey,
        clear_api_key: $("reverseProxyClearKey").checked,
        name: $("reverseProxyName").value.trim(),
      }),
    });
    $("reverseProxySettingsState").textContent = "已儲存";
    renderReverseProxyStatus({ hydrate: true });
    if (showToast) toast("反向代理設定已儲存");
    return state.reverseProxy;
  } catch (error) {
    $("reverseProxySettingsState").textContent = "儲存失敗";
    toast(error.message);
    return null;
  } finally {
    renderReverseProxyStatus();
  }
}

async function saveReverseProxySettings(event) {
  event.preventDefault();
  await persistReverseProxySettings();
}

async function startReverseProxy() {
  if (!$("reverseProxyAcceptPolicy").checked) {
    toast("請先閱讀並同意反向代理使用政策");
    return;
  }
  const saved = await persistReverseProxySettings({ showToast: false });
  if (!saved) return;
  $("startReverseProxy").disabled = true;
  $("saveReverseProxy").disabled = true;
  $("reverseProxySettingsState").textContent = "啟動中…";
  try {
    state.reverseProxy = await request("/api/v1/admin/reverse-proxy/start", {
      method: "POST",
      body: JSON.stringify({ accept_usage_policy: true }),
    });
    $("reverseProxySettingsState").textContent = "反向代理啟動中";
    renderReverseProxyStatus();
    toast("NetPass 反向代理已啟動");
  } catch (error) {
    $("reverseProxySettingsState").textContent = "啟動失敗";
    toast(error.message);
    await loadReverseProxyStatus({ silent: true });
  }
}

async function stopReverseProxy() {
  $("stopReverseProxy").disabled = true;
  $("reverseProxySettingsState").textContent = "停止中…";
  try {
    state.reverseProxy = await request("/api/v1/admin/reverse-proxy/stop", {
      method: "POST",
      body: "{}",
    });
    $("reverseProxySettingsState").textContent = "已停止";
    renderReverseProxyStatus();
    toast("NetPass 反向代理已停止");
  } catch (error) {
    $("reverseProxySettingsState").textContent = "停止失敗";
    toast(error.message);
  } finally {
    $("stopReverseProxy").disabled = false;
  }
}

function refreshReverseProxyControls() {
  if (state.reverseProxy) renderReverseProxyStatus();
}

function refreshReverseProxyInBackground() {
  const panelOpen = !$("managementPanel").classList.contains("hidden")
    && !$("reverseProxyPanel").classList.contains("hidden");
  if (panelOpen || state.reverseProxy?.running) void loadReverseProxyStatus({ silent: true });
}

async function loadTools() {
  try {
    const query = state.session ? `?session_id=${encodeURIComponent(state.session.id)}` : "";
    const values = await request(`/api/v1/tools${query}`);
    const container = $("toolList");
    container.replaceChildren();
    for (const item of values) {
      const article = document.createElement("article");
      article.className = `tool-item ${item.available ? "available" : "unavailable"}`;
      const head = document.createElement("div");
      const title = document.createElement("strong");
      const badge = document.createElement("span");
      title.textContent = item.definition.label || item.definition.name;
      badge.className = "tool-status";
      badge.textContent = item.available ? "可用" : item.allowed ? "權限不足" : "已停用";
      head.append(title, badge);
      const code = document.createElement("code");
      code.textContent = item.definition.name;
      const description = document.createElement("p");
      description.textContent = item.definition.description;
      article.append(head, code, description);
      if (item.unavailable_reason) {
        const reason = document.createElement("small");
        reason.textContent = item.unavailable_reason;
        article.append(reason);
      }
      container.append(article);
    }
  } catch (error) {
    toast(error.message);
  }
}

async function loadAudit(reset) {
  const list = $("auditList");
  if (!state.session) {
    list.replaceChildren();
    const empty = document.createElement("p");
    empty.className = "quiet";
    empty.textContent = "請先選擇 Session。";
    list.append(empty);
    $("loadMoreAudit").classList.add("hidden");
    return;
  }
  if (reset) {
    state.entriesAfter = 0;
    list.replaceChildren();
  }
  try {
    const value = await request(`/api/v1/sessions/${encodeURIComponent(state.session.id)}/entries?after_sequence=${state.entriesAfter}&limit=100`);
    for (const entry of value.items || []) list.append(auditNode(entry));
    state.entriesAfter = value.next_after_sequence || state.entriesAfter;
    state.entriesHasMore = Boolean(value.has_more);
    $("loadMoreAudit").classList.toggle("hidden", !state.entriesHasMore);
    if (!list.children.length) {
      const empty = document.createElement("p");
      empty.className = "quiet";
      empty.textContent = "尚無稽核資料。";
      list.append(empty);
    }
  } catch (error) {
    toast(error.message);
  }
}

function auditNode(entry) {
  const article = document.createElement("article");
  article.className = "audit-item";
  const head = document.createElement("div");
  const type = document.createElement("strong");
  const sequence = document.createElement("span");
  type.textContent = entry.type;
  sequence.textContent = `#${entry.sequence}`;
  head.append(type, sequence);
  const time = document.createElement("small");
  time.textContent = new Date(entry.created_at).toLocaleString();
  const pre = document.createElement("pre");
  const data = entry.message
    ? { role: entry.message.role, content: entry.message.content, tool_name: entry.message.tool_name, stop_reason: entry.message.stop_reason }
    : entry.data || {};
  const text = JSON.stringify(data, null, 2);
  pre.textContent = text.length > 4000 ? `${text.slice(0, 4000)}\n…` : text;
  article.append(head, time, pre);
  return article;
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

async function initializeApplication() {
  await loadPromptOutbox();
  await refreshBackend();
}

$("startBackend").addEventListener("click", () => controlBackend("start"));
$("restartBackend").addEventListener("click", () => controlBackend("restart"));
$("stopBackend").addEventListener("click", () => controlBackend("stop"));
$("displayMode").addEventListener("change", (event) => applyDisplayMode(event.target.value));
$("modelControlButton").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleModelPopover();
});
$("openPlan").addEventListener("click", openPlanDialog);
$("closePlan").addEventListener("click", () => $("planDialog").close());
$("lockPlansSwitch").addEventListener("change", changePlanLock);
$("activePlanTab").addEventListener("click", () => selectPlanTab("active"));
$("completedPlanTab").addEventListener("click", () => selectPlanTab("completed"));
for (const tab of [$("activePlanTab"), $("completedPlanTab")]) {
  tab.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    selectPlanTab(event.key === "ArrowRight" || event.key === "End" ? "completed" : "active", true);
  });
}
$("createPlan").addEventListener("click", () => editPlan());
$("clearCompletedPlans").addEventListener("click", clearCompletedPlans);
$("createPlanFromEmpty").addEventListener("click", () => editPlan());
$("planList").addEventListener("dragover", handlePlanDragOver);
$("planList").addEventListener("drop", persistPlanOrder);
$("addPlanStep").addEventListener("click", () => addPlanStepEditorRow());
$("cancelPlanEdit").addEventListener("click", cancelPlanEdit);
$("planForm").addEventListener("submit", savePlan);
$("workspaceSelect").addEventListener("change", (event) => switchWorkspace(event.target.value));
$("workspaceManagementSelect").addEventListener("change", (event) => switchWorkspace(event.target.value));
async function openNewWorkspaceDialog() {
  renderProviderOptions();
  const provider = selectableProviders()[0];
  if (provider) {
    await loadProviderModels(provider.id, { notify: false });
    $("newWorkspaceProvider").value = provider.id;
    $("newWorkspaceModel").value = displayedModelForProvider(provider.id);
  }
  $("workspaceDialog").showModal();
}
$("newWorkspace").addEventListener("click", openNewWorkspaceDialog);
$("newManagementWorkspace").addEventListener("click", openNewWorkspaceDialog);
$("cancelWorkspace").addEventListener("click", () => $("workspaceDialog").close());
$("workspaceForm").addEventListener("submit", createWorkspace);
$("workspaceSettings").addEventListener("submit", saveWorkspaceSettings);
$("serviceSettings").addEventListener("submit", saveServiceSettings);
$("toolSettings").addEventListener("submit", saveToolSettings);
$("settingHTTPFetchEnabled").addEventListener("change", syncHTTPFetchSettingFields);
$("settingServiceName").addEventListener("input", (event) => {
  if (state.serviceSettings) {
    state.serviceSettings.service_name_is_default = event.target.value.trim() === localizedDefaultServiceName();
  }
});
$("settingUILanguage").addEventListener("change", (event) => applyUILanguage(event.target.value));
$("newWorkspaceProvider").addEventListener("change", async (event) => {
  const provider = selectableProviders().find((item) => item.id === event.target.value);
  if (provider) {
    await loadProviderModels(provider.id, { notify: false });
    $("newWorkspaceModel").value = displayedModelForProvider(provider.id);
  }
});
$("newProject").addEventListener("click", () => {
  resetProjectForm();
  $("projectDialog").showModal();
});
$("projectSectionToggle").addEventListener("click", toggleProjectSection);
$("scheduleToggle").addEventListener("click", toggleScheduleSection);
$("newSchedule").addEventListener("click", () => openScheduleDialog(null));
$("scheduleFrequency").addEventListener("change", syncScheduleFrequencyFields);
$("scheduleForm").addEventListener("submit", saveSchedule);
$("cancelSchedule").addEventListener("click", closeScheduleDialog);
$("deleteSchedule").addEventListener("click", () => void deleteScheduleFromDialog());
$("runScheduleNow").addEventListener("click", () => void runScheduleNow(state.editingSchedule));
$("pickScheduleFolders").addEventListener("click", () => pickProjectSandboxRoots("schedule"));
$("pickScheduleFolders").addEventListener("dragenter", (event) => handleProjectFolderDrag(event, "schedule"));
$("pickScheduleFolders").addEventListener("dragover", (event) => handleProjectFolderDrag(event, "schedule"));
$("pickScheduleFolders").addEventListener("dragleave", () => $("pickScheduleFolders").classList.remove("drag-over"));
$("pickScheduleFolders").addEventListener("drop", (event) => handleProjectFolderDrop(event, "schedule"));
$("cancelProject").addEventListener("click", () => {
  $("projectDialog").close();
  resetProjectForm();
});
$("projectForm").addEventListener("submit", createProject);
$("pickProjectFolders").addEventListener("click", () => pickProjectSandboxRoots("create"));
$("pickProjectFolders").addEventListener("dragenter", (event) => handleProjectFolderDrag(event, "create"));
$("pickProjectFolders").addEventListener("dragover", (event) => handleProjectFolderDrag(event, "create"));
$("pickProjectFolders").addEventListener("dragleave", () => $("pickProjectFolders").classList.remove("drag-over"));
$("pickProjectFolders").addEventListener("drop", (event) => handleProjectFolderDrop(event, "create"));
$("cancelProjectSettings").addEventListener("click", closeProjectSettings);
$("projectSettingsForm").addEventListener("submit", saveProjectSettings);
$("deleteProjectSettings").addEventListener("click", deleteProjectSettings);
$("pickEditProjectFolders").addEventListener("click", () => pickProjectSandboxRoots("settings"));
$("pickEditProjectFolders").addEventListener("dragenter", (event) => handleProjectFolderDrag(event, "settings"));
$("pickEditProjectFolders").addEventListener("dragover", (event) => handleProjectFolderDrag(event, "settings"));
$("pickEditProjectFolders").addEventListener("dragleave", () => $("pickEditProjectFolders").classList.remove("drag-over"));
$("pickEditProjectFolders").addEventListener("drop", (event) => handleProjectFolderDrop(event, "settings"));
$("cancelSession").addEventListener("click", () => $("sessionDialog").close());
$("restoreRunsForm").addEventListener("submit", (event) => {
  event.preventDefault();
  $("restoreRunsDialog").close("confirm");
});
$("cancelRestoreRuns").addEventListener("click", () => $("restoreRunsDialog").close("cancel"));
$("sessionForm").addEventListener("submit", createSession);
$("openContextProjectDirectory").addEventListener("click", () => {
  const project = state.contextProject;
  const directory = project?.sandbox_roots?.find((root) => String(root || "").trim());
  closeProjectContextMenu();
  if (directory) void openResource({ kind: "path", target: directory }, "open");
});
$("manageContextProject").addEventListener("click", () => {
  const project = state.contextProject;
  closeProjectContextMenu();
  if (project) manageProject(project);
});
$("newContextProjectSession").addEventListener("click", () => {
  const project = state.contextProject;
  closeProjectContextMenu();
  if (project) openSessionDialog(project.id);
});
$("deleteContextProject").addEventListener("click", () => {
  const project = state.contextProject;
  closeProjectContextMenu();
  if (project) void deleteProject(project);
});
$("inspectContextSession").addEventListener("click", () => {
  const session = state.contextSession;
  closeSessionContextMenu();
  openSessionContent(session);
});
$("renameContextSession").addEventListener("click", () => {
  const session = state.contextSession;
  closeSessionContextMenu();
  openRenameSessionDialog(session);
});
$("pinContextSession").addEventListener("click", () => {
  const session = state.contextSession;
  closeSessionContextMenu();
  if (session) setPinned(session, !session.pinned);
});
$("deleteContextSession").addEventListener("click", () => {
  const session = state.contextSession;
  closeSessionContextMenu();
  openDeleteSessionDialog(session);
});
$("openContextResource").addEventListener("click", () => {
  const resource = state.contextResource;
  closeResourceContextMenu();
  void openResource(resource, "open");
});
$("revealContextResource").addEventListener("click", () => {
  const resource = state.contextResource;
  closeResourceContextMenu();
  void openResource(resource, "reveal");
});
$("copyContextResource").addEventListener("click", () => {
  const resource = state.contextResource;
  closeResourceContextMenu();
  if (resource) void copyText(resource.target, resource.kind === "url" ? "已複製網址" : "已複製路徑");
});
$("cancelRenameSession").addEventListener("click", () => $("renameSessionDialog").close());
$("renameSessionForm").addEventListener("submit", renameSession);
$("renameSessionDialog").addEventListener("close", () => { state.renamingSession = null; });
$("closeSessionContent").addEventListener("click", () => $("sessionContentDialog").close());
$("sessionContentDialog").addEventListener("close", () => { state.inspectingSession = null; });
$("cancelDeleteSession").addEventListener("click", () => $("deleteSessionDialog").close());
$("deleteSessionForm").addEventListener("submit", deleteSession);
$("deleteSessionDialog").addEventListener("close", () => { state.deletingSession = null; });
$("sessionSettings").addEventListener("submit", saveSessionSettings);
$("sessionProviderSelect").addEventListener("change", changeSessionProvider);
$("sessionModelSelect").addEventListener("change", changeSessionModel);
$("sessionThinkingSelect").addEventListener("change", changeSessionThinking);
$("newProvider").addEventListener("change", (event) => {
  const providerID = event.target.value || state.workspace?.default_provider_id || "";
  $("newModel").value = defaultModelForProvider(providerID);
});
$("composer").addEventListener("submit", sendPrompt);
$("hideWindowDuringCapture").checked = state.screenCaptureHideWindow;
$("captureScreen").addEventListener("click", () => { closeScreenCaptureMenu(); void captureScreenToClipboard(); });
$("screenCaptureOptions").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleScreenCaptureMenu();
});
$("screenCaptureMenu").addEventListener("pointerdown", (event) => event.stopPropagation());
$("hideWindowDuringCapture").addEventListener("change", (event) => {
  state.screenCaptureHideWindow = event.target.checked;
  localStorage.setItem("nrIntern.screenCapture.hideWindow", event.target.checked ? "1" : "0");
});
for (const button of document.querySelectorAll("[data-image-tool]")) {
  button.addEventListener("click", () => setImageEditorTool(button.dataset.imageTool));
}
$("imageEditorColorButton").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleImageEditorColorPalette();
});
$("imageEditorColorPalette").addEventListener("pointerdown", (event) => event.stopPropagation());
for (const button of document.querySelectorAll("[data-image-color]")) {
  button.style.setProperty("--image-editor-color", button.dataset.imageColor);
  button.addEventListener("click", () => setImageEditorColor(button.dataset.imageColor, { close: true }));
}
$("imageEditorColorHex").addEventListener("input", (event) => {
  setImageEditorColor(event.target.value, { syncHex: false });
});
$("imageEditorColorHex").addEventListener("blur", (event) => {
  if (!setImageEditorColor(event.target.value)) event.target.value = $("imageEditorColor").value.toUpperCase();
});
$("imageEditorColorHex").addEventListener("keydown", (event) => {
  if (event.key !== "Enter") return;
  event.preventDefault();
  if (setImageEditorColor(event.target.value, { close: true })) $("imageEditorColorButton").focus();
});
$("closeImageEditor").addEventListener("click", () => { void closeImageEditor(); });
$("undoImageEdit").addEventListener("click", undoImageEdit);
$("resetImageEdit").addEventListener("click", resetImageEdit);
$("copyEditedImage").addEventListener("click", () => { void copyEditedImage(); });
$("imageEditorCanvas").addEventListener("pointerdown", startImageEditorDrawing);
$("imageEditorCanvas").addEventListener("pointermove", moveImageEditorDrawing);
$("imageEditorCanvas").addEventListener("pointerup", (event) => finishImageEditorDrawing(event, true));
$("imageEditorCanvas").addEventListener("pointercancel", (event) => finishImageEditorDrawing(event, false));
$("imageEditorDialog").addEventListener("keydown", (event) => {
  if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "z") return;
  event.preventDefault();
  undoImageEdit();
});
$("imageEditorDialog").addEventListener("close", () => {
  closeImageEditorColorPalette();
  imageEditorState.image = null;
  imageEditorState.operations = [];
  imageEditorState.draft = null;
  imageEditorState.pointerId = null;
});
window.addEventListener("resize", fitImageEditorCanvas);
$("send").addEventListener("click", activateRunAction);
$("stopRun").addEventListener("click", () => { void cancelCurrentRun(); });
$("messages").addEventListener("scroll", updateMessageAutoScroll, { passive: true });
$("prompt").addEventListener("paste", (event) => {
  if (!state.session) return;
  const files = [...(event.clipboardData?.files || [])];
  if (files.length) addPendingAttachments(files);
});
const chatWorkspace = document.querySelector("main.workspace");
chatWorkspace.addEventListener("dragenter", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer) || !state.session) return;
  event.preventDefault();
  chatDragDepth += 1;
  chatWorkspace.classList.add("chat-drag-over");
});
chatWorkspace.addEventListener("dragover", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer) || !state.session) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
  chatWorkspace.classList.add("chat-drag-over");
});
chatWorkspace.addEventListener("dragleave", () => {
  chatDragDepth = Math.max(0, chatDragDepth - 1);
  if (chatDragDepth === 0) chatWorkspace.classList.remove("chat-drag-over");
});
chatWorkspace.addEventListener("drop", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer)) return;
  event.preventDefault();
  chatDragDepth = 0;
  chatWorkspace.classList.remove("chat-drag-over");
  if (!state.session) return;
  addPendingAttachments(event.dataTransfer.files);
  $("prompt").focus();
});
$("prompt").addEventListener("compositionstart", () => {
  promptComposition.active = true;
  promptComposition.enterObserved = false;
  promptComposition.suppressNextEnter = false;
  clearTimeout(promptComposition.resetTimer);
});
$("prompt").addEventListener("compositionend", () => {
  promptComposition.active = false;
  // Safari／部分桌面 WebView 會先送 compositionend，再送同一次選字用的 Enter keydown。
  // 若 keydown 已在 composition 期間觀察到，就不應再攔截使用者下一次真正要送出的 Enter。
  promptComposition.suppressNextEnter = !promptComposition.enterObserved;
  promptComposition.enterObserved = false;
  clearTimeout(promptComposition.resetTimer);
  promptComposition.resetTimer = setTimeout(() => {
    promptComposition.suppressNextEnter = false;
  }, 160);
});
$("prompt").addEventListener("keydown", (event) => {
  // 任何系統快捷鍵都交回瀏覽器／桌面 WebView，不得被送出訊息邏輯攔截。
  if (hasSystemShortcutModifier(event)) return;
  const enterKey = event.key === "Enter";
  const composing = event.isComposing || promptComposition.active || event.keyCode === 229;
  if (enterKey && composing) {
    promptComposition.enterObserved = true;
    return;
  }
  if (!enterKey) {
    promptComposition.suppressNextEnter = false;
    return;
  }
  if (promptComposition.suppressNextEnter) {
    promptComposition.suppressNextEnter = false;
    event.preventDefault();
    return;
  }
  if (event.shiftKey) return;
  event.preventDefault();
  $("composer").requestSubmit();
});
$("retryRun").addEventListener("click", retryCurrentRun);
$("approveTool").addEventListener("click", () => decideApproval("approve"));
$("denyTool").addEventListener("click", () => decideApproval("deny"));
$("openManagement").addEventListener("click", () => openManagement("overview"));
$("memoButton").addEventListener("click", openMemo);
$("closeMemo").addEventListener("click", closeMemo);
$("clearMemo").addEventListener("click", () => void clearMemo());
$("memoText").addEventListener("input", scheduleMemoSave);
$("memoText").addEventListener("keydown", (event) => {
  if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "s") return;
  event.preventDefault();
  saveMemo();
});
$("memoDialog").addEventListener("close", () => {
  if (memoSaveTimer) saveMemo();
});
$("providerUsageButton").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleProviderUsagePopover();
});
$("notificationButton").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleNotificationPopover();
});
$("markAllNotificationsRead").addEventListener("click", async () => {
  try { await request("/api/v1/notifications/read-all", { method: "POST", body: "{}" }); await loadNotifications(); } catch (error) { toast(error.message); }
});
$("clearReadNotifications").addEventListener("click", async () => {
  try { await request("/api/v1/notifications/read", { method: "DELETE" }); await loadNotifications(); } catch (error) { toast(error.message); }
});
$("globalSearchButton").addEventListener("click", () => void loadGlobalSearch());
$("globalSearchInput").addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); void loadGlobalSearch(); } });
$("contextUsageButton").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleContextUsagePopover();
});
$("closeManagement").addEventListener("click", closeManagement);
for (const button of document.querySelectorAll(".panel-tab")) button.addEventListener("click", () => activatePanel(button.dataset.panel));
$("refreshDiagnostics").addEventListener("click", loadDiagnostics);
$("exportDiagnostics").addEventListener("click", () => void downloadAdminFile("/api/v1/admin/diagnostics/export", "nr-intern-diagnostics.json"));
$("downloadBackup").addEventListener("click", () => void downloadAdminFile("/api/v1/admin/backup", "nr-intern-backup.zip"));
$("checkForUpdates").addEventListener("click", () => void checkForUpdates());
$("openLatestRelease").addEventListener("click", () => {
	if (state.updateStatus?.release_url) void openResource({ kind: "url", target: state.updateStatus.release_url }, "open");
});
$("restoreBackup").addEventListener("click", () => $("restoreBackupFile").click());
$("restoreBackupFile").addEventListener("change", (event) => { void restoreAdminBackup(event.target.files?.[0]); event.target.value = ""; });
$("pauseRun").addEventListener("click", () => void pauseCurrentRun());
$("resumeRun").addEventListener("click", () => void resumeCurrentRun());
$("cancelAllRuns").addEventListener("click", async () => {
  if (!(await confirmAction("確定取消所有排隊中與執行中的 Run 嗎？"))) return;
  try { const value = await request("/api/v1/runs/cancel-all", { method: "POST", body: "{}" }); toast("已送出取消 " + (value.accepted || 0) + " 個 Run 的要求"); } catch (error) { toast(error.message); }
});
$("refreshPermissions").addEventListener("click", loadPermissionCenter);
$("addProviderSetting").addEventListener("click", () => {
  state.providerSettingsDraft = newProviderSetting();
  state.selectedProviderSettingsID = "";
  renderProviderSettings();
  $("providerSettingID").focus();
});
$("providerSettingsForm").addEventListener("submit", saveProviderSetting);
$("deleteProviderSetting").addEventListener("click", deleteProviderSetting);
$("refreshProviderModels").addEventListener("click", () => loadProviderModels(state.selectedProviderSettingsID));
$("testProviderSetting").addEventListener("click", testProviderSetting);
$("connectProviderOAuth").addEventListener("click", connectProviderOAuth);
$("disconnectProviderOAuth").addEventListener("click", disconnectProviderOAuth);
$("addMCPSetting").addEventListener("click", () => {
  state.mcpSettingsDraft = newMCPSetting();
  state.selectedMCPSettingsID = "";
  renderMCPSettings();
  $("mcpSettingID").focus();
});
$("mcpSettingsForm").addEventListener("submit", saveMCPSetting);
$("deleteMCPSetting").addEventListener("click", deleteMCPSetting);
$("testMCPSetting").addEventListener("click", testMCPSetting);
$("mcpToolListToggle").addEventListener("click", () => {
  $("mcpToolList").classList.toggle("hidden");
  syncMCPToolListToggle();
});
$("mcpSettingTransport").addEventListener("change", (event) => renderMCPTransportFields(event.target.value));
$("mcpSettingAPIKey").addEventListener("input", (event) => {
  if (event.target.value) $("mcpSettingClearKey").setAttribute("aria-pressed", "false");
});
$("mcpSettingClearKey").addEventListener("click", (event) => {
  const button = event.currentTarget;
  const clear = button.getAttribute("aria-pressed") !== "true";
  button.setAttribute("aria-pressed", String(clear));
  if (clear) $("mcpSettingAPIKey").value = "";
});
for (const id of ["mcpSettingUsername", "mcpSettingPassword"]) {
  $(id).addEventListener("input", (event) => {
    if (event.target.value) $("mcpSettingClearBasicAuth").setAttribute("aria-pressed", "false");
  });
}
$("mcpSettingClearBasicAuth").addEventListener("click", (event) => {
  const button = event.currentTarget;
  const clear = button.getAttribute("aria-pressed") !== "true";
  button.setAttribute("aria-pressed", String(clear));
  if (clear) {
    $("mcpSettingUsername").value = "";
    $("mcpSettingPassword").value = "";
  }
});
$("mcpImportDropzone").addEventListener("click", () => $("mcpImportFile").click());
$("mcpImportDropzone").addEventListener("keydown", (event) => {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  $("mcpImportFile").click();
});
$("mcpImportDropzone").addEventListener("dragenter", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer)) return;
  event.preventDefault();
  $("mcpImportDropzone").classList.add("drag-over");
});
$("mcpImportDropzone").addEventListener("dragover", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer)) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
  $("mcpImportDropzone").classList.add("drag-over");
});
$("mcpImportDropzone").addEventListener("dragleave", (event) => {
  if (event.relatedTarget && $("mcpImportDropzone").contains(event.relatedTarget)) return;
  $("mcpImportDropzone").classList.remove("drag-over");
});
$("mcpImportDropzone").addEventListener("drop", (event) => {
  event.preventDefault();
  $("mcpImportDropzone").classList.remove("drag-over");
  void handleMCPImportDrop(event.dataTransfer);
});
$("mcpImportFile").addEventListener("change", (event) => {
  const file = event.target.files?.[0];
  event.target.value = "";
  if (file) void handleMCPImportFile(file);
});
$("mcpImportForm").addEventListener("submit", (event) => { void installMCPImport(event); });
$("closeMCPImport").addEventListener("click", () => $("mcpImportDialog").close());
$("cancelMCPImport").addEventListener("click", () => $("mcpImportDialog").close());
$("mcpImportDialog").addEventListener("close", () => { state.mcpImportDraft = null; });
$("reverseProxySettingsForm").addEventListener("submit", saveReverseProxySettings);
$("startReverseProxy").addEventListener("click", startReverseProxy);
$("stopReverseProxy").addEventListener("click", stopReverseProxy);
$("refreshReverseProxy").addEventListener("click", () => loadReverseProxyStatus());
$("reverseProxyAPIKey").addEventListener("input", (event) => {
  if (event.target.value) $("reverseProxyClearKey").checked = false;
  refreshReverseProxyControls();
});
$("reverseProxyClearKey").addEventListener("change", (event) => {
  if (event.target.checked) $("reverseProxyAPIKey").value = "";
  refreshReverseProxyControls();
});
$("reverseProxyAcceptPolicy").addEventListener("change", refreshReverseProxyControls);
$("copyReverseProxyURL").addEventListener("click", () => copyText($("reverseProxyPublicURL").value, "已複製公開網址"));
$("openReverseProxyURL").addEventListener("click", () => {
  const target = $("reverseProxyPublicURL").value;
  if (target) void openResource({ kind: "url", target }, "open");
});
$("providerSettingType").addEventListener("change", (event) => {
  const isCodex = event.target.value === "openai-codex-responses";
  const settings = isCodex ? {
    has_oauth_token: false,
    model: "gpt-5.2-codex",
    max_attempts: 3,
    timeout_seconds: 1800,
    connect_timeout_seconds: 20,
    response_header_timeout_seconds: 120,
    context_window: 0,
    max_output_tokens: 0,
  } : {
    base_url: "https://api.openai.com/v1",
    has_api_key: false,
    model: "gpt-4o-mini",
    instruction_role: "system",
    disable_streaming: false,
    stream_include_usage: true,
    omit_tool_choice: false,
    max_attempts: 3,
    timeout_seconds: 1800,
    connect_timeout_seconds: 20,
    response_header_timeout_seconds: 120,
    context_window: 0,
    max_output_tokens: 0,
  };
  $("providerSettingBaseURL").value = settings.base_url || "";
  $("providerSettingAPIKey").value = "";
  $("providerSettingModel").value = settings.model;
  $("providerSettingModel").dataset.configuredModel = settings.model;
  $("providerSettingInstructionRole").value = settings.instruction_role || "system";
  $("providerSettingDisableStreaming").checked = Boolean(settings.disable_streaming);
  $("providerSettingStreamUsage").checked = settings.stream_include_usage !== false;
  $("providerSettingOmitToolChoice").checked = Boolean(settings.omit_tool_choice);
  $("providerSettingMaxAttempts").value = settings.max_attempts;
  $("providerSettingTimeout").value = settings.timeout_seconds;
  $("providerSettingConnectTimeout").value = settings.connect_timeout_seconds;
  $("providerSettingHeaderTimeout").value = settings.response_header_timeout_seconds;
  $("providerSettingContextWindow").value = settings.context_window;
  $("providerSettingMaxOutputTokens").value = settings.max_output_tokens;
  renderProviderTypeFields(event.target.value, settings, Boolean(state.providerSettingsDraft), state.selectedProviderSettingsID);
  renderProviderModelOptions("");
});
$("providerSettingDefault").addEventListener("change", (event) => {
  if (event.target.checked) $("providerSettingEnabled").checked = true;
});
$("providerSettingEnabled").addEventListener("change", (event) => {
  if (!event.target.checked && $("providerSettingDefault").checked) event.target.checked = true;
});
$("providerSettingModelCatalog").addEventListener("change", (event) => {
  if (event.target.value === "__custom_model__") {
    event.target.classList.add("hidden");
    $("providerSettingModel").classList.remove("hidden");
    $("providerSettingModel").focus();
    $("providerSettingModel").select();
    $("providerModelState").textContent = "手動輸入模型名稱；按「更新列表」可回到 Provider 模型清單。";
    return;
  }
  $("providerSettingModel").value = event.target.value;
});
$("providerSettingAPIKey").addEventListener("input", (event) => {
  if (event.target.value) $("providerSettingClearKey").checked = false;
});
$("providerSettingClearKey").addEventListener("change", (event) => {
  if (event.target.checked) $("providerSettingAPIKey").value = "";
});
$("refreshTools").addEventListener("click", loadTools);
$("refreshAudit").addEventListener("click", () => loadAudit(true));
$("loadMoreAudit").addEventListener("click", () => loadAudit(false));
document.addEventListener("pointerdown", (event) => {
  if (!$("projectContextMenu").contains(event.target)) closeProjectContextMenu();
  if (!$("sessionContextMenu").contains(event.target)) closeSessionContextMenu();
  if (!$("resourceContextMenu").contains(event.target)) closeResourceContextMenu();
  if (!$("providerUsagePopover").contains(event.target) && !$("providerUsageButton").contains(event.target)) closeProviderUsagePopover();
  if (!$("notificationPopover").contains(event.target) && !$("notificationButton").contains(event.target)) closeNotificationPopover();
  if (!$("sessionRuntimeControls").contains(event.target) && !$("modelControlButton").contains(event.target)) closeModelPopover();
  if (!$("globalSearchPopover").contains(event.target) && !$("globalSearchInput").contains(event.target) && !$("globalSearchButton").contains(event.target)) $("globalSearchPopover").classList.add("hidden");
  if (!$("contextUsagePopover").contains(event.target) && !$("contextUsageButton").contains(event.target)) closeContextUsagePopover();
  if (!$("screenCaptureMenu").contains(event.target) && !$("screenCaptureOptions").contains(event.target)) closeScreenCaptureMenu();
  if (!$("imageEditorColorPalette").contains(event.target) && !$("imageEditorColorButton").contains(event.target)) closeImageEditorColorPalette();
});
document.addEventListener("click", (event) => {
  const resource = resourceFromElement(event.target);
  if (!resource) return;
  event.preventDefault();
  void openResource(resource, "open");
});
document.addEventListener("contextmenu", (event) => {
  const resource = resourceFromElement(event.target);
  if (resource) {
    showResourceContextMenu(event, resource);
    return;
  }
  closeResourceContextMenu();
  if (!event.target.closest?.(".project-head")) closeProjectContextMenu();
  if (!event.target.closest?.(".session-row")) closeSessionContextMenu();
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  closeProjectContextMenu();
  closeSessionContextMenu();
  closeResourceContextMenu();
  closeProviderUsagePopover();
  closeNotificationPopover();
  closeModelPopover();
  $("globalSearchPopover").classList.add("hidden");
  closeScreenCaptureMenu();
  closeImageEditorColorPalette();
});
document.addEventListener("scroll", () => {
  closeProjectContextMenu();
  closeSessionContextMenu();
  closeResourceContextMenu();
  closeModelPopover();
}, true);
window.addEventListener("resize", () => {
  closeProjectContextMenu();
  closeSessionContextMenu();
  closeResourceContextMenu();
  closeModelPopover();
});
window.addEventListener("blur", () => {
  closeProjectContextMenu();
  closeSessionContextMenu();
  closeResourceContextMenu();
  closeProviderUsagePopover();
  closeNotificationPopover();
  closeModelPopover();
});
window.addEventListener("nr-intern-language-change", () => {
  refreshOpenProviderUsage();
  renderContextUsage();
  renderSessionUsage();
  renderSchedules();
  if (state.restoreCandidates.length > 0) renderRestoreRunOptions({ preserveSelection: true });
  syncMCPToolListToggle();
  for (const block of document.querySelectorAll(".message-reasoning, .session-content-reasoning")) {
    updateReasoningSummary(block);
  }
});

setInterval(() => {
  if (state.backendHealthy && state.workspace && !$("scheduleDialog").open) void loadSchedules();
}, scheduleRefreshIntervalMilliseconds);
setInterval(() => {
  if (state.backendHealthy && notificationCenterEnabled()) void loadNotifications();
}, 30_000);

installDialogDragGuards();
renderSchedules();
notifyNativeStartupReady();
void initializeApplication();
setInterval(updateLiveReasoningDurations, 1000);
setInterval(refreshOpenProviderUsage, providerUsageUIRefreshIntervalMilliseconds);
setInterval(refreshReverseProxyInBackground, reverseProxyRefreshIntervalMilliseconds);
