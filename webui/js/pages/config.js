// Page: 配置中心 — masked config editing with validation, hot reload and reveal.
import { api } from "../api.js";
import { loadConfig, loadDebugModels } from "../data.js";
import * as store from "../store.js";
import { t, onChange } from "../i18n.js";
import { $, $$, bindTabs, confirmDialog, el, openDialog, toast } from "../ui.js";

let probeResults = [];

function selectedProbeIndexes() {
  return [...$("#proxy-probe-results").querySelectorAll("input[data-index]:checked")].map((input) => Number(input.dataset.index));
}

function syncProbeControls() {
  const ready = probeResults.length > 0;
  for (const selector of ["#proxy-select-all", "#proxy-select-timeout", "#proxy-select-429", "#proxy-select-403"]) $(selector).disabled = !ready;
  $("#proxy-delete").disabled = !selectedProbeIndexes().length;
}

function selectProbeResults() {
  const changed = document.activeElement?.id;
  const all = $("#proxy-select-all").checked;
  const timeout = $("#proxy-select-timeout").checked;
  const rateLimited = $("#proxy-select-429").checked;
  const forbidden = $("#proxy-select-403").checked;
  $$("#proxy-probe-results input[data-index]").forEach((input) => {
    const result = probeResults[Number(input.dataset.index)];
    if (changed === "proxy-select-all") input.checked = all;
    else if (changed === "proxy-select-timeout") input.checked = timeout ? Boolean(result?.timeout) || input.checked : !result?.timeout && input.checked;
    else if (changed === "proxy-select-429") input.checked = rateLimited ? result?.status === 429 || input.checked : result?.status !== 429 && input.checked;
    else if (changed === "proxy-select-403") input.checked = forbidden ? result?.status === 403 || input.checked : result?.status !== 403 && input.checked;
  });
  syncProbeControls();
}

function renderProbeResults(results) {
  probeResults = results || [];
  for (const selector of ["#proxy-select-all", "#proxy-select-timeout", "#proxy-select-429", "#proxy-select-403"]) $(selector).checked = false;
  const body = $("#proxy-probe-results");
  body.replaceChildren();
  if (!probeResults.length) {
    body.append(el("tr", {}, el("td", { class: "empty", colspan: 7, text: t("没有代理节点", "config.proxy.none") })));
    $("#proxy-probe-summary").textContent = t("没有可检测的代理节点", "config.proxy.none");
    syncProbeControls();
    return;
  }
  let ok = 0;
  for (const result of probeResults) {
    const statusOk = result.status >= 200 && result.status < 400;
    if (statusOk && !result.error) ok++;
    const checkbox = el("input", { type: "checkbox", dataset: { index: result.index }, "aria-label": `${t("选择节点", "config.proxy.selectNode")} ${result.address}` });
    checkbox.addEventListener("change", () => {
      $("#proxy-select-all").checked = false;
      syncProbeControls();
    });
    const status = result.status ? String(result.status) : result.error ? t("无 HTTP 响应", "config.proxy.noHttpResponse") : "—";
    const issue = result.timeout ? t("超时", "config.proxy.timeout") : result.error ? t("连接失败", "config.proxy.connectError") : statusOk ? t("可达", "config.proxy.reachable") : t("HTTP 响应", "config.proxy.httpResponse");
    const tone = statusOk && !result.error ? "ok" : result.timeout || result.error || result.status === 403 || result.status === 429 ? "bad" : "warn";
    const row = el("tr", {}, [
      el("td", {}, checkbox),
      el("td", { class: "mono", text: result.address }),
      el("td", { text: result.channel || "—" }),
      el("td", { class: "mono", text: result.model || "—" }),
      el("td", { class: `num ${tone}`, text: status }),
      el("td", { class: "num", text: `${result.duration_ms} ms` }),
      el("td", { class: tone, title: result.detail || "", text: result.error ? `${issue} (${result.error}${result.detail ? `: ${result.detail}` : ""})` : issue }),
    ]);
    body.append(row);
  }
  $("#proxy-probe-summary").textContent = t(
    `检测完成：${probeResults.length} 个节点，可达 ${ok} 个，异常 ${probeResults.length - ok} 个`,
    "config.proxy.summary", probeResults.length, ok, probeResults.length - ok,
  );
  syncProbeControls();
}

