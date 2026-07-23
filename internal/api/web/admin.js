"use strict";

const state = {
  records: [],
  selectedKey: "",
  toastTimer: 0,
};

const elements = {
  form: document.querySelector("#announcement-form"),
  list: document.querySelector("#record-list"),
  empty: document.querySelector("#record-empty"),
  search: document.querySelector("#record-search"),
  newButton: document.querySelector("#new-record-button"),
  duplicateButton: document.querySelector("#duplicate-button"),
  deleteButton: document.querySelector("#delete-button"),
  editorMode: document.querySelector("#editor-mode"),
  editorTitle: document.querySelector("#editor-title"),
  saveState: document.querySelector("#save-state"),
  summaryTotal: document.querySelector("#summary-total"),
  summaryPublished: document.querySelector("#summary-published"),
  summaryDrafts: document.querySelector("#summary-drafts"),
  id: document.querySelector("#record-id"),
  type: document.querySelector("#record-type"),
  enabled: document.querySelector("#record-enabled"),
  language: document.querySelector("#record-language"),
  platform: document.querySelector("#record-platform"),
  minBuild: document.querySelector("#record-min-build"),
  maxBuild: document.querySelector("#record-max-build"),
  title: document.querySelector("#record-title"),
  body: document.querySelector("#record-body"),
  bodyCount: document.querySelector("#body-count"),
  previewType: document.querySelector("#preview-type"),
  previewAudience: document.querySelector("#preview-audience"),
  previewTitle: document.querySelector("#preview-title"),
  previewBody: document.querySelector("#preview-body"),
  toast: document.querySelector("#toast"),
};

const typePresentation = {
  info: { label: "普通", className: "type-info" },
  warning: { label: "提醒", className: "type-warning" },
  blocking: { label: "重要", className: "type-blocking" },
};

async function requestJSON(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });

  if (response.status === 401) {
    window.location.reload();
    throw new Error("管理会话已过期");
  }
  if (!response.ok) {
    let message = `请求失败（${response.status}）`;
    try {
      const payload = await response.json();
      message = payload.error || message;
    } catch {
      // 非 JSON 错误沿用状态码提示。
    }
    throw new Error(message);
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

async function loadRecords(preferredKey = state.selectedKey) {
  const payload = await requestJSON("/v1/admin/announcements");
  state.records = payload.records || [];
  renderSummary();
  renderList();

  if (preferredKey && state.records.some((record) => record.key === preferredKey)) {
    selectRecord(preferredKey);
  } else if (state.records.length > 0 && !state.selectedKey) {
    selectRecord(state.records[0].key);
  } else if (state.records.length === 0) {
    resetEditor();
  }
}

function renderSummary() {
  const published = state.records.filter((record) => record.enabled).length;
  elements.summaryTotal.textContent = String(state.records.length);
  elements.summaryPublished.textContent = String(published);
  elements.summaryDrafts.textContent = String(state.records.length - published);
}

function renderList() {
  const query = elements.search.value.trim().toLocaleLowerCase();
  const filtered = state.records.filter((record) => {
    if (!query) {
      return true;
    }
    return [record.id, record.title, record.body, record.language, record.platform]
      .filter(Boolean)
      .some((value) => String(value).toLocaleLowerCase().includes(query));
  });

  elements.list.replaceChildren();
  elements.empty.hidden = state.records.length > 0;

  if (state.records.length > 0 && filtered.length === 0) {
    const noResults = document.createElement("p");
    noResults.className = "empty-state";
    noResults.textContent = "没有匹配的公告。";
    elements.list.append(noResults);
    return;
  }

  for (const record of filtered) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "record-card";
    button.dataset.key = record.key;
    button.setAttribute("aria-current", String(record.key === state.selectedKey));
    button.addEventListener("click", () => selectRecord(record.key));

    const header = document.createElement("span");
    header.className = "record-card-header";
    const title = document.createElement("strong");
    title.textContent = record.title;
    const id = document.createElement("span");
    id.className = "record-card-id";
    id.textContent = `#${record.id}`;
    header.append(title, id);

    const meta = document.createElement("span");
    meta.className = "record-card-meta";
    const audience = document.createElement("span");
    audience.textContent = `${record.language || "全部语言"} · ${platformLabel(record.platform)}`;
    const published = document.createElement("span");
    published.className = `publish-indicator${record.enabled ? " is-published" : ""}`;
    published.textContent = record.enabled ? "发布中" : "草稿";
    meta.append(audience, published);

    button.append(header, meta);
    elements.list.append(button);
  }
}

function selectRecord(key) {
  const record = state.records.find((item) => item.key === key);
  if (!record) {
    return;
  }

  state.selectedKey = record.key;
  elements.id.value = String(record.id);
  elements.type.value = record.type;
  elements.enabled.checked = Boolean(record.enabled);
  elements.language.value = record.language || "";
  elements.platform.value = record.platform || "";
  elements.minBuild.value = record.min_build || "";
  elements.maxBuild.value = record.max_build || "";
  elements.title.value = record.title;
  elements.body.value = record.body;
  elements.editorMode.textContent = record.enabled ? "正在发布" : "草稿";
  elements.editorTitle.textContent = record.title;
  elements.saveState.textContent = formatUpdatedAt(record.updated_at);
  elements.duplicateButton.disabled = false;
  elements.deleteButton.disabled = false;
  renderList();
  updatePreview();
}

