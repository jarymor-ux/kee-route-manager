'use strict';

let panelState = null;
let panelGeneration = 0;
let panelLoading = false;
let panelMutating = false;
let panelDirty = false;
let panelNeedsRefresh = false;
let panelCertificateURL = '';
let panelCertificatePEM = '';
let panelTimer;
let panelAttempts = 0;
let panelTargetRevision = '';
let panelDeadline = 0;
let panelNavigationApproved = false;
const panelStatuses = new Set(['idle', 'prepared', 'applying', 'awaiting_confirmation', 'applied', 'rolled_back', 'failed']);
function panelTrialActive() { return ['applying', 'awaiting_confirmation'].includes(panelState?.status) || Boolean(panelTargetRevision); }
function validPanelHostname(hostname) {
  return typeof hostname === 'string' && hostname.length <= 253 && hostname === hostname.toLowerCase() && !validPanelIP(hostname) && !['localhost', 'local'].includes(hostname) && !hostname.endsWith('.local') && !hostname.endsWith('.localhost')
    && hostname.split('.').every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
}
function validPanelIP(ip) {
  if (typeof ip !== 'string') return false;
  if (/^\d+\.\d+\.\d+\.\d+$/.test(ip)) return ip.split('.').every((part) => Number(part) <= 255);
  if (!/^[a-fA-F0-9:]+$/.test(ip) || !ip.includes(':')) return false;
  try { return new URL(`https://[${ip}]/`).hostname.startsWith('['); } catch { return false; }
}
function samePanelIP(first, second) {
  if (!validPanelIP(first) || !validPanelIP(second)) return false;
  const canonical = (ip) => new URL(`https://${ip.includes(':') ? `[${ip}]` : ip}/`).hostname;
  return canonical(first) === canonical(second);
}
function validatedPanelStatus(data) {
  if (!data || typeof data.supported !== 'boolean' || !panelStatuses.has(data.status)) throw new Error('Панель вернула некорректное состояние адреса.');
  if (!data.supported) return { supported: false, status: data.status, error: typeof data.error === 'string' ? data.error : '' };
  if (!(validPanelHostname(data.hostname) || samePanelIP(data.hostname, data.listen_ip)) || !Number.isInteger(data.port) || data.port < 1 || data.port > 65535
      || !validPanelIP(data.listen_ip) || (data.revision !== undefined && typeof data.revision !== 'string') || (data.status !== 'idle' && !data.revision)
      || typeof data.dns_automatic !== 'boolean' || typeof data.certificate_changed !== 'boolean') throw new Error('Панель вернула некорректные параметры адреса.');
  let url;
  try { url = new URL(data.url); } catch { throw new Error('Некорректная ссылка панели.'); }
  if (url.protocol !== 'https:' || url.hostname.replace(/^\[|\]$/g, '') !== data.hostname || Number(url.port || 443) !== data.port
      || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Ссылка панели не соответствует её имени и порту.');
  if (data.certificate_pem !== undefined && (typeof data.certificate_pem !== 'string' || data.certificate_pem.length > 65536
      || !/^-----BEGIN CERTIFICATE-----\r?\n[A-Za-z0-9+/=\r\n]+-----END CERTIFICATE-----\s*$/.test(data.certificate_pem))) throw new Error('Панель вернула некорректный публичный сертификат.');
  if (data.confirmation_deadline && !Number.isFinite(Date.parse(data.confirmation_deadline))) throw new Error('Некорректный срок подтверждения адреса.');
  return { ...data, revision: data.revision || '', url: url.origin };
}
function panelInput() {
  const hostname = $('#panel-hostname').value.trim().toLowerCase();
  const rawPort = $('#panel-port').value.trim();
  const port = Number(rawPort);
  if (!(validPanelHostname(hostname) || samePanelIP(hostname, panelState?.listen_ip)) || !rawPort || !Number.isInteger(port) || port < 1 || port > 65535 || port === 9443) throw new Error('Укажите локальное имя или текущий IP панели и HTTPS-порт 1–65535. Другой IP недоступен; порт 9443 занят API controller.');
  return { hostname, port };
}
function panelDraftMatches() {
  try { const input = panelInput(); return input.hostname === panelState?.hostname && input.port === panelState?.port; } catch { return false; }
}
function renderPanelProgress() {
  const status = panelState?.status || 'idle';
  const step = status === 'applied' ? 3 : status === 'awaiting_confirmation' ? 2 : ['prepared', 'applying'].includes(status) ? 1 : 0;
  ['prepare', 'check', 'confirm'].forEach((name, index) => {
    const element = $(`#panel-step-${name}`);
    element.classList.toggle('complete', index < step);
    element.classList.toggle('current', index === step);
    if (index === step) element.setAttribute('aria-current', 'step');
    else element.removeAttribute('aria-current');
  });
  const deadline = Date.parse(panelState?.confirmation_deadline || '');
  const counting = status === 'awaiting_confirmation' && Number.isFinite(deadline) && deadline > 0;
  $('#panel-countdown').classList.toggle('hidden', !counting);
  if (counting) {
    const seconds = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
    $('#panel-countdown').textContent = seconds > 0
      ? `До автоматического возврата: ${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
      : 'Срок подтверждения истёк. Проверяем восстановление адреса…';
  } else $('#panel-countdown').textContent = '';
}
function updatePanelControls() {
  const allowed = authenticated && can('config.manage|users.manage') && panelState?.supported;
  const trial = panelTrialActive();
  const busy = panelLoading || panelMutating || settingsSaving || settingsLoading || settingsChecking;
  $('#panel-hostname').disabled = !allowed || busy || trial;
  $('#panel-port').disabled = !allowed || busy || trial;
  $('#panel-prepare').disabled = !allowed || busy || trial || panelNeedsRefresh;
  const prepared = panelState?.status === 'prepared' && panelDraftMatches();
  $('#panel-apply').classList.toggle('hidden', !prepared);
  $('#panel-apply').disabled = !allowed || busy || trial || panelNeedsRefresh || (panelState?.certificate_changed && !panelCertificateURL);
  const confirm = panelState?.status === 'awaiting_confirmation' && (!panelTargetRevision || panelState.revision === panelTargetRevision);
  $('#panel-confirm').classList.toggle('hidden', !confirm);
  $('#panel-confirm').disabled = !allowed || busy || !confirm || settingsDirty || panelNeedsRefresh;
  $('#panel-confirm-hint').textContent = confirm && settingsDirty ? 'Перед подтверждением отмените несохранённые настройки ниже: переход на новый адрес очистит черновик.' : '';
  $('#panel-refresh').disabled = !allowed || panelLoading || panelMutating;
  $('#panel-open').classList.toggle('hidden', !allowed || !['prepared', 'applying', 'awaiting_confirmation', 'applied'].includes(panelState?.status) || !panelDraftMatches());
  $('#panel-certificate').classList.toggle('hidden', !allowed || !panelState?.certificate_changed || !panelCertificateURL || !panelDraftMatches());
  renderPanelProgress();
}
function clearPanelCertificate() {
  if (panelCertificateURL) URL.revokeObjectURL(panelCertificateURL);
  panelCertificateURL = '';
  panelCertificatePEM = '';
  $('#panel-certificate-download').removeAttribute('href');
  $('#panel-certificate-fingerprint').textContent = '';
  $('#panel-certificate').classList.add('hidden');
}
async function updatePanelCertificate(data, generation) {
  if (!data.certificate_changed || !data.certificate_pem) { clearPanelCertificate(); return; }
  if (panelCertificatePEM === data.certificate_pem) return;
  clearPanelCertificate();
  panelCertificatePEM = data.certificate_pem;
  panelCertificateURL = URL.createObjectURL(new Blob([data.certificate_pem], { type: 'application/x-x509-ca-cert' }));
  $('#panel-certificate-download').href = panelCertificateURL;
  $('#panel-certificate-download').download = `${data.hostname}.crt`;
  if (typeof crypto === 'undefined' || !crypto.subtle || typeof atob === 'undefined') return;
  try {
    const der = Uint8Array.from(atob(data.certificate_pem.replace(/-----[^-]+-----|\s/g, '')), (char) => char.charCodeAt(0));
    const digest = await crypto.subtle.digest('SHA-256', der);
    if (generation !== panelGeneration || panelCertificatePEM !== data.certificate_pem) return;
    $('#panel-certificate-fingerprint').textContent = `SHA-256: ${Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join(':')}`;
  } catch { if (generation === panelGeneration) $('#panel-certificate-fingerprint').textContent = 'Отпечаток сертификата можно проверить после скачивания.'; }
}
function renderPanelStatus(data) {
  panelState = data;
  $('#panel-form').classList.toggle('hidden', !data.supported);
  if (!data.supported) {
    clearPanelCertificate();
    $('#panel-current').textContent = '';
    $('#panel-old-address').textContent = '—';
    $('#panel-new-address').textContent = '—';
    $('#panel-status').textContent = data.error || 'Редактор адреса недоступен. Требуется обновить стабильный launcher.';
    updatePanelControls();
    return;
  }
  const origin = window.location.origin;
  $('#panel-current').textContent = `Локальный IP: ${data.listen_ip}.`;
  $('#panel-old-address').textContent = typeof origin === 'string' && /^https?:\/\//.test(origin) ? origin : data.url;
  $('#panel-new-address').textContent = data.url;
  if (!panelDirty) {
    $('#panel-hostname').value = data.hostname;
    $('#panel-port').value = String(data.port);
  }
  $('#panel-listen-ip').value = data.listen_ip;
  $('#panel-dns').textContent = data.dns_automatic
    ? 'На Keenetic локальная DNS-запись создаётся автоматически при применении. Устройства должны использовать DNS роутера.'
    : `Перед применением настройте в локальном DNS запись ${data.hostname} → ${data.listen_ip}. Публичная регистрация домена не требуется.`;
  $('#panel-preview').textContent = `Подготовленный адрес: ${data.url}`;
  $('#panel-open').href = `${data.url}/#settings`;
  const messages = {
    idle: 'Действующий адрес загружен.', prepared: 'Адрес подготовлен. Проверьте DNS и доверие сертификату, затем примените.',
    applying: 'Применяем адрес. Дождитесь возможности открыть его.', awaiting_confirmation: 'Откройте новый адрес, проверьте вход и подтвердите работу.',
    applied: 'Новый адрес подтверждён.', rolled_back: 'Прежний адрес автоматически восстановлен. Подготовьте изменение заново.', failed: 'Не удалось применить адрес. Действующие настройки сохранены.',
  };
  $('#panel-status').textContent = messages[data.status] + (data.status === 'awaiting_confirmation' && data.confirmation_deadline ? ` Подтверждение до ${new Date(data.confirmation_deadline).toLocaleTimeString()}.` : '');
  if (data.error) $('#panel-error').textContent = String(data.error);
  updatePanelCertificate(data, panelGeneration).then(() => { if (panelState === data) updatePanelControls(); });
  updatePanelControls();
}
function clearPanelEditor() {
  panelGeneration++;
  clearTimeout(panelTimer);
  panelTimer = undefined;
  panelState = null;
  panelLoading = false;
  panelMutating = false;
  panelDirty = false;
  panelNeedsRefresh = false;
  panelTargetRevision = '';
  panelDeadline = 0;
  panelAttempts = 0;
  panelNavigationApproved = false;
  clearPanelCertificate();
  ['panel-hostname', 'panel-port', 'panel-listen-ip'].forEach((id) => { $(`#${id}`).value = ''; });
  ['panel-current', 'panel-status', 'panel-dns', 'panel-preview', 'panel-error', 'panel-confirm-hint', 'panel-old-address', 'panel-new-address', 'panel-countdown'].forEach((id) => { $(`#${id}`).textContent = ''; });
  $('#panel-open').removeAttribute('href');
  $('#panel-form').classList.add('hidden');
  updatePanelControls();
}
async function loadPanelStatus(force = false) {
  if (!authenticated || !can('config.manage|users.manage') || panelLoading || panelMutating || (!force && panelTrialActive())) return;
  const generation = panelGeneration;
  panelLoading = true;
  updatePanelControls();
  try {
    const data = validatedPanelStatus(await api('/api/v1/panel/status'));
    if (generation !== panelGeneration) return;
    if (force) { panelNeedsRefresh = false; $('#panel-error').textContent = ''; }
    renderPanelStatus(data);
    if (data.supported && ['applying', 'awaiting_confirmation'].includes(data.status)) {
      panelTargetRevision = data.revision;
      beginPanelPolling();
    }
  } catch (error) { if (generation === panelGeneration) { $('#panel-error').textContent = error.message; $('#panel-status').textContent = 'Не удалось получить адрес панели. ' + error.message; } }
  finally { if (generation === panelGeneration) { panelLoading = false; updateSettingsControls(); } }
}
async function preparePanel(event) {
  event.preventDefault();
  if (!authenticated || !can('config.manage|users.manage') || !panelState?.supported || panelLoading || panelMutating || panelTrialActive() || panelNeedsRefresh || settingsSaving || settingsLoading || settingsChecking) return;
  let input;
  try { input = panelInput(); } catch (error) { $('#panel-error').textContent = error.message; return; }
  const generation = ++panelGeneration;
  panelMutating = true;
  $('#panel-error').textContent = '';
  updateSettingsControls();
  try {
    const fixedIP = panelState.listen_ip;
    const data = validatedPanelStatus(await api('/api/v1/panel/prepare', { method: 'POST', body: JSON.stringify(input) }));
    if (generation !== panelGeneration) return;
    if (!data.supported || data.status !== 'prepared' || data.hostname !== input.hostname || data.port !== input.port || data.listen_ip !== fixedIP) throw new Error('Подготовленный адрес не соответствует запросу. Обновите состояние.');
    panelDirty = false;
    renderPanelStatus(data);
  } catch (error) { if (generation === panelGeneration) { $('#panel-error').textContent = error.message; panelNeedsRefresh = true; } }
  finally { if (generation === panelGeneration) { panelMutating = false; updateSettingsControls(); } }
}
function beginPanelPolling() {
  clearTimeout(panelTimer);
  panelAttempts = 0;
  const serverDeadline = Date.parse(panelState?.confirmation_deadline || '');
  panelDeadline = Math.min(Date.now() + 315000, Number.isFinite(serverDeadline) && serverDeadline > 0 ? serverDeadline + 15000 : Date.now() + 315000);
  panelTimer = setTimeout(() => pollPanelTrial(panelGeneration), 2000);
}
async function applyPanel() {
  if (!authenticated || !can('config.manage|users.manage') || panelState?.status !== 'prepared' || !panelDraftMatches() || panelLoading || panelMutating || panelTrialActive() || panelNeedsRefresh || (panelState.certificate_changed && !panelCertificateURL) || settingsSaving || settingsChecking || settingsLoading) return;
  const generation = ++panelGeneration;
  const revision = panelState.revision;
  panelTargetRevision = revision;
  panelMutating = true;
  $('#panel-error').textContent = '';
  updateSettingsControls();
  try {
    const result = await api('/api/v1/panel/apply', { method: 'POST', body: JSON.stringify({ revision }) });
    if (generation !== panelGeneration) return;
    if (!result.accepted) throw Object.assign(new Error('Панель не приняла изменение адреса.'), { status: 409 });
    panelDirty = false;
    renderPanelStatus({ ...panelState, status: 'applying' });
    beginPanelPolling();
  } catch (error) {
    if (generation !== panelGeneration) return;
    if (!error.status) {
      $('#panel-status').textContent = 'Связь прервалась. Проверяем состояние адреса без повторного применения…';
      beginPanelPolling();
    } else {
      panelTargetRevision = '';
      panelNeedsRefresh = true;
      $('#panel-error').textContent = error.message;
    }
  } finally { if (generation === panelGeneration) { panelMutating = false; updateSettingsControls(); } }
}
async function pollPanelTrial(generation) {
  if (generation !== panelGeneration || !authenticated || !can('config.manage|users.manage') || !panelTrialActive() || panelMutating) return;
  panelAttempts++;
  try {
    const data = validatedPanelStatus(await api('/api/v1/panel/status', typeof AbortSignal === 'undefined' ? {} : { signal: AbortSignal.timeout(8000) }));
    if (generation !== panelGeneration) return;
    if (data.supported && data.revision === panelTargetRevision) {
      renderPanelStatus(data);
      if (['applied', 'rolled_back', 'failed', 'idle'].includes(data.status)) {
        panelTargetRevision = '';
        clearTimeout(panelTimer);
        updateSettingsControls();
        return;
      }
    } else if (data.supported && ['rolled_back', 'failed', 'idle'].includes(data.status)) {
      // Some launchers report the restored configuration revision after rollback.
      panelTargetRevision = '';
      panelNeedsRefresh = true;
      $('#panel-status').textContent = 'Проверка завершилась. Обновите состояние, чтобы увидеть действующий адрес.';
      updateSettingsControls();
      return;
    }
  } catch { if (generation !== panelGeneration) return; }
  if (generation !== panelGeneration || !authenticated || !can('config.manage|users.manage')) return;
  if (panelAttempts >= 150 || Date.now() >= panelDeadline) {
    panelTargetRevision = '';
    panelNeedsRefresh = true;
    panelState = panelState ? { ...panelState, status: 'failed' } : null;
    $('#panel-error').textContent = 'Не удалось получить итог изменения адреса. Без подтверждения launcher возвращает прежний адрес через 5 минут. Обновите состояние по прежнему адресу.';
    $('#panel-status').textContent = 'Связь с панелью требует проверки.';
    updateSettingsControls();
    return;
  }
  panelTimer = setTimeout(() => pollPanelTrial(generation), 2000);
}
async function confirmPanel() {
  if (!authenticated || !can('config.manage|users.manage') || panelState?.status !== 'awaiting_confirmation' || panelLoading || panelMutating || settingsDirty || settingsSaving || settingsLoading || settingsChecking || panelNeedsRefresh) return;
  const generation = ++panelGeneration;
  const target = validatedPanelStatus(panelState);
  panelMutating = true;
  clearTimeout(panelTimer);
  $('#panel-error').textContent = '';
  updateSettingsControls();
  try {
    const result = await api('/api/v1/panel/confirm', { method: 'POST', body: JSON.stringify({ revision: target.revision }) });
    if (generation !== panelGeneration) return;
    if (!result.accepted) throw Object.assign(new Error('Подтверждение не принято. Обновите состояние.'), { status: 409 });
    panelNavigationApproved = true;
    $('#panel-status').textContent = 'Адрес подтверждён. Открываем его; при смене имени потребуется войти снова.';
    window.location.assign(`${target.url}/#settings`);
  } catch (error) {
    if (generation !== panelGeneration) return;
    panelNeedsRefresh = Boolean(error.status);
    $('#panel-error').textContent = error.status ? error.message : 'Ответ на подтверждение потерян. Проверьте новый адрес; повторное подтверждение автоматически не отправляется.';
    beginPanelPolling();
  } finally { if (generation === panelGeneration) { panelMutating = false; updateSettingsControls(); } }
}
function initializePanelEditor() {
  $('#panel-form').addEventListener('submit', preparePanel);
  const changed = () => {
    if (panelMutating || panelTrialActive()) return;
    panelDirty = !panelDraftMatches();
    $('#panel-error').textContent = '';
    updatePanelControls();
  };
  $('#panel-hostname').addEventListener('input', changed);
  $('#panel-port').addEventListener('input', changed);
  $('#panel-apply').onclick = applyPanel;
  $('#panel-confirm').onclick = confirmPanel;
  $('#panel-refresh').onclick = () => loadPanelStatus(true);
}