async function probeProxies() {
  const button = $("#proxy-probe");
  const config = store.get("config") || {};
  const total = (config.proxies?.length || 0) + Number(config.proxyfile_count || 0);
  let completed = 0;
  let failed = false;
  let hasResults = false;
  button.disabled = true;
  button.classList.add("is-loading");
  button.setAttribute("aria-busy", "true");
  button.textContent = `${t("检测中…", "config.proxy.probing")} 0`;
  $("#proxy-probe-summary").textContent = button.textContent;
  await new Promise((resolve) => requestAnimationFrame(resolve));
  try {
    if (!total) {
      const result = await api("/api/proxies/probe", { method: "POST", body: JSON.stringify({ indexes: [] }) });
      renderProbeResults(result.results || []);
      hasResults = true;
    } else {
      const batchSize = 8;
      const byIndex = new Map();
      for (let start = 0; start < total; start += batchSize) {
        const indexes = Array.from({ length: Math.min(batchSize, total - start) }, (_, offset) => start + offset);
        const result = await api("/api/proxies/probe", { method: "POST", body: JSON.stringify({ indexes, channel: $("#proxy-probe-channel").value }) });
        for (const item of result.results || []) byIndex.set(item.index, item);
        completed = Math.min(start + indexes.length, total);
        renderProbeResults([...byIndex.values()].sort((a, b) => a.index - b.index));
        hasResults = true;
        button.textContent = `${t("检测中…", "config.proxy.probing")} ${completed}/${total}`;
        $("#proxy-probe-summary").textContent = button.textContent;
      }
      ended = true;
      ended = true;
    }
  } catch (error) {
    failed = true;
    $("#proxy-probe-summary").textContent = `${t("测活失败", "config.proxy.probeFailed")}: ${error.message}`;
    toast(error.message, true);
  } finally {
    button.disabled = false;
    button.classList.remove("is-loading");
    button.removeAttribute("aria-busy");
    button.textContent = t("一键测活", "config.proxy.probe");
    if (failed && hasResults) $("#proxy-probe-summary").textContent = `${t("检测已中断，已显示部分结果", "config.proxy.probeInterrupted")} (${completed})`;
    syncProbeControls();
  }
}

async function deleteSelectedProxies() {
  const indexes = selectedProbeIndexes();
  if (!indexes.length) return;
  if (!(await confirmDialog({ title: t("删除代理节点", "config.proxy.confirmTitle"), message: t(`确定删除所选的 ${indexes.length} 个代理节点？`, "config.proxy.confirm", indexes.length), danger: true, confirmLabel: t("删除", "common.remove") }))) return;
  try {
    const result = await api("/api/proxies/delete", { method: "POST", body: JSON.stringify({ indexes }) });
    store.set("config", result.config);
    $("#proxies-new").value = "";
    renderProbeResults([]);
    toast(t(`已删除 ${result.removed} 个代理节点`, "config.proxy.deleted", result.removed));
  } catch (error) {
    toast(error.message, true);
  }
}

async function clearConfiguredProxies() {
  const count = $("#proxies-chips").children.length;
  if (!(await confirmDialog({
    title: t("清空 WebUI 代理", "config.proxy.clearConfigured"),
    message: t(`确定清空配置中心中的 ${count} 个代理？Proxy File 和 proxies.txt 不会被修改。`, "config.proxy.clearConfiguredConfirm", count),
    danger: true,
    confirmLabel: t("清空", "config.proxy.clearAction"),
  }))) return;
  const button = $("#proxy-clear-configured");
  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  toast(t("正在清空 WebUI 代理…", "config.proxy.clearConfiguredWorking"));
  try {
    const result = await api("/api/proxies/configured", { method: "DELETE" });
    store.set("config", result.config);
    $("#proxies-new").value = "";
    renderProbeResults([]);
    toast(t("已清空 {0} 个 WebUI 代理；Proxy File 未修改", "config.proxy.clearConfiguredDone", result.removed));
  } catch (error) {
    toast(error.message, true);
  } finally {
    button.disabled = false;
    button.removeAttribute("aria-busy");
  }
}

async function importProxyFile(file) {
  if (!file) return;
  try {
    const text = await file.text();
    const imported = [];
    let skipped = 0;
    for (let line of text.replace(/^\uFEFF/, "").split(/\r?\n/)) {
      line = line.trim();
      if (!line || line.startsWith("#") || line.startsWith(";") || line.startsWith("//")) continue;
      if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(line)) line = `http://${line}`;
      try {
        const url = new URL(line);
        if (!["http:", "https:", "socks5:", "socks5h:"].includes(url.protocol) || !url.hostname || !url.port) throw new Error("invalid proxy");
      } catch {
        skipped++;
        continue;
      }
      imported.push(line);
    }
    if (!imported.length) {
      toast(t(`没有新增代理（跳过无效行 ${skipped} 行）`, "config.proxy.importEmpty", skipped), skipped > 0);
      return;
    }
    const result = await api("/api/proxies/import", { method: "POST", body: JSON.stringify({ proxies: imported }) });
    store.set("config", result.config);
    $("#proxies-new").value = "";
    toast(t(`已将 ${result.added} 个代理合并到 proxies.txt（跳过无效行 ${skipped} 行）`, "config.proxy.imported", result.added, skipped));
  } catch (error) {
    toast(error.message || t("读取文件失败", "config.proxy.importError"), true);
  } finally {
    $("#proxy-import-file").value = "";
  }
}

