"use strict";

const state = {
  records: [],
  selectedKey: "",
  responseCount: 0,
  definitionLocked: false,
  toastTimer: 0,
};

const elements = {
  form: document.querySelector("#survey-form"),
  list: document.querySelector("#record-list"),
  empty: document.querySelector("#record-empty"),
  search: document.querySelector("#record-search"),
  newButton: document.querySelector("#new-record-button"),
  duplicateButton: document.querySelector("#duplicate-button"),
  deleteButton: document.querySelector("#delete-button"),
  addQuestionButton: document.querySelector("#add-question-button"),
  questionList: document.querySelector("#question-list"),
  questionTemplate: document.querySelector("#question-template"),
  optionTemplate: document.querySelector("#option-template"),
  editorMode: document.querySelector("#editor-mode"),
  editorTitle: document.querySelector("#editor-title"),
  saveState: document.querySelector("#save-state"),
  lockedNotice: document.querySelector("#locked-notice"),
  summaryTotal: document.querySelector("#summary-total"),
  summaryPublished: document.querySelector("#summary-published"),
  summaryResponses: document.querySelector("#summary-responses"),
  id: document.querySelector("#record-id"),
  enabled: document.querySelector("#record-enabled"),
  language: document.querySelector("#record-language"),
  platform: document.querySelector("#record-platform"),
  minBuild: document.querySelector("#record-min-build"),
  maxBuild: document.querySelector("#record-max-build"),
  title: document.querySelector("#record-title"),
  description: document.querySelector("#record-description"),
  resultsSection: document.querySelector("#results-section"),
  responseCount: document.querySelector("#response-count"),
  environmentSummary: document.querySelector("#environment-summary"),
  resultsList: document.querySelector("#results-list"),
  toast: document.querySelector("#toast"),
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
  const payload = await requestJSON("/v1/admin/surveys");
  state.records = payload.records || [];
  renderSummary();
  renderList();

  if (preferredKey && state.records.some((record) => record.key === preferredKey)) {
    await selectRecord(preferredKey);
  } else if (state.records.length > 0 && !state.selectedKey) {
    await selectRecord(state.records[0].key);
  } else if (state.records.length === 0) {
    resetEditor();
  }
}

function renderSummary() {
  elements.summaryTotal.textContent = String(state.records.length);
  elements.summaryPublished.textContent = String(
    state.records.filter((record) => record.enabled).length,
  );
  elements.summaryResponses.textContent = String(state.responseCount);
}

function renderList() {
  const query = elements.search.value.trim().toLocaleLowerCase();
  const filtered = state.records.filter((record) => {
    if (!query) {
      return true;
    }
    return [record.id, record.title, record.description, record.language, record.platform]
      .filter(Boolean)
      .some((value) => String(value).toLocaleLowerCase().includes(query));
  });

  elements.list.replaceChildren();
  elements.empty.hidden = state.records.length > 0;
  if (state.records.length > 0 && filtered.length === 0) {
    const noResults = document.createElement("p");
    noResults.className = "empty-state";
    noResults.textContent = "没有匹配的意见征集。";
    elements.list.append(noResults);
    return;
  }

  for (const record of filtered) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "record-card";
    button.dataset.key = record.key;
    button.setAttribute("aria-current", String(record.key === state.selectedKey));
    button.addEventListener("click", () => {
      selectRecord(record.key).catch((error) => showToast(error.message, true));
    });

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
    published.textContent = record.enabled ? "征集中" : "草稿";
    meta.append(audience, published);

    button.append(header, meta);
    elements.list.append(button);
  }
}

async function selectRecord(key) {
  const record = state.records.find((item) => item.key === key);
  if (!record) {
    return;
  }

  state.selectedKey = record.key;
  elements.id.value = String(record.id);
  elements.enabled.checked = Boolean(record.enabled);
  elements.language.value = record.language || "";
  elements.platform.value = record.platform || "";
  elements.minBuild.value = record.min_build || "";
  elements.maxBuild.value = record.max_build || "";
  elements.title.value = record.title;
  elements.description.value = record.description || "";
  elements.editorMode.textContent = record.enabled ? "正在征集" : "草稿";
  elements.editorTitle.textContent = record.title;
  elements.saveState.textContent = formatUpdatedAt(record.updated_at);
  elements.duplicateButton.disabled = false;
  elements.deleteButton.disabled = false;
  renderQuestions(record.questions || []);
  renderList();
  await loadResults(record.key);
}

