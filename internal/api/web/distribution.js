"use strict";

const state = {
  records: [],
  selectedKey: "",
  toastTimer: 0,
};

const elements = {
  form: document.querySelector("#distribution-form"),
  list: document.querySelector("#record-list"),
  empty: document.querySelector("#record-empty"),
  search: document.querySelector("#record-search"),
  newButton: document.querySelector("#new-record-button"),
  deleteButton: document.querySelector("#delete-button"),
  editorMode: document.querySelector("#editor-mode"),
  editorTitle: document.querySelector("#editor-title"),
  saveState: document.querySelector("#save-state"),
  summaryTotal: document.querySelector("#summary-total"),
  summaryPublished: document.querySelector("#summary-published"),
  summarySize: document.querySelector("#summary-size"),
  name: document.querySelector("#record-name"),
  path: document.querySelector("#record-path"),
  enabled: document.querySelector("#record-enabled"),
  file: document.querySelector("#record-file"),
  fileLabel: document.querySelector("#file-label"),
  fileDetail: document.querySelector("#file-detail"),
  fileName: document.querySelector("#file-name"),
  fileSize: document.querySelector("#file-size"),
  fileChecksum: document.querySelector("#file-checksum"),
  fileURL: document.querySelector("#file-url"),
  toast: document.querySelector("#toast"),
};

async function request(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
  });
  if (response.status === 401) {
    window.location.href = "/admin/announcements";
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
  const payload = await request("/v1/admin/distribution");
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
  const totalBytes = state.records.reduce((total, record) => total + Number(record.size || 0), 0);
  elements.summaryTotal.textContent = String(state.records.length);
  elements.summaryPublished.textContent = String(published);
  elements.summarySize.textContent = formatBytes(totalBytes);
}

function renderList() {
  const query = elements.search.value.trim().toLocaleLowerCase();
  const filtered = state.records.filter((record) => {
    if (!query) {
      return true;
    }
    return [record.name, record.file_name, record.destination_path]
      .filter(Boolean)
      .some((value) => String(value).toLocaleLowerCase().includes(query));
  });

  elements.list.replaceChildren();
  elements.empty.hidden = state.records.length > 0;
  if (state.records.length > 0 && filtered.length === 0) {
    const noResults = document.createElement("p");
    noResults.className = "empty-state";
    noResults.textContent = "没有匹配的文件。";
    elements.list.append(noResults);
    return;
  }

  for (const record of filtered) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "record-card";
    button.setAttribute("aria-current", String(record.key === state.selectedKey));
    button.addEventListener("click", () => selectRecord(record.key));

    const header = document.createElement("span");
    header.className = "record-card-header";
    const title = document.createElement("strong");
    title.textContent = record.name;
    const size = document.createElement("span");
    size.className = "record-card-id";
    size.textContent = formatBytes(record.size);
    header.append(title, size);

    const fileName = document.createElement("span");
    fileName.className = "record-card-file";
    fileName.textContent = record.file_name;

    const meta = document.createElement("span");
    meta.className = "record-card-meta";
    const destination = document.createElement("span");
    destination.textContent = record.destination_path;
    const published = document.createElement("span");
    published.className = `publish-indicator${record.enabled ? " is-published" : ""}`;
    published.textContent = record.enabled ? "下发中" : "已停用";
    meta.append(destination, published);

    button.append(header, fileName, meta);
    elements.list.append(button);
  }
}

function selectRecord(key) {
  const record = state.records.find((item) => item.key === key);
  if (!record) {
    return;
  }

  state.selectedKey = key;
  elements.name.value = record.name;
  elements.path.value = record.destination_path;
  elements.enabled.checked = Boolean(record.enabled);
  elements.file.value = "";
  elements.file.required = false;
  elements.fileLabel.textContent = "替换文件（可选）";
  elements.editorMode.textContent = record.enabled ? "正在下发" : "已停用";
  elements.editorTitle.textContent = record.name;
  elements.saveState.textContent = formatUpdatedAt(record.updated_at);
  elements.deleteButton.disabled = false;
  showFileDetail(record);
  renderList();
}

function resetEditor() {
  state.selectedKey = "";
  elements.form.reset();
  elements.path.value = "/Documents/Providers";
  elements.enabled.checked = false;
  elements.file.required = true;
  elements.fileLabel.textContent = "选择文件";
  elements.editorMode.textContent = "新文件";
  elements.editorTitle.textContent = "配置下发内容";
  elements.saveState.textContent = "尚未保存";
  elements.deleteButton.disabled = true;
  elements.fileDetail.hidden = true;
  renderList();
  elements.name.focus();
}

function showFileDetail(record) {
  elements.fileDetail.hidden = false;
  elements.fileName.textContent = record.file_name;
  elements.fileSize.textContent = formatBytes(record.size);
  elements.fileChecksum.textContent = record.sha256;
  elements.fileURL.textContent =
    `/v1/distribution/files/${record.sha256}/${encodeURIComponent(record.file_name)}`;
}

async function saveRecord(event) {
  event.preventDefault();
  if (!elements.form.reportValidity()) {
    return;
  }

  const formData = new FormData();
  formData.append("name", elements.name.value.trim());
  formData.append("destination_path", elements.path.value.trim());
  formData.append("enabled", String(elements.enabled.checked));
  if (elements.file.files.length > 0) {
    formData.append("file", elements.file.files[0]);
  }

  const isEditing = Boolean(state.selectedKey);
  const path = isEditing
    ? `/v1/admin/distribution/${encodeURIComponent(state.selectedKey)}`
    : "/v1/admin/distribution";
  setSaving(true);
  try {
    const payload = await request(path, {
      method: isEditing ? "PUT" : "POST",
      body: formData,
    });
    state.selectedKey = payload.record.key;
    await loadRecords(payload.record.key);
    showToast(payload.record.enabled ? "文件已加入公开清单。" : "文件已保存但未公开。");
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
  if (!window.confirm(`确定删除“${record.name}”及其文件吗？此操作无法撤销。`)) {
    return;
  }

  try {
    await request(`/v1/admin/distribution/${encodeURIComponent(record.key)}`, {
      method: "DELETE",
    });
    state.selectedKey = "";
    await loadRecords();
    showToast("官方数据文件已删除。");
  } catch (error) {
    showToast(error.message, true);
  }
}

function setSaving(isSaving) {
  const submit = elements.form.querySelector('button[type="submit"]');
  submit.disabled = isSaving;
  submit.textContent = isSaving ? "正在上传…" : "保存更改";
  if (isSaving) {
    elements.saveState.textContent = "正在保存";
  }
}

function formatBytes(rawValue) {
  const bytes = Number(rawValue || 0);
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function formatUpdatedAt(value) {
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
elements.deleteButton.addEventListener("click", deleteSelected);
elements.search.addEventListener("input", renderList);
elements.file.addEventListener("change", () => {
  if (elements.file.files.length > 0) {
    elements.saveState.textContent = `已选择 ${elements.file.files[0].name}`;
  }
});

document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "s") {
    event.preventDefault();
    elements.form.requestSubmit();
  }
});

loadRecords().catch((error) => {
  showToast(error.message, true);
});