function renderChips(name) {
  const host = $(`#${name}-chips`);
  host.replaceChildren();
  (store.get("config")?.[name] || []).forEach((item) => {
    const remove = el("button", { type: "button", title: t("移除", "common.remove"), text: "×" });
    const chip = el("span", { class: "chip", dataset: { id: item.id } });
    remove.addEventListener("click", () => chip.remove());
    chip.append(el("span", { text: item.display }), remove);
    host.append(chip);
  });
}

function renderRestartNotice(value) {
  const notice = $("#restart-notice");
  if (value?.restart_required_fields?.length) {
    notice.textContent = t(
      `已保存但尚未生效：${value.restart_required_fields.join(", ")}。当前 API ${value.effective.listen}、WebUI ${value.effective.webui_listen}。请重启进程。`,
      "config.restartNotice",
      value.restart_required_fields.join(", "),
      value.effective.listen,
      value.effective.webui_listen,
    );
    notice.classList.remove("hidden");
  } else {
    notice.classList.add("hidden");
  }
}

function secretInputs(name) {
  const kept = [...$(`#${name}-chips`).children].map((node) => ({ id: node.dataset.id }));
  const added = $(`#${name}-new`)
    .value.split(/\r?\n/)
    .map((value) => value.trim())
    .filter(Boolean)
    .map((value) => ({ value }));
  return kept.concat(added);
}

const numberValue = (selector) => Number($(selector).value);

function fillConfig(value) {
  if (!value) return;
  $("#c-listen").value = value.listen;
  $("#c-prefer").value = value.prefer;
  $("#c-web-listen").value = value.webui.listen;
  $("#c-session").value = value.webui.session_ttl_minutes;
  $("#c-web-enabled").checked = value.webui.enabled;
  $("#c-anonymous").checked = !!value.anonymous;
  $("#c-proxyfile").value = value.proxyfile;
  $("#c-up-zen").value = value.upstream.zen;
  $("#c-up-go").value = value.upstream.go;
  $("#c-attempts").value = value.retry.max_attempts;
  $("#c-timeout").value = value.retry.timeout_seconds;
  $("#c-refresh").value = value.models.refresh_seconds;
  $("#c-protocols").value = JSON.stringify(value.models.protocols || {}, null, 2);
  $("#c-free-only").checked = !!value.models.free_only;
  const reasoning = value.reasoning || {};
  $("#c-reasoning-effort").value = reasoning.effort || "";
  $("#c-reasoning-by-model").value = JSON.stringify(reasoning.effort_by_model || {}, null, 2);
  $("#c-idle").value = value.performance.max_idle_conns;
  $("#c-idle-host").value = value.performance.max_idle_conns_per_host;
  $("#c-max-host").value = value.performance.max_conns_per_host;
  $("#c-idle-timeout").value = value.performance.idle_conn_timeout_seconds;
  $("#c-connect").value = value.performance.connect_timeout_seconds;
  $("#c-cooldown").value = value.performance.failure_cooldown_seconds;
  $("#c-attempt-timeout").value = value.performance.attempt_timeout_seconds;
  $("#c-first-event-timeout").value = value.performance.first_event_timeout_seconds || 0;
  $("#c-level").value = value.logging.level;
  $("#c-ring").value = value.logging.ring_size;
  $("#c-dump-bodies").checked = !!value.logging.dump_request_bodies;
  ["server_keys", "zen_keys", "go_keys", "proxies"].forEach(renderChips);
  renderRestartNotice(value);
}