function resetEditor() {
  state.selectedKey = "";
  state.responseCount = 0;
  elements.form.reset();
  elements.id.value = String(nextSurveyID());
  elements.enabled.checked = false;
  elements.editorMode.textContent = "新征集";
  elements.editorTitle.textContent = "编辑稿件";
  elements.saveState.textContent = "尚未保存";
  elements.duplicateButton.disabled = true;
  elements.deleteButton.disabled = true;
  elements.resultsSection.hidden = true;
  renderQuestions([]);
  addQuestion();
  setDefinitionLocked(false);
  renderList();
  renderSummary();
  elements.title.focus();
}

function duplicateSelected() {
  const record = state.records.find((item) => item.key === state.selectedKey);
  if (!record) {
    return;
  }
  state.selectedKey = "";
  state.responseCount = 0;
  elements.enabled.checked = false;
  elements.language.value = "";
  elements.editorMode.textContent = "新语言版本";
  elements.saveState.textContent = "复制内容尚未保存";
  elements.duplicateButton.disabled = true;
  elements.deleteButton.disabled = true;
  elements.resultsSection.hidden = true;
  setDefinitionLocked(false);
  renderList();
  renderSummary();
  elements.language.focus();
}

function renderQuestions(questions) {
  elements.questionList.replaceChildren();
  for (const question of questions) {
    addQuestion(question);
  }
  updateQuestionNumbers();
}

function addQuestion(question = null) {
  if (elements.questionList.children.length >= 10) {
    showToast("每份征集最多添加 10 道题。", true);
    return;
  }

  const fragment = elements.questionTemplate.content.cloneNode(true);
  const card = fragment.querySelector(".question-card");
  card.dataset.questionId = question?.id || newIdentifier("q");
  card.querySelector(".question-title").value = question?.question || "";
  card.querySelector(".question-type").value = question?.type || "single_select";
  card.querySelector(".question-required").checked = Boolean(question?.required);
  card.querySelector(".question-other").checked = Boolean(question?.allow_other);

  card.querySelector(".remove-question").addEventListener("click", () => {
    if (elements.questionList.children.length === 1) {
      showToast("至少保留一道题。", true);
      return;
    }
    card.remove();
    updateQuestionNumbers();
  });
  card.querySelector(".add-option").addEventListener("click", () => addOption(card));

  elements.questionList.append(card);
  const options = question?.options?.length ? question.options : [null, null];
  for (const option of options) {
    addOption(card, option);
  }
  updateQuestionNumbers();
}

function addOption(card, option = null) {
  const list = card.querySelector(".option-list");
  if (list.children.length >= 20) {
    showToast("每道题最多添加 20 个选项。", true);
    return;
  }

  const fragment = elements.optionTemplate.content.cloneNode(true);
  const row = fragment.querySelector(".option-row");
  row.dataset.optionId = option?.id || newIdentifier("o");
  row.querySelector(".option-label").value = option?.label || "";
  row.querySelector(".remove-option").addEventListener("click", () => {
    if (list.children.length === 1) {
      showToast("至少保留一个选项。", true);
      return;
    }
    row.remove();
    updateOptionNumbers(card);
  });
  list.append(row);
  updateOptionNumbers(card);
}

function updateQuestionNumbers() {
  [...elements.questionList.children].forEach((card, index) => {
    card.querySelector(".question-number").textContent = `问题 ${index + 1}`;
    updateOptionNumbers(card);
  });
}

function updateOptionNumbers(card) {
  [...card.querySelectorAll(".option-row")].forEach((row, index) => {
    row.querySelector(".option-index").textContent = String(index + 1);
  });
}

