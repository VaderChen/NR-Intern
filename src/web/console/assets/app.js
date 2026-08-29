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
  backendHealthy: false,
  backendLifecycleState: "stopped",
  refreshingBackend: false,
  running: false,
  runningSessionId: "",
  runActivityText: "",
  canceling: false,
  currentRunId: "",
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
  contextResource: null,
  renamingSession: null,
  inspectingSession: null,
  deletingSession: null,
  pendingAttachments: [],
  attachmentSessionId: "",
  runStartedAt: new Map(),
  sessionSelectionVersion: 0,
  sessionRuntimeSaving: false,
  messageAutoScroll: true,
  collapsedProjects: new Set(JSON.parse(localStorage.getItem("collapsedProjects") || "[]")),
};

const $ = (id) => document.getElementById(id);
const defaultServiceNames = Object.freeze({
  "zh-TW": "永不休息的實習生",
  en: "Tireless Intern",
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
const backendRefreshIntervalMilliseconds = 4000;
const backendStartupRefreshIntervalMilliseconds = 200;
const contextCompactionActivity = "本輪已達上下文上限，自動整理資料後將繼續處理。";
const promptComposition = {
  active: false,
  enterObserved: false,
  suppressNextEnter: false,
  resetTimer: null,
};
let chatDragDepth = 0;
let messageScrollFrame = 0;
let backendRefreshTimer = 0;
let backendFastPollUntil = Date.now() + 5000;
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
  return {
    service_name: serviceName,
    service_name_is_default: Boolean(serviceNameIsDefault),
    ui_language: normalizedUILanguage(value.ui_language ?? current.ui_language ?? "auto"),
    max_wall_clock_seconds: Number.isInteger(wallClockSeconds) && wallClockSeconds > 0 ? wallClockSeconds : 7200,
    max_tokens: Number.isInteger(maxTokens) && maxTokens >= 0 ? maxTokens : 0,
    max_tool_calls: Number.isInteger(maxToolCalls) && maxToolCalls >= 0 ? maxToolCalls : 0,
  };
}

function applyServiceSettings(value) {
  const settings = normalizedServiceSettings(value);
  state.serviceSettings = settings;
  applyUILanguage(settings.ui_language);
  applyServiceName(settings.service_name_is_default ? localizedDefaultServiceName() : settings.service_name);
  if ($("settingMaxWallClockMinutes")) $("settingMaxWallClockMinutes").value = String(Math.ceil(settings.max_wall_clock_seconds / 60));
  if ($("settingMaxTokens")) $("settingMaxTokens").value = String(settings.max_tokens);
  if ($("settingMaxToolCalls")) $("settingMaxToolCalls").value = String(settings.max_tool_calls);
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

function toast(message) {
  $("toast").textContent = message;
  $("toast").classList.remove("hidden");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => $("toast").classList.add("hidden"), 4200);
}

async function refreshBackend() {
  if (state.refreshingBackend) return;
  state.refreshingBackend = true;
  try {
    const status = await desktop("status");
    const wasHealthy = state.backendHealthy;
    state.backendHealthy = status.healthy;
    state.backendLifecycleState = status.state || "stopped";
    const label = status.healthy
      ? (status.owned ? "後端執行中" : "已連接外部後端")
      : ["starting", "running"].includes(status.state) ? "啟動中" : "後端未啟動";
    $("backendState").textContent = label;
    $("managementBackendState").textContent = label;
    $("backendURL").textContent = status.backend_url;
    $("backendDetail").textContent = status.last_error
      ? `錯誤：${status.last_error}`
      : status.pid ? `PID ${status.pid}` : status.owned ? "由桌面程式管理" : "";
    for (const id of ["statusDot", "managementStatusDot"]) {
      $(id).className = `dot ${status.healthy ? "ok" : "bad"}`;
    }
    $("stopBackend").disabled = !status.owned;
    $("restartBackend").disabled = !status.owned;
    $("startBackend").disabled = status.healthy || ["starting", "running"].includes(status.state);
    $("newWorkspace").disabled = !status.healthy || state.running;
    $("workspaceSelect").disabled = !status.healthy || state.running;
    $("newProject").disabled = !status.healthy || !state.workspace || state.running;
    $("openManagement").disabled = !status.healthy;
    if (status.healthy && (!wasHealthy || !state.agent)) await loadApplicationState();
    if (!status.healthy && wasHealthy) resetWorkspace();
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
  state.session = null;
  state.plans = [];
  state.planTab = "active";
  state.planEditingID = "";
  state.expandedPlanIDs.clear();
  state.planExpansionInitialized = false;
  state.activePlanID = "";
  state.planDragID = "";
  state.sessionRuntimeSaving = false;
  state.runningSessionId = "";
  state.runActivityText = "";
  state.retryableRunId = "";
  state.retryableSessionId = "";
  state.pendingApproval = null;
  state.pendingApprovalSessionId = "";
  state.liveMessage = null;
  state.runStartedAt.clear();
  $("sessionTitle").textContent = "選擇或建立對話";
  $("agentLabel").textContent = "後端目前離線";
  $("prompt").disabled = true;
  syncRunActionButton();
  $("workspaceSelect").replaceChildren();
  syncSessionRuntimeControls({ loadModels: false });
  syncPlanButton();
  $("workspaceSettings").classList.add("hidden");
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
  $("agentLabel").textContent = state.agent?.name || "尚無 Agent";
  renderProviderOptions();
  renderPermissionOptions();
  renderWorkspaceOptions();
  const storedID = sessionStorage.getItem("activeWorkspaceID");
  state.workspace = state.workspaces.find((item) => item.id === storedID) || state.workspaces[0] || null;
  if (state.workspace) {
    $("workspaceSelect").value = state.workspace.id;
    sessionStorage.setItem("activeWorkspaceID", state.workspace.id);
    syncWorkspaceSettings();
    void initializeWorkspaceModels(state.workspace.id);
  }
  if (!state.agent || !state.workspace) return;
  await Promise.all([loadProjects(), loadSessions()]);
  $("newProject").disabled = state.running;
  $("workspaceSelect").disabled = state.running;
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

  const projectList = $("projectList");
  projectList.replaceChildren();
  for (const project of state.projects) {
    const sessions = state.sessions.filter((session) => session.project_id === project.id && !session.pinned);
    projectList.append(projectNode(project, sessions));
  }
  const ungrouped = state.sessions.filter((session) => !session.project_id && !session.pinned);
  if (ungrouped.length > 0) projectList.append(projectNode(null, ungrouped));
  if (state.projects.length === 0 && ungrouped.length === 0 && state.backendHealthy) {
    const empty = document.createElement("p");
    empty.className = "navigation-empty";
    empty.textContent = "尚無專案，先建立一個專案。";
    projectList.append(empty);
  }
}

function projectNode(project, sessions) {
  const projectID = project?.id || "uncategorized";
  const group = document.createElement("section");
  group.className = "project-group";
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
  header.append(toggle);
  if (project) {
    const actions = document.createElement("div");
    actions.className = "project-row-actions";
    const newConversation = iconButton(chatBubbleIcon(), "在此專案建立對話", () => openSessionDialog(project.id));
    newConversation.classList.add("project-chat-button");
    newConversation.disabled = state.running || !state.backendHealthy || !state.agent;
    const manage = iconButton("⋯", "管理專案", () => manageProject(project));
    manage.classList.add("project-manage-button");
    manage.disabled = state.running || !state.backendHealthy;
    actions.append(manage, newConversation);
    header.append(actions);
  }
  const children = document.createElement("div");
  children.className = "project-sessions";
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
  const running = state.running && state.runningSessionId === session.id;
  row.className = `session-row ${state.session?.id === session.id ? "active" : ""} ${running ? "running" : ""}`;
  row.dataset.sessionId = session.id;
  row.addEventListener("contextmenu", (event) => openSessionContextMenu(event, session));
  const button = document.createElement("button");
  button.className = "session";
  const title = document.createElement("span");
  title.textContent = session.title || "未命名";
  const meta = document.createElement("small");
  const providerID = session.provider_id || state.workspace?.default_provider_id || "";
  meta.textContent = displayedModelForProvider(providerID, session.model)
    || session.permission_profile
    || "default";
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
  pin.disabled = state.running;
  if (pinnedContext) pin.classList.add("pinned");
  row.append(button, runIndicator, pin);
  return row;
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

function showSessionContextMenu(session, clientX, clientY, focusMenu) {
  const menu = $("sessionContextMenu");
  closeResourceContextMenu();
  state.contextSession = session;
  $("pinContextSessionLabel").textContent = session.pinned ? "取消釘選" : "釘選對話";
  for (const id of ["renameContextSession", "pinContextSession", "deleteContextSession"]) {
    $(id).disabled = state.running;
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

function renderProviderOptions() {
  for (const select of [$("newWorkspaceProvider"), $("settingWorkspaceProvider")]) {
    const selected = select.value;
    select.replaceChildren();
    for (const provider of selectableProviders()) {
      select.add(new Option(provider.id, provider.id));
    }
    if ([...select.options].some((option) => option.value === selected)) select.value = selected;
  }
}

function renderSessionProviderOptions() {
  for (const select of [$("newProvider"), $("settingProvider")]) {
    const selected = select.value;
    const workspaceDefault = state.workspace?.default_provider_id || "";
    select.replaceChildren(new Option(workspaceDefault ? `使用 Workspace 預設（${workspaceDefault}）` : "使用 Workspace 預設", ""));
    for (const provider of selectableProviders()) {
      select.add(new Option(provider.id, provider.id));
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

function syncSessionRuntimeControlState() {
  const disabled = !state.session || selectedSessionIsRunning() || state.sessionRuntimeSaving;
  const providerID = state.session?.provider_id || state.workspace?.default_provider_id || "";
  $("sessionProviderSelect").disabled = disabled;
  $("sessionModelSelect").disabled = disabled || sessionRuntimeModels(providerID).length === 0;
  $("sessionRuntimeControls").setAttribute("aria-busy", state.sessionRuntimeSaving ? "true" : "false");
}

function syncSessionRuntimeControls({ loadModels = true } = {}) {
  const controls = $("sessionRuntimeControls");
  const session = state.session;
  controls.classList.toggle("hidden", !session);
  if (!session) {
    $("sessionProviderSelect").replaceChildren();
    $("sessionModelSelect").replaceChildren();
    syncSessionRuntimeControlState();
    return;
  }

  const providerID = session.provider_id || state.workspace?.default_provider_id || "";
  const providerSelect = $("sessionProviderSelect");
  providerSelect.replaceChildren();
  for (const provider of selectableProviders()) {
    providerSelect.add(new Option(provider.id, provider.id));
  }
  providerSelect.value = providerID;

  const model = session.model || defaultModelForProvider(providerID);
  const catalog = providerModelCatalog(providerID);
  const models = catalog || [];
  const modelSelect = $("sessionModelSelect");
  modelSelect.replaceChildren();
  for (const value of models) modelSelect.add(new Option(value, value));
  if (catalog === null) {
    modelSelect.add(new Option(state.providerModelErrors[providerID] ? "無法取得模型列表" : "正在載入模型…", ""));
  } else if (models.length === 0) {
    modelSelect.add(new Option("沒有可用模型", ""));
  }
  const validModel = models.includes(model);
  modelSelect.value = validModel ? model : models[0] || "";
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
    toast(error.message);
    return null;
  } finally {
    state.sessionRuntimeSaving = false;
    syncSessionRuntimeControlState();
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

function renderWorkspaceOptions() {
  const select = $("workspaceSelect");
  select.replaceChildren();
  for (const workspace of state.workspaces) {
    select.add(new Option(workspace.name, workspace.id));
  }
}

async function switchWorkspace(workspaceID) {
  if (state.running) {
    $("workspaceSelect").value = state.workspace?.id || "";
    toast("Run 執行中，完成或取消後才能切換 Workspace");
    return;
  }
  const workspace = state.workspaces.find((item) => item.id === workspaceID);
  if (!workspace || workspace.id === state.workspace?.id) return;
  state.workspace = workspace;
  sessionStorage.setItem("activeWorkspaceID", workspace.id);
  state.projects = [];
  state.sessions = [];
  state.session = null;
  clearPendingAttachments();
  clearSessionUI();
  syncWorkspaceSettings();
  void initializeWorkspaceModels(workspace.id);
  renderNavigation();
  try {
    await Promise.all([loadProjects(), loadSessions()]);
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
  $("sessionTitle").textContent = state.workspace?.name || "選擇或建立對話";
  const workspaceModel = displayedModelForProvider(state.workspace?.default_provider_id, state.workspace?.model);
  $("agentLabel").textContent = state.workspace
    ? `${state.workspace.default_provider_id}${workspaceModel ? ` · ${workspaceModel}` : ""}`
    : state.agent?.name || "General Harness Agent";
  syncSessionRuntimeControls({ loadModels: false });
  syncPlanButton();
  $("sessionPermission").classList.add("hidden");
  $("prompt").disabled = true;
  syncRunActionButton();
  $("messages").replaceChildren();
  $("messages").classList.add("hidden");
  $("emptyState").classList.remove("hidden");
  $("sessionSettings").classList.add("hidden");
  $("noSessionManagement").classList.remove("hidden");
}

function syncWorkspaceSettings() {
  const workspace = state.workspace;
  $("workspaceSettings").classList.toggle("hidden", !workspace);
  if (!workspace) return;
  renderProviderOptions();
  renderSessionProviderOptions();
  $("settingWorkspaceName").value = workspace.name || "";
  $("settingWorkspaceDescription").value = workspace.description || "";
  $("settingWorkspaceProvider").value = workspace.default_provider_id || "";
  $("settingWorkspaceModel").value = workspace.model || "";
  if (!state.session) clearSessionUI();
}

async function selectSession(session) {
  const selectionVersion = ++state.sessionSelectionVersion;
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
    syncSessionUI();
    renderNavigation();
    await Promise.all([loadMessages(), loadPlans(selected.id)]);
    if (selectionVersion !== state.sessionSelectionVersion) return;
    syncSelectedRunUI();
  } catch (error) {
    if (selectionVersion !== state.sessionSelectionVersion) return;
    toast(error.message);
  }
}

function syncSessionUI() {
  const session = state.session;
  if (!session) return;
  $("sessionTitle").textContent = session.title || "未命名";
  const providerID = session.provider_id || state.workspace?.default_provider_id || "";
  const model = displayedModelForProvider(providerID, session.model);
  $("agentLabel").textContent = `${providerID}${model ? ` · ${model}` : ""}`;
  $("sessionPermission").textContent = session.permission_profile || "default";
  $("sessionPermission").classList.remove("hidden");
  syncSessionRuntimeControls();
  syncPlanButton();
  syncSelectedRunUI();
  $("sessionSettings").classList.remove("hidden");
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
  return Boolean(state.running && sessionID && state.runningSessionId === sessionID);
}

async function loadPlans(sessionID = state.session?.id) {
  if (!sessionID) return;
  const values = await request(`/api/v1/sessions/${encodeURIComponent(sessionID)}/plans`);
  if (state.session?.id !== sessionID) return;
  state.plans = Array.isArray(values) ? values : [];
  const active = state.plans.find((plan) => plan.status === "active");
  if (!state.planExpansionInitialized) {
    state.expandedPlanIDs.clear();
    if (active) state.expandedPlanIDs.add(active.id);
    state.planExpansionInitialized = true;
  } else if (active && active.id !== state.activePlanID) {
    state.expandedPlanIDs.add(active.id);
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
  $("planListHint").textContent = showingCompleted ? "已完成與已取消的計畫" : "拖曳計畫可調整執行順序";
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

function renderPlanCard(plan, index, visiblePlanCount) {
  const locked = sessionRunIsActive();
  const terminal = terminalPlanStatuses.has(plan.status);
  const sortable = !terminal && state.planTab === "active";
  const expanded = state.expandedPlanIDs.has(plan.id);
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
  header.append(handle, order, summary, status, quickActions, toggle);
  card.append(header);

  const details = document.createElement("div");
  details.className = "plan-card-details";
  details.classList.toggle("hidden", !expanded);
  const steps = document.createElement("ol");
  steps.className = "plan-step-list";
  for (const step of plan.steps) steps.append(renderPlanStep(step));
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
  details.append(steps, actions);
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
  if (state.planEditingID && !window.confirm("重建計畫會清除這份計畫目前的步驟進度，確定繼續嗎？")) return;
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
  if (!window.confirm("確定刪除這份計畫嗎？")) return;
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
      if (appendMessage(entry.message, { operationId: activeOperationID })) visibleEntries += 1;
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
  scrollMessages({ force: true });
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
  const reasoning = !existing ? tagged.reasoning
    : !tagged.reasoning || tagged.reasoning === existing ? existing
    : `${existing}\n\n${tagged.reasoning}`;
  return { ...message, content: tagged.content, reasoning };
}

function appendMessage(message, options = {}) {
  if (!message) return null;
  message = normalizeAssistantThinking(message);
  if (!message.content && !message.reasoning && message.role !== "assistant") return null;
  // tool 與內部階段訊息是 Harness transcript，不是對使用者顯示的聊天內容。
  // 它們仍保留在後端稽核與下一輪 LLM context 中。
  if (message.role === "tool") return null;
  const internalAssistant = message.role === "assistant"
    && (message.metadata?.internal === true || (Array.isArray(message.tool_calls) && message.tool_calls.length > 0));
  if (internalAssistant && !message.reasoning) return null;
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
    const copy = iconButton(messageCopyIcon(), "複製訊息", () => copyMessage(message.content));
    copy.classList.add("message-action-button");
    actions.append(copy);
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
  const block = document.createElement("details");
  block.className = className;
  const summary = document.createElement("summary");
  summary.className = "reasoning-summary";
  summary.textContent = "處理過程";
  const content = document.createElement("div");
  content.className = "reasoning-content";
  renderRichContent(content, normalizeReasoningMarkdown(value));
  block.append(summary, content);
  block.classList.toggle("hidden", !value);
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
  const source = normalizeReasoningMarkdown(append ? `${richContentSource(content)}${value}` : value);
  if (append) scheduleRichContent(content, source);
  else renderRichContent(content, source);
  block.classList.remove("hidden");
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
  if (addition) setMessageReasoning(previous, `\n\n${addition}`, true);
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
    const summary = node.querySelector(".reasoning-summary");
    if (summary) summary.textContent = `處理過程 · ${formatProcessingDuration(durationMilliseconds)}`;
    const block = node.querySelector(".message-reasoning");
    if (block) block.dataset.durationMilliseconds = String(Math.round(durationMilliseconds));
  }
}

function updateLiveReasoningDuration(operationID, now = Date.now()) {
  const startedAt = state.runStartedAt.get(operationID);
  if (!operationID || !Number.isFinite(startedAt) || !Number.isFinite(now) || now < startedAt) return;
  const label = `處理過程 · ${formatProcessingDuration(now - startedAt)}`;
  for (const node of $("messages").querySelectorAll(".message.assistant[data-operation-id]")) {
    if (node.dataset.operationId !== operationID) continue;
    const block = node.querySelector(".message-reasoning:not(.hidden)");
    const summary = block?.querySelector(".reasoning-summary");
    if (summary) summary.textContent = label;
  }
}

function updateLiveReasoningDurations() {
  if (state.runStartedAt.size === 0) return;
  const now = Date.now();
  for (const operationID of state.runStartedAt.keys()) updateLiveReasoningDuration(operationID, now);
}

function formatProcessingDuration(durationMilliseconds) {
  const totalSeconds = Math.max(1, Math.round(durationMilliseconds / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}分${String(seconds).padStart(2, "0")}秒`;
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
    && !state.running
    && (!state.retryableSessionId || state.retryableSessionId === state.session?.id);
  $("retryRun").classList.toggle("hidden", !retryVisible);
}

function selectedSessionIsRunning() {
  return Boolean(state.running && state.session?.id && state.session.id === state.runningSessionId);
}

function ensureRunDraft(sessionID, operationID = "", messageID = "") {
  const current = state.runDraft;
  const startsNewMessage = Boolean(messageID && current?.messageId && current.messageId !== messageID);
  if (!current || current.sessionId !== sessionID || startsNewMessage) {
    state.runDraft = {
      sessionId: sessionID,
      operationId: operationID,
      messageId: messageID,
      content: "",
      reasoning: "",
      processing: true,
    };
    return state.runDraft;
  }
  if (operationID) current.operationId = operationID;
  if (messageID) current.messageId = messageID;
  return current;
}

function renderSelectedRunDraft() {
  if (!selectedSessionIsRunning()) return null;
  const draft = ensureRunDraft(state.runningSessionId, state.currentRunId);
  const messageID = draft.messageId || `active-run-${draft.operationId || draft.sessionId}`;
  let messageNode = findMessage(messageID);
  if (!messageNode
    && state.liveMessage?.isConnected
    && state.liveMessage.dataset.runDraftPlaceholder === "true") {
    messageNode = state.liveMessage;
    messageNode.dataset.messageId = messageID;
  }
  if (!messageNode) {
    messageNode = appendMessage({ role: "assistant", id: messageID, content: "" }, { operationId: draft.operationId });
  }
  if (!messageNode) return null;
  messageNode.dataset.runDraftPlaceholder = draft.messageId ? "false" : "true";
  if (draft.operationId) messageNode.dataset.operationId = draft.operationId;
  const content = messageNode.querySelector(".content");
  if (content && richContentSource(content) !== draft.content) scheduleRichContent(content, draft.content);
  if (draft.reasoning) setMessageReasoning(messageNode, draft.reasoning);
  setAgentProcessing(messageNode, draft.processing);
  state.liveMessage = messageNode;
  $("emptyState").classList.add("hidden");
  $("messages").classList.remove("hidden");
  return messageNode;
}

function setRunActivity(text, sessionID = state.runningSessionId) {
  if (!sessionID || sessionID !== state.runningSessionId) return;
  state.runActivityText = text || "";
  if (state.session?.id === sessionID) showActivity(state.runActivityText);
}

function syncSelectedRunUI() {
  const selectedRunning = selectedSessionIsRunning();
  $("prompt").disabled = !state.session || state.running;
	syncSessionRuntimeControlState();
	syncRunActionButton();
	if (selectedRunning) {
		// 一般執行狀態由訊息區動畫與停止按鈕表達；這一列只保留給斷線
		// 重連等需要使用者注意的狀態。
		showActivity(state.runActivityText);
    renderSelectedRunDraft();
    if (state.pendingApproval && state.pendingApprovalSessionId === state.session?.id && !$("approvalDialog").open) {
      $("approvalDialog").show();
    }
    return;
  }
  if ($("approvalDialog").open && state.pendingApprovalSessionId !== state.session?.id) $("approvalDialog").close();
  if (state.retryableRunId && (!state.retryableSessionId || state.retryableSessionId === state.session?.id)) {
    showActivity("Run 已中斷，可用原始輸入建立重試");
    return;
  }
  showActivity("");
}

function syncRunActionButton() {
  const button = $("send");
  if (!button) return;
  const stopping = selectedSessionIsRunning();
  const backgroundRun = state.running && !stopping;
  const label = stopping ? "停止 Run" : "送出訊息";
  button.classList.toggle("is-stop", stopping);
  $("sendIcon").classList.toggle("hidden", stopping);
  $("stopIcon").classList.toggle("hidden", !stopping);
  button.setAttribute("aria-label", label);
  button.title = label;
  button.disabled = stopping
    ? !state.currentRunId || state.canceling
    : !state.session || backgroundRun;
  syncPlanButton();
}

function activateRunAction() {
  if (selectedSessionIsRunning()) {
    void cancelCurrentRun();
    return;
  }
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
  if (!state.session || state.running) return;
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

function dataTransferHasFiles(dataTransfer) {
  return [...(dataTransfer?.types || [])].includes("Files") || (dataTransfer?.files?.length || 0) > 0;
}

async function sendPrompt(event) {
  event.preventDefault();
  let input = normalizeFullwidthASCII($("prompt").value).trim();
  const pending = [...state.pendingAttachments];
  if ((!input && pending.length === 0) || !state.session || state.running) return;
  if (!input) input = "請檢視附件。";
  const sessionID = state.session.id;
  state.running = true;
  state.runningSessionId = sessionID;
	state.runActivityText = "";
  state.canceling = false;
  state.currentRunId = "";
  state.retryableRunId = "";
  state.retryableSessionId = "";
  state.pendingApproval = null;
  state.pendingApprovalSessionId = "";
  state.runDraft = {
    sessionId: sessionID,
    operationId: "",
    messageId: "",
    content: "",
    reasoning: "",
    processing: true,
  };
  syncRunActionButton();
  $("prompt").disabled = true;
  $("newProject").disabled = true;
  $("newWorkspace").disabled = true;
  $("workspaceSelect").disabled = true;
  $("emptyState").classList.add("hidden");
  $("messages").classList.remove("hidden");
  renderNavigation();
  try {
    const attachments = await uploadPendingAttachments(sessionID, pending);
    $("prompt").value = "";
    if (pending.length) clearPendingAttachments();
    appendMessage({ role: "user", content: input, metadata: { attachments }, id: `local-${Date.now()}` });
    renderSelectedRunDraft();
    scrollMessages({ force: true });
    await runWithReconnect({
      sessionId: sessionID,
      input,
      attachmentIds: attachments.map((attachment) => attachment.id),
      idempotencyKey: crypto.randomUUID(),
    });
  } catch (error) {
    toast(error.message);
  } finally {
    const finishedSessionID = state.runningSessionId;
    if (state.currentRunId) state.runStartedAt.delete(state.currentRunId);
    state.running = false;
    state.runningSessionId = "";
    state.runActivityText = "";
    state.canceling = false;
    state.currentRunId = "";
    setAgentProcessing(state.liveMessage, false);
    state.liveMessage = null;
    state.runDraft = null;
    state.pendingApproval = null;
    state.pendingApprovalSessionId = "";
    if ($("approvalDialog").open) $("approvalDialog").close();
    syncSelectedRunUI();
    $("newProject").disabled = !state.backendHealthy || !state.workspace;
    $("newWorkspace").disabled = !state.backendHealthy;
    $("workspaceSelect").disabled = !state.backendHealthy;
    renderNavigation();
    if (state.session?.id === finishedSessionID) await loadMessages().catch(() => {});
    await loadSessions().catch(() => {});
  }
}

async function runWithReconnect(task) {
  const sessionID = task.sessionId || state.runningSessionId;
  const progress = { runId: task.runId || "", lastSequence: 0, terminal: false };
  let reconnects = 0;
  while (!progress.terminal) {
    try {
      const response = await openRunStream(task, progress);
      progress.runId = response.headers.get("X-Run-ID") || progress.runId;
      if (!progress.runId) throw new Error("後端未回傳 Run ID，無法安全重連");
      state.currentRunId = progress.runId;
      syncRunActionButton();
			setRunActivity("", sessionID);
      await consumeEvents(response.body, progress, sessionID);
      if (progress.terminal) return;
      const run = await request(`/api/v1/runs/${encodeURIComponent(progress.runId)}`);
      if (["completed", "failed", "canceled"].includes(run.status)) {
        handleTerminalRun(run, sessionID);
        progress.terminal = true;
        return;
      }
      throw new Error("事件連線提前結束");
    } catch (error) {
      if (progress.terminal) return;
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
  const response = await fetch(path, reconnect ? { headers } : {
    method: "POST",
    headers,
    body: JSON.stringify({ input: task.input, attachment_ids: task.attachmentIds || [] }),
  });
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
      if (["run.completed", "run.failed", "run.canceled"].includes(event.type)) progress.terminal = true;
    }
  }
}

function handleTerminalRun(run, sessionID) {
  const visible = state.session?.id === sessionID;
  const startedAt = Date.parse(run.started_at);
  const completedAt = Date.parse(run.completed_at);
  if (visible && Number.isFinite(startedAt) && Number.isFinite(completedAt)) {
    finalizeReasoningGroup(run.id, completedAt - startedAt);
  }
  state.runStartedAt.delete(run.id);
  if (visible) setAgentProcessing(state.liveMessage, false);
  if (run.status === "failed" || run.status === "canceled") {
    if (run.error?.retryable) {
      state.retryableRunId = run.id;
      state.retryableSessionId = sessionID;
    }
    if (visible) {
      appendRunOutcome({ id: run.id, status: run.status, error: run.error });
      scrollMessages();
    }
    toast(run.error?.message || `Run ${run.status}`);
  }
}

function handleEvent(event, sessionID) {
  const payload = event.payload || {};
  const visible = state.session?.id === sessionID;
	if (visible && event.type === "tool.execution.end" && String(payload.result?.tool_name || "").startsWith("plan_")) {
	  loadPlans(sessionID).catch(() => {});
	}
	if (visible && event.type === "plan.completion_check") loadPlans(sessionID).catch(() => {});
	const operationID = String(event.run_id || state.currentRunId || "");
	if (event.type === "run.started" && operationID && !state.runStartedAt.has(operationID)) {
	  const startedAt = Date.parse(event.created_at);
	  state.runStartedAt.set(operationID, Number.isFinite(startedAt) ? startedAt : Date.now());
	  const draft = ensureRunDraft(sessionID, operationID);
	  draft.processing = true;
	  if (visible) renderSelectedRunDraft();
	}
	if (visible && event.type === "context.compacted") {
	  setRunActivity(contextCompactionActivity, sessionID);
	} else if (visible
	  && state.runActivityText === contextCompactionActivity
	  && ["message.delta", "message.thinking_delta", "message.end", "tool.execution.start", "tool.execution.end"].includes(event.type)) {
	  setRunActivity("", sessionID);
	}
	if (event.type === "message.start" && payload.message?.role === "assistant") {
    const message = normalizeAssistantThinking(payload.message);
    const draft = ensureRunDraft(sessionID, operationID, message.id);
    draft.content = message.content || "";
    draft.reasoning = message.reasoning || "";
    draft.processing = true;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "message.delta") {
    const draft = ensureRunDraft(sessionID, operationID, payload.message_id);
    draft.content += payload.delta || "";
    if (payload.delta) draft.processing = false;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "message.thinking_delta") {
    const draft = ensureRunDraft(sessionID, operationID, payload.message_id);
    draft.reasoning += payload.delta || "";
    if (payload.delta) draft.processing = false;
    if (visible) {
      renderSelectedRunDraft();
      scrollMessages();
    }
  } else if (event.type === "message.end" && payload.message?.role === "assistant") {
    const message = normalizeAssistantThinking(payload.message);
    const draft = ensureRunDraft(sessionID, operationID, message.id);
    draft.content = message.content || draft.content;
    draft.reasoning = message.reasoning || draft.reasoning;
    draft.processing = false;
    if (!visible) {
      if (message.metadata?.internal === true || (Array.isArray(message.tool_calls) && message.tool_calls.length > 0)) state.runDraft = null;
      return;
    }
    state.liveMessage = renderSelectedRunDraft() || findMessage(message.id) || state.liveMessage;
    if (state.liveMessage && operationID) state.liveMessage.dataset.operationId = operationID;
    if (message.metadata?.internal === true || (Array.isArray(message.tool_calls) && message.tool_calls.length > 0)) {
      if (message.reasoning && state.liveMessage) {
        setMessageReasoning(state.liveMessage, message.reasoning);
        state.liveMessage.classList.add("reasoning-only");
        renderRichContent(state.liveMessage.querySelector(".content"), "");
        setAgentProcessing(state.liveMessage, false);
        mergeReasoningIntoPrevious(state.liveMessage, operationID);
      } else {
        state.liveMessage?.remove();
      }
      state.liveMessage = null;
      state.runDraft = null;
    } else {
      if (state.liveMessage) {
        const content = state.liveMessage.querySelector(".content");
        renderRichContent(content, message.content || richContentSource(content));
        if (message.reasoning) setMessageReasoning(state.liveMessage, message.reasoning);
        if (message.reasoning) mergeReasoningIntoPrevious(state.liveMessage, operationID);
      }
      setAgentProcessing(state.liveMessage, false);
    }
	} else if (event.type === "run.approval_required" && payload.approval) {
    showApproval(payload.approval, sessionID);
  } else if (event.type === "run.approval_resolved") {
    state.pendingApproval = null;
    state.pendingApprovalSessionId = "";
    if (visible && $("approvalDialog").open) $("approvalDialog").close();
		setRunActivity("", sessionID);
	} else if (event.type === "run.completed") {
    finalizeLiveReasoningDuration(event, operationID, visible);
    if (visible) setAgentProcessing(state.liveMessage, false);
    if (visible) loadPlans(sessionID).catch(() => {});
  } else if (event.type === "run.failed" || event.type === "run.canceled") {
    finalizeLiveReasoningDuration(event, operationID, visible);
    if (visible) setAgentProcessing(state.liveMessage, false);
    if (payload.error?.retryable) {
      state.retryableRunId = state.currentRunId;
      state.retryableSessionId = sessionID;
    }
    if (visible) {
      appendRunOutcome({
        id: state.currentRunId,
        status: event.type === "run.canceled" ? "canceled" : "failed",
        error: payload.error,
      });
      scrollMessages();
    }
    toast(payload.error?.message || event.type);
  }
}

function finalizeLiveReasoningDuration(event, operationID, visible) {
  const startedAt = state.runStartedAt.get(operationID);
  const completedAt = Date.parse(event.created_at);
  if (visible && Number.isFinite(startedAt) && Number.isFinite(completedAt)) {
    finalizeReasoningGroup(operationID, completedAt - startedAt);
  }
  state.runStartedAt.delete(operationID);
}

async function retryCurrentRun() {
  const sourceRunId = state.retryableRunId;
  if (!sourceRunId || state.running) return;
  const sessionID = state.retryableSessionId || state.session?.id;
  if (!sessionID) return;
  state.running = true;
  state.runningSessionId = sessionID;
	state.runActivityText = "";
  state.canceling = false;
  state.retryableRunId = "";
  state.retryableSessionId = "";
  state.runDraft = {
    sessionId: sessionID,
    operationId: "",
    messageId: "",
    content: "",
    reasoning: "",
    processing: true,
  };
  $("prompt").disabled = true;
  syncRunActionButton();
  $("retryRun").disabled = true;
  renderSelectedRunDraft();
  scrollMessages({ force: true });
  try {
    const run = await request(`/api/v1/runs/${encodeURIComponent(sourceRunId)}/retry`, {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: "{}",
    });
    state.currentRunId = run.id;
    syncRunActionButton();
		setRunActivity("", sessionID);
    await runWithReconnect({ runId: run.id, sessionId: sessionID });
  } catch (error) {
    state.retryableRunId = sourceRunId;
    state.retryableSessionId = sessionID;
    toast(error.message);
  } finally {
    if (state.currentRunId) state.runStartedAt.delete(state.currentRunId);
    state.running = false;
    state.runningSessionId = "";
    state.runActivityText = "";
    state.canceling = false;
    state.currentRunId = "";
    setAgentProcessing(state.liveMessage, false);
    state.liveMessage = null;
    state.runDraft = null;
    $("retryRun").disabled = false;
    syncSelectedRunUI();
    renderNavigation();
    if (state.session?.id === sessionID) await loadMessages().catch(() => {});
    await loadSessions().catch(() => {});
  }
}

function showApproval(approval, sessionID = state.runningSessionId) {
  state.pendingApproval = approval;
  state.pendingApprovalSessionId = sessionID;
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
  const approval = state.pendingApproval;
  if (!approval || !state.currentRunId) return;
  $("approveTool").disabled = true;
  $("denyTool").disabled = true;
  $("permanentApproval").disabled = true;
  const permanent = decision === "approve" && $("permanentApproval").checked;
  try {
    await request(`/api/v1/runs/${encodeURIComponent(state.currentRunId)}/decision`, {
      method: "POST",
      body: JSON.stringify({
        approval_id: approval.id,
        decision,
        reason: $("approvalDecisionReason").value.trim(),
        permanent,
      }),
    });
		setRunActivity("", state.pendingApprovalSessionId);
  } catch (error) {
    toast(error.message);
    $("approveTool").disabled = false;
    $("denyTool").disabled = false;
    $("permanentApproval").disabled = false;
  }
}

async function cancelCurrentRun() {
  if (!state.currentRunId || state.canceling) return;
  state.canceling = true;
  syncRunActionButton();
  try {
    await request(`/api/v1/runs/${encodeURIComponent(state.currentRunId)}/cancel`, { method: "POST", body: "{}" });
  } catch (error) {
    state.canceling = false;
    syncRunActionButton();
    toast(error.message);
  }
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
  if (!session || state.running) return;
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
  if (!session || !title || state.running) return;
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
  $("sessionContentMeta").textContent = `${state.inspectingSession?.id || ""} · ${messages.length} 則訊息`;
  if (messages.length === 0) {
    const empty = document.createElement("p");
    empty.className = "quiet session-content-state";
    empty.textContent = "這個對話目前沒有內容。";
    list.append(empty);
    return;
  }
  list.append(...messages.map(sessionContentNode));
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
  if (!session || state.running) return;
  state.deletingSession = session;
  $("deleteSessionMessage").textContent = `確定要刪除對話「${session.title || "未命名"}」嗎？`;
  $("deleteSessionDialog").showModal();
  $("cancelDeleteSession").focus();
}

async function deleteSession(event) {
  event.preventDefault();
  const session = state.deletingSession;
  if (!session || state.running) return;
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

function resetProjectForm() {
  $("projectForm").reset();
  state.newProjectSandboxRoots = [];
  renderProjectSandboxRoots("create");
}

function projectSandboxEditor(mode) {
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
  if (mode === "settings") state.editProjectSandboxRoots = roots;
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
    $("workspaceSelect").value = workspace.id;
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
      }),
    });
    state.workspace = updated;
    state.workspaces = state.workspaces.map((item) => item.id === updated.id ? updated : item);
    renderWorkspaceOptions();
    $("workspaceSelect").value = updated.id;
    syncWorkspaceSettings();
    $("workspaceSettingsState").textContent = "已儲存";
  } catch (error) {
    $("workspaceSettingsState").textContent = "儲存失敗";
    toast(error.message);
  } finally {
    $("saveWorkspaceSettings").disabled = false;
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
  if (!project || !window.confirm(`確定刪除專案「${project.name}」？含有對話時後端會拒絕。`)) return;
  $("deleteProjectSettings").disabled = true;
  try {
    await request(`/api/v1/projects/${encodeURIComponent(project.id)}`, { method: "DELETE" });
    closeProjectSettings();
    await loadProjects();
  } catch (error) {
    toast(error.message);
  } finally {
    $("deleteProjectSettings").disabled = false;
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
    toast(error.message);
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
  for (const value of ["overview", "providers", "tools", "audit"]) $(`${value}Panel`).classList.toggle("hidden", value !== name);
  if (name === "overview") await loadDiagnostics();
  if (name === "providers") await loadProviderSettings();
  if (name === "tools") await loadTools();
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
    appendSummary(summary, "Provider", activeProvider.id || "—");
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
    const settings = provider.openai_compatible || {};
    const button = document.createElement("button");
    button.type = "button";
    const enabled = provider.enabled !== false;
    button.className = `provider-setting-card ${enabled ? "" : "provider-disabled"} ${!state.providerSettingsDraft && provider.id === state.selectedProviderSettingsID ? "active" : ""}`;
    const title = document.createElement("strong");
    title.textContent = provider.id;
    const detail = document.createElement("small");
    detail.textContent = `${settings.model || "未設定模型"} · ${settings.base_url || "未設定 URL"}`;
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
  const settings = selected.openai_compatible || {};
  $("providerSettingsEditorTitle").textContent = isNew ? "新增 Provider" : `編輯 ${selected.id}`;
  $("providerSettingsState").textContent = "";
  $("providerTestState").textContent = "";
  $("providerSettingID").value = selected.id || "";
  $("providerSettingID").disabled = !isNew;
  $("providerSettingType").value = selected.type || "openai-compatible";
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
}

function renderProviderModelOptions(providerID, preferredModel = "") {
  // 管理頁可保留「實際測試成功」的模型供設定；對話頂部選單仍只讀取
  // providerModelLists，也就是 Provider /models 的正式目錄。
  const models = [...new Set([
    ...(state.providerModelLists[providerID] || []),
    ...(state.providerTestedModels[providerID] || []),
  ].map((model) => String(model).trim()).filter(Boolean))];
  const input = $("providerSettingModel");
  const catalog = $("providerSettingModelCatalog");
  catalog.replaceChildren();
  if (models.length > 0) {
    const currentModel = input.value.trim();
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
    catalog.value = selectedModel;
    input.classList.add("hidden");
    catalog.classList.remove("hidden");
  } else {
    catalog.classList.add("hidden");
    input.classList.remove("hidden");
  }
  if (!providerID) {
    $("providerModelState").textContent = "儲存 Provider 後會自動更新模型列表；模型名稱仍可手動輸入。";
  } else if (state.providerModelLists[providerID]) {
    $("providerModelState").textContent = models.length > 0
      ? `已載入 ${models.length} 個模型，可從輸入欄位選取。`
      : "Provider 回傳空模型列表，仍可手動輸入模型名稱。";
  } else {
    $("providerModelState").textContent = "模型列表尚未載入，仍可手動輸入模型名稱。";
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
        : "無法讀取模型列表，仍可手動輸入模型名稱。";
    }
    if (notify) toast(error.message);
    return null;
  } finally {
    if (!state.providerSettingsDraft && state.selectedProviderSettingsID === providerID) $("refreshProviderModels").disabled = false;
  }
}

function providerSettingFormValue() {
  const provider = {
    id: $("providerSettingID").value.trim(),
    type: $("providerSettingType").value,
    enabled: $("providerSettingDefault").checked || $("providerSettingEnabled").checked,
    openai_compatible: {
      base_url: $("providerSettingBaseURL").value.trim(),
      model: $("providerSettingModel").value.trim(),
      instruction_role: $("providerSettingInstructionRole").value,
      disable_streaming: $("providerSettingDisableStreaming").checked,
      stream_include_usage: $("providerSettingStreamUsage").checked,
      omit_tool_choice: $("providerSettingOmitToolChoice").checked,
      max_attempts: Number($("providerSettingMaxAttempts").value),
      timeout_seconds: Number($("providerSettingTimeout").value),
      connect_timeout_seconds: Number($("providerSettingConnectTimeout").value),
      response_header_timeout_seconds: Number($("providerSettingHeaderTimeout").value),
      context_window: Number($("providerSettingContextWindow").value) || 0,
      max_output_tokens: Number($("providerSettingMaxOutputTokens").value) || 0,
    },
  };
  const apiKey = $("providerSettingAPIKey").value.trim();
  if (apiKey) provider.openai_compatible.api_key = apiKey;
  if ($("providerSettingClearKey").checked) provider.openai_compatible.api_key = "";
  return provider;
}

function providerSettingsPayload(providers, defaultProviderID) {
  return {
    default_provider_id: defaultProviderID,
    providers: providers.map((provider) => {
      const value = JSON.parse(JSON.stringify(provider));
      if (value.openai_compatible) delete value.openai_compatible.has_api_key;
      return value;
    }),
  };
}

async function persistProviderSetting({ showToast = true, refreshModels = true } = {}) {
  if (!state.providerSettings) return;
  if (!$("providerSettingsForm").reportValidity()) return null;
  const provider = providerSettingFormValue();
  const isNew = Boolean(state.providerSettingsDraft);
  if (!provider.id || !provider.openai_compatible.base_url || !provider.openai_compatible.model) return;
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
        : "Provider 設定已儲存；模型名稱仍可手動輸入");
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
    if (result.warning) $("providerModelState").textContent = "模型目錄無法讀取；已加入並選取本次測試成功的模型。";
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
  if (!window.confirm(`確定刪除 Provider「${id}」？`)) return;
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

async function refreshProviderCatalog() {
  state.providers = await request("/api/v1/providers");
  renderProviderOptions();
  renderSessionProviderOptions();
  syncWorkspaceSettings();
  if (state.session) syncSessionUI();
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

$("startBackend").addEventListener("click", () => controlBackend("start"));
$("restartBackend").addEventListener("click", () => controlBackend("restart"));
$("stopBackend").addEventListener("click", () => controlBackend("stop"));
$("displayMode").addEventListener("change", (event) => applyDisplayMode(event.target.value));
$("openPlan").addEventListener("click", openPlanDialog);
$("closePlan").addEventListener("click", () => $("planDialog").close());
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
$("createPlanFromEmpty").addEventListener("click", () => editPlan());
$("planList").addEventListener("dragover", handlePlanDragOver);
$("planList").addEventListener("drop", persistPlanOrder);
$("addPlanStep").addEventListener("click", () => addPlanStepEditorRow());
$("cancelPlanEdit").addEventListener("click", cancelPlanEdit);
$("planForm").addEventListener("submit", savePlan);
$("workspaceSelect").addEventListener("change", (event) => switchWorkspace(event.target.value));
$("newWorkspace").addEventListener("click", async () => {
  renderProviderOptions();
  const provider = selectableProviders()[0];
  if (provider) {
    await loadProviderModels(provider.id, { notify: false });
    $("newWorkspaceProvider").value = provider.id;
    $("newWorkspaceModel").value = displayedModelForProvider(provider.id);
  }
  $("workspaceDialog").showModal();
});
$("cancelWorkspace").addEventListener("click", () => $("workspaceDialog").close());
$("workspaceForm").addEventListener("submit", createWorkspace);
$("workspaceSettings").addEventListener("submit", saveWorkspaceSettings);
$("serviceSettings").addEventListener("submit", saveServiceSettings);
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
$("sessionForm").addEventListener("submit", createSession);
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
$("newProvider").addEventListener("change", (event) => {
  const providerID = event.target.value || state.workspace?.default_provider_id || "";
  $("newModel").value = defaultModelForProvider(providerID);
});
$("composer").addEventListener("submit", sendPrompt);
$("send").addEventListener("click", activateRunAction);
$("messages").addEventListener("scroll", updateMessageAutoScroll, { passive: true });
$("prompt").addEventListener("paste", (event) => {
  if (!state.session || state.running) return;
  const files = [...(event.clipboardData?.files || [])];
  if (files.length) addPendingAttachments(files);
});
const chatWorkspace = document.querySelector("main.workspace");
chatWorkspace.addEventListener("dragenter", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer) || !state.session || state.running) return;
  event.preventDefault();
  chatDragDepth += 1;
  chatWorkspace.classList.add("chat-drag-over");
});
chatWorkspace.addEventListener("dragover", (event) => {
  if (!dataTransferHasFiles(event.dataTransfer) || !state.session || state.running) return;
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
  if (!state.session || state.running) return;
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
$("closeManagement").addEventListener("click", closeManagement);
for (const button of document.querySelectorAll(".panel-tab")) button.addEventListener("click", () => activatePanel(button.dataset.panel));
$("refreshDiagnostics").addEventListener("click", loadDiagnostics);
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
  if (!$("sessionContextMenu").contains(event.target)) closeSessionContextMenu();
  if (!$("resourceContextMenu").contains(event.target)) closeResourceContextMenu();
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
  if (!event.target.closest?.(".session-row")) closeSessionContextMenu();
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  closeSessionContextMenu();
  closeResourceContextMenu();
});
document.addEventListener("scroll", () => {
  closeSessionContextMenu();
  closeResourceContextMenu();
}, true);
window.addEventListener("resize", () => {
  closeSessionContextMenu();
  closeResourceContextMenu();
});
window.addEventListener("blur", () => {
  closeSessionContextMenu();
  closeResourceContextMenu();
});

installDialogDragGuards();
notifyNativeStartupReady();
refreshBackend();
setInterval(updateLiveReasoningDurations, 1000);