async function save(event) {
  event.preventDefault();
  const current = store.get("config");
  if (!current) return;
  try {
    const update = {
      listen: $("#c-listen").value,
      server_keys: secretInputs("server_keys"),
      zen_keys: secretInputs("zen_keys"),
      go_keys: secretInputs("go_keys"),
      anonymous: $("#c-anonymous").checked,
      proxies: secretInputs("proxies"),
      proxyfile: $("#c-proxyfile").value,
      prefer: $("#c-prefer").value,
      upstream: { zen: $("#c-up-zen").value, go: $("#c-up-go").value },
      retry: { max_attempts: numberValue("#c-attempts"), timeout_seconds: numberValue("#c-timeout") },
      models: {
        refresh_seconds: numberValue("#c-refresh"),
        protocols: JSON.parse($("#c-protocols").value || "{}"),
        free_only: $("#c-free-only").checked,
      },
      reasoning: {
        effort: $("#c-reasoning-effort").value,
        effort_by_model: JSON.parse($("#c-reasoning-by-model").value || "{}"),
      },
      performance: {
        max_idle_conns: numberValue("#c-idle"),
        max_idle_conns_per_host: numberValue("#c-idle-host"),
        max_conns_per_host: numberValue("#c-max-host"),
        idle_conn_timeout_seconds: numberValue("#c-idle-timeout"),
        connect_timeout_seconds: numberValue("#c-connect"),
        failure_cooldown_seconds: numberValue("#c-cooldown"),
        attempt_timeout_seconds: numberValue("#c-attempt-timeout"),
        first_event_timeout_seconds: numberValue("#c-first-event-timeout"),
      },
      logging: {
        level: $("#c-level").value,
        ring_size: numberValue("#c-ring"),
        dump_request_bodies: $("#c-dump-bodies").checked,
      },
      webui: {
        enabled: $("#c-web-enabled").checked,
        listen: $("#c-web-listen").value,
        username: current.webui.username,
        session_ttl_minutes: numberValue("#c-session"),
      },
    };
    const result = await api("/api/config", { method: "PUT", body: JSON.stringify(update) });
    store.set("config", result.config);
    ["server_keys", "zen_keys", "go_keys", "proxies"].forEach(
      (name) => ($(`#${name}-new`).value = ""),
    );
    await loadDebugModels();
    toast(
      result.result.restart_required
        ? t(
            `已应用；${result.result.restart_fields.join(", ")} 需重启`,
            "toast.config.restart",
            result.result.restart_fields.join(", "),
          )
        : t("配置已保存并立即应用", "toast.config.saved"),
    );
  } catch (error) {
    toast(error.message, true);
  }
}

async function reload() {
  try {
    const result = await api("/api/config/reload", { method: "POST", body: "{}" });
    await loadConfig();
    await loadDebugModels();
    toast(
      result.restart_required
        ? t("磁盘配置已重载，监听字段需重启", "toast.config.reloadedRestart")
        : t("磁盘配置已重载", "toast.config.reloaded"),
    );
  } catch (error) {
    toast(error.message, true);
  }
}

function reveal() {
  const input = el("input", { type: "password", autocomplete: "current-password" });
  const submit = async (close) => {
    try {
      const values = await api("/api/config/reveal", {
        method: "POST",
        body: JSON.stringify({ password: input.value }),
      });
      close();
      openDialog({
        title: t("敏感配置", "modal.title"),
        size: "dialog-lg",
        body: el("pre", { text: JSON.stringify(values, null, 2) }),
        actions: [{ label: t("关闭", "modal.close") }],
      });
    } catch (error) {
      toast(error.message, true);
    }
  };
  const dialog = openDialog({
    title: t("查看敏感配置", "modal.revealTitle"),
    body: el("div", { class: "field" }, [
      el("label", { text: t("请再次输入管理密码", "config.revealPrompt") }),
      input,
    ]),
    actions: [
      { label: t("取消", "common.cancel") },
      { label: t("查看", "common.view"), kind: "btn-primary", onClick: submit },
    ],
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      submit(dialog.close);
    }
  });
}

export default {
  id: "config",
  title: "配置中心",
  sub: "验证后原子保存，并热应用可变字段。",
  init() {
    store.on("config", fillConfig);
    bindTabs($("#page-config"));
    $("#config-form").addEventListener("submit", save);
    $("#reload-config").addEventListener("click", reload);
    $("#reveal").addEventListener("click", reveal);
    $("#proxy-probe").addEventListener("click", probeProxies);
    $("#proxy-delete").addEventListener("click", deleteSelectedProxies);
    $("#proxy-clear-configured").addEventListener("click", clearConfiguredProxies);
    $("#proxy-import").addEventListener("click", () => $("#proxy-import-file").click());
    $("#proxy-import-file").addEventListener("change", (event) => importProxyFile(event.target.files?.[0]));
    ["#proxy-select-all", "#proxy-select-timeout", "#proxy-select-429", "#proxy-select-403"].forEach((selector) => $(selector).addEventListener("change", selectProbeResults));
    onChange(() => {
      const value = store.get("config");
      if (value) renderRestartNotice(value);
      // Update chip tooltips in place so pending unsaved chip removals survive a language switch.
      $$(".chips .chip button").forEach((button) => (button.title = t("移除", "common.remove")));
    });
  },
};