function resetEditor() {
  state.selectedKey = "";
  elements.form.reset();
  elements.id.value = String(nextAnnouncementID());
  elements.type.value = "info";
  elements.enabled.checked = false;
  elements.editorMode.textContent = "新公告";
  elements.editorTitle.textContent = "编辑内容";
  elements.saveState.textContent = "尚未保存";
  elements.duplicateButton.disabled = true;
  elements.deleteButton.disabled = true;
  renderList();
  updatePreview();
  elements.title.focus();
}

function duplicateSelected() {
  const record = state.records.find((item) => item.key === state.selectedKey);
  if (!record) {
    return;
  }
  state.selectedKey = "";
  elements.enabled.checked = false;
  elements.language.value = "";
  elements.editorMode.textContent = "新语言版本";
  elements.editorTitle.textContent = record.title;
  elements.saveState.textContent = "复制内容尚未保存";
  elements.duplicateButton.disabled = true;
  elements.deleteButton.disabled = true;
  renderList();
  updatePreview();
  elements.language.focus();
}

function collectRecord() {
  return {
    id: Number(elements.id.value),
    type: elements.type.value,
    min_build: elements.minBuild.value.trim(),
    max_build: elements.maxBuild.value.trim(),
    language: elements.language.value.trim(),
    platform: elements.platform.value,
    title: elements.title.value.trim(),
    body: elements.body.value.trim(),
    enabled: elements.enabled.checked,
  };
}

async function saveRecord(event) {
  event.preventDefault();
  if (!elements.form.reportValidity()) {
    return;
  }

  const payload = collectRecord();
  const isEditing = Boolean(state.selectedKey);
  const path = isEditing
    ? `/v1/admin/announcements/${encodeURIComponent(state.selectedKey)}`
    : "/v1/admin/announcements";

  setSaving(true);
  try {
    const response = await requestJSON(path, {
      method: isEditing ? "PUT" : "POST",
      body: JSON.stringify(payload),
    });
    state.selectedKey = response.record.key;
    await loadRecords(response.record.key);
    showToast(payload.enabled ? "公告已保存并发布。" : "公告已保存为草稿。");
  } catch (error) {
    showToast(error.message, true);
  } finally {
    setSaving(false);
  }
}

async function deleteSelected() {
  const record = state.records.find((item) => item.key === state.selectedKey);
  if (!record) {
    return;
  }
  if (!window.confirm(`确定删除“${record.title}”吗？此操作无法撤销。`)) {
    return;
  }

  try {
    await requestJSON(`/v1/admin/announcements/${encodeURIComponent(record.key)}`, {
      method: "DELETE",
    });
    state.selectedKey = "";
    await loadRecords();
    showToast("公告已删除。");
  } catch (error) {
    showToast(error.message, true);
  }
}

function setSaving(isSaving) {
  const submit = elements.form.querySelector('button[type="submit"]');
  submit.disabled = isSaving;
  submit.textContent = isSaving ? "正在保存…" : "保存更改";
  if (isSaving) {
    elements.saveState.textContent = "正在保存";
  }
}

function updatePreview() {
  const presentation = typePresentation[elements.type.value] || typePresentation.info;
  elements.previewType.textContent = presentation.label;
  elements.previewType.className = `type-badge ${presentation.className}`;
  elements.previewAudience.textContent = `${elements.language.value.trim() || "全部语言"} · ${platformLabel(elements.platform.value)}`;
  elements.previewTitle.textContent = elements.title.value.trim() || "公告标题预览";
  elements.previewBody.textContent = elements.body.value.trim() || "公告正文会显示在这里。";
  elements.bodyCount.textContent = String(elements.body.value.length);
}

function nextAnnouncementID() {
  const now = new Date();
  const prefix = [
    now.getFullYear(),
    String(now.getMonth() + 1).padStart(2, "0"),
    String(now.getDate()).padStart(2, "0"),
  ].join("");
  const existingSuffixes = state.records
    .map((record) => String(record.id))
    .filter((id) => id.startsWith(prefix))
    .map((id) => Number(id.slice(8)))
    .filter(Number.isFinite);
  const suffix = Math.max(0, ...existingSuffixes) + 1;
  return `${prefix}${String(suffix).padStart(2, "0")}`;
}

function platformLabel(platform) {
  if (platform === "iOS") {
    return "仅 iOS";
  }
  if (platform === "watchOS") {
    return "仅 watchOS";
  }
  return "双平台";
}

function formatUpdatedAt(value) {
  if (!value) {
    return "尚未保存";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "已保存";
  }
  return `更新于 ${new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date)}`;
}

function showToast(message, isError = false) {
  window.clearTimeout(state.toastTimer);
  elements.toast.textContent = message;
  elements.toast.classList.toggle("is-error", isError);
  elements.toast.hidden = false;
  state.toastTimer = window.setTimeout(() => {
    elements.toast.hidden = true;
  }, 3200);
}

elements.form.addEventListener("submit", saveRecord);
elements.newButton.addEventListener("click", resetEditor);
elements.duplicateButton.addEventListener("click", duplicateSelected);
elements.deleteButton.addEventListener("click", deleteSelected);
elements.search.addEventListener("input", renderList);

for (const input of [
  elements.type,
  elements.language,
  elements.platform,
  elements.title,
  elements.body,
]) {
  input.addEventListener("input", updatePreview);
  input.addEventListener("change", updatePreview);
}

document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "s") {
    event.preventDefault();
    elements.form.requestSubmit();
  }
});

loadRecords().catch((error) => {
  showToast(error.message, true);
});
