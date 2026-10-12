'use strict';

let componentsSnapshot = null;
let componentsActiveID = 'xray';
let componentsRequestGeneration = 0;
let componentsBusy = false;

const componentsNames = {
  xray: 'Xray-core',
  xkeen: 'XKeen',
  amneziawg: 'AmneziaWG',
};

function renderComponents(snapshot) {
  componentsSnapshot = snapshot;
  const items = Array.isArray(snapshot?.items) ? snapshot.items : [];
  const nav = $('#components-list');
  nav.replaceChildren();
  for (const item of items) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'ghost compact';
    button.textContent = componentsNames[item.id] || item.id;
    button.setAttribute('aria-pressed', String(componentsActiveID === item.id));
    button.addEventListener('click', () => {
      componentsActiveID = item.id;
      renderComponents(componentsSnapshot);
    });
    nav.append(button);
  }
  if (!items.some(item => item.id === componentsActiveID)) componentsActiveID = items[0]?.id || '';
  for (const button of nav.querySelectorAll('button')) {
    button.setAttribute('aria-pressed', String(button.textContent === componentsNames[componentsActiveID]));
  }
  const item = items.find(component => component.id === componentsActiveID);
  const details = $('#components-details');
  if (!item) {
    details.textContent = 'Компоненты недоступны';
    return;
  }
  details.replaceChildren();
  const title = document.createElement('h3');
  title.textContent = componentsNames[item.id] || item.id;
  const status = document.createElement('p');
  status.className = 'sub';
  status.textContent = item.error
    ? 'Проверка: ' + item.error
    : item.missing ? 'Не установлен'
      : item.update_available ? 'Доступно обновление'
        : 'Установлен';
  const versions = document.createElement('p');
  versions.className = 'sub';
  versions.textContent = 'Установлено: ' + (item.installed_version || '—') +
    ' · Доступно: ' + (item.latest_version || 'неизвестно');
  const install = document.createElement('button');
  install.type = 'button';
  install.className = 'primary';
  install.textContent = item.missing ? 'Установить' : 'Обновить';
  install.disabled = componentsBusy || !item.latest_version || (!item.missing && !item.update_available);
  install.addEventListener('click', () => installComponent(item.id));
  details.append(title, status, versions, install);
  $('#component-check').disabled = componentsBusy;
  $('#components-status').textContent = snapshot.checked_at
    ? 'Последняя проверка: ' + new Date(snapshot.checked_at).toLocaleString()
    : 'Проверка ещё не выполнялась';
}

async function loadComponents(manual = false) {
  if (!authenticated || !can('updates.manage')) return;
  const generation = ++componentsRequestGeneration;
  componentsBusy = true;
  $('#component-check').disabled = true;
  try {
    const data = manual
      ? await api('/api/v1/components/check', { method: 'POST' })
      : await api('/api/v1/components/status');
    if (generation !== componentsRequestGeneration || !authenticated) return;
    renderComponents(data);
  } catch (error) {
    if (generation === componentsRequestGeneration) {
      $('#components-status').textContent = 'Не удалось проверить компоненты';
      toast(error.message, true);
    }
  } finally {
    if (generation === componentsRequestGeneration) {
      componentsBusy = false;
      $('#component-check').disabled = false;
    }
  }
}

async function installComponent(id) {
  if (componentsBusy || !authenticated || !can('updates.manage')) return;
  const label = componentsNames[id] || id;
  if (!window.confirm('Установить или обновить ' + label + '? Работающие VPN-подключения могут быть прерваны.')) return;
  componentsBusy = true;
  $('#component-check').disabled = true;
  try {
    await api('/api/v1/components/install', { method: 'POST', body: JSON.stringify({ id }) });
    toast(label + ': установка завершена');
    await loadComponents(true);
  } catch (error) {
    toast(error.message, true);
  } finally {
    componentsBusy = false;
    if (componentsSnapshot) renderComponents(componentsSnapshot);
  }
}

function initializeComponents() {
  $('#component-check').addEventListener('click', () => loadComponents(true));
}