function collectRecord() {
  const questions = [...elements.questionList.querySelectorAll(".question-card")].map((card) => ({
    id: card.dataset.questionId,
    question: card.querySelector(".question-title").value.trim(),
    type: card.querySelector(".question-type").value,
    allow_other: card.querySelector(".question-other").checked,
    required: card.querySelector(".question-required").checked,
    options: [...card.querySelectorAll(".option-row")].map((row) => ({
      id: row.dataset.optionId,
      label: row.querySelector(".option-label").value.trim(),
    })),
  }));

  return {
    id: Number(elements.id.value),
    title: elements.title.value.trim(),
    description: elements.description.value.trim(),
    min_build: elements.minBuild.value.trim(),
    max_build: elements.maxBuild.value.trim(),
    language: elements.language.value.trim(),
    platform: elements.platform.value,
    questions,
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
    ? `/v1/admin/surveys/${encodeURIComponent(state.selectedKey)}`
    : "/v1/admin/surveys";

  setSaving(true);
  try {
    const response = await requestJSON(path, {
      method: isEditing ? "PUT" : "POST",
      body: JSON.stringify(payload),
    });
    state.selectedKey = response.record.key;
    await loadRecords(response.record.key);
    showToast(payload.enabled ? "意见征集已保存并发布。" : "意见征集已保存为草稿。");
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
  if (!window.confirm(`确定删除“${record.title}”吗？`)) {
    return;
  }

  try {
    await requestJSON(`/v1/admin/surveys/${encodeURIComponent(record.key)}`, {
      method: "DELETE",
    });
    state.selectedKey = "";
    await loadRecords();
    showToast("意见征集已删除。");
  } catch (error) {
    showToast(error.message, true);
  }
}

async function loadResults(key) {
  const payload = await requestJSON(
    `/v1/admin/surveys/${encodeURIComponent(key)}/results`,
  );
  if (state.selectedKey !== key) {
    return;
  }

  state.responseCount = payload.response_count || 0;
  elements.responseCount.textContent = `${state.responseCount} 份`;
  elements.resultsSection.hidden = false;
  setDefinitionLocked(state.responseCount > 0);
  renderResults(payload.survey, payload.responses || []);
  renderSummary();
}

function renderResults(survey, responses) {
  elements.resultsList.replaceChildren();
  renderEnvironmentSummary(responses);
  if (responses.length === 0) {
    const empty = document.createElement("p");
    empty.className = "results-empty";
    empty.textContent = "还没有收到答卷。";
    elements.resultsList.append(empty);
    return;
  }

  for (const question of survey.questions || []) {
    const answerRows = responses
      .map((response) => ({
        response,
        answer: (response.answers || []).find(
          (candidate) => candidate.question_id === question.id,
        ),
      }))
      .filter((row) => row.answer);
    const answeredCount = answerRows.filter(
      ({ answer }) =>
        (answer.selected_option_ids || []).length > 0 || Boolean(answer.other_text),
    ).length;

    const card = document.createElement("article");
    card.className = "result-card";
    const heading = document.createElement("div");
    heading.className = "result-card-heading";
    const title = document.createElement("strong");
    title.textContent = question.question;
    const count = document.createElement("span");
    const skippedCount = responses.length - answeredCount;
    const answerRate = responses.length > 0
      ? Math.round((answeredCount / responses.length) * 100)
      : 0;
    count.textContent = `${answeredCount} 人回答 · ${skippedCount} 人跳过 · 作答率 ${answerRate}%`;
    heading.append(title, count);
    card.append(heading);

    for (const option of question.options || []) {
      const optionCount = answerRows.filter(({ answer }) =>
        (answer.selected_option_ids || []).includes(option.id),
      ).length;
      const percentage = answeredCount > 0 ? (optionCount / answeredCount) * 100 : 0;
      const row = document.createElement("div");
      row.className = "result-option";

      const label = document.createElement("div");
      const optionLabel = document.createElement("span");
      optionLabel.textContent = option.label;
      const optionValue = document.createElement("strong");
      optionValue.textContent = `${optionCount} · ${Math.round(percentage)}%`;
      label.append(optionLabel, optionValue);

      const track = document.createElement("span");
      track.className = "result-track";
      const bar = document.createElement("span");
      bar.style.width = `${Math.min(100, percentage)}%`;
      track.append(bar);
      row.append(label, track);
      card.append(row);
    }

    const customAnswers = answerRows.filter(({ answer }) => answer.other_text);
    if (customAnswers.length > 0) {
      const customSection = document.createElement("details");
      customSection.className = "custom-answers";
      const summary = document.createElement("summary");
      summary.textContent = `自定义回答（${customAnswers.length}）`;
      customSection.append(summary);
      for (const { response, answer } of customAnswers) {
        const item = document.createElement("div");
        const text = document.createElement("p");
        text.textContent = answer.other_text;
        const time = document.createElement("span");
        time.textContent = [
          formatClientVersion(response),
          platformLabel(response.platform),
          response.language || "未知语言",
          formatSubmittedAt(response.submitted_at),
        ].filter(Boolean).join(" · ");
        item.append(text, time);
        customSection.append(item);
      }
      card.append(customSection);
    }
    elements.resultsList.append(card);
  }
}

function renderEnvironmentSummary(responses) {
  elements.environmentSummary.replaceChildren();
  elements.environmentSummary.hidden = responses.length === 0;
  if (responses.length === 0) {
    return;
  }

  const card = document.createElement("article");
  card.className = "environment-card";
  const heading = document.createElement("div");
  heading.className = "environment-heading";
  const title = document.createElement("strong");
  title.textContent = "客户端分布";
  const description = document.createElement("span");
  description.textContent = "匿名版本与运行环境";
  heading.append(title, description);
  card.append(heading);

  const groups = [
    ["版本与构建", countValues(responses, formatClientVersion)],
    ["平台", countValues(responses, (response) => platformLabel(response.platform))],
    ["语言", countValues(responses, (response) => response.language || "未知语言")],
  ];

  const grid = document.createElement("div");
  grid.className = "environment-grid";
  for (const [label, values] of groups) {
    const group = document.createElement("section");
    const groupLabel = document.createElement("span");
    groupLabel.className = "environment-label";
    groupLabel.textContent = label;
    const list = document.createElement("div");
    list.className = "environment-values";
    for (const [value, count] of values) {
      const item = document.createElement("span");
      const name = document.createElement("span");
      name.textContent = value;
      const amount = document.createElement("strong");
      amount.textContent = String(count);
      item.append(name, amount);
      list.append(item);
    }
    group.append(groupLabel, list);
    grid.append(group);
  }
  card.append(grid);
  elements.environmentSummary.append(card);
}

function countValues(responses, selector) {
  const counts = new Map();
  for (const response of responses) {
    const value = selector(response);
    counts.set(value, (counts.get(value) || 0) + 1);
  }
  return [...counts.entries()].sort((left, right) =>
    right[1] - left[1] || left[0].localeCompare(right[0], "zh-CN"),
  );
}

function formatClientVersion(response) {
  const version = response.app_version || "";
  const build = response.app_build || "";
  if (version && build) {
    return `${version} (${build})`;
  }
  if (version) {
    return version;
  }
  if (build) {
    return `构建 ${build}`;
  }
  return "未知版本";
}

function setDefinitionLocked(locked) {
  state.definitionLocked = locked;
  elements.lockedNotice.hidden = !locked;
  for (const input of elements.form.querySelectorAll("[data-definition]")) {
    input.disabled = locked;
  }
  elements.addQuestionButton.disabled = locked;
  elements.deleteButton.disabled = locked || !state.selectedKey;
  for (const button of elements.form.querySelectorAll(
    ".remove-question, .remove-option, .add-option",
  )) {
    button.disabled = locked;
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

function nextSurveyID() {
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

function newIdentifier(prefix) {
  if (typeof crypto.randomUUID === "function") {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
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

function formatSubmittedAt(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
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
elements.addQuestionButton.addEventListener("click", () => addQuestion());
elements.search.addEventListener("input", renderList);

document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === "s") {
    event.preventDefault();
    elements.form.requestSubmit();
  }
});

loadRecords().catch((error) => {
  showToast(error.message, true);
});
