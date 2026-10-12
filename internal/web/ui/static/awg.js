'use strict';

let awgProfilesBusy = false;
let awgProfilesGeneration = 0;
let awgProfilesList = [];

function renderAWGProfiles() {
  const container = $('#awg-profiles');
  container.replaceChildren();
  for (const profile of awgProfilesList) {
    const row = document.createElement('div');
    row.className = 'card';
    const name = document.createElement('strong');
    name.textContent = profile.name || profile.id;
    const details = document.createElement('p');
    details.className = 'sub';
    details.textContent = profile.endpoint || 'AmneziaWG';
    row.append(name, details);
    if (can('subscriptions.manage')) {
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'danger-outline';
      remove.textContent = 'Удалить';
      remove.disabled = awgProfilesBusy;
      remove.addEventListener('click', () => deleteAWGProfile(profile.id));
      row.append(remove);
    }
    container.append(row);
  }
  if (!awgProfilesList.length) {
    const placeholder = document.createElement('p');
    placeholder.className = 'sub';
    placeholder.textContent = 'Профилей AmneziaWG пока нет';
    container.append(placeholder);
  }
  $('#awg-upload').disabled = awgProfilesBusy || !can('subscriptions.manage');
  $('#awg-status').textContent = awgProfilesList.length + ' профилей';
}

async function loadAWGProfiles() {
  if (!authenticated || !can('vpn.view|subscriptions.manage')) return;
  const generation = ++awgProfilesGeneration;
  try {
    const data = await api('/api/v1/awg/profiles');
    if (generation !== awgProfilesGeneration || !authenticated) return;
    awgProfilesList = Array.isArray(data.profiles) ? data.profiles : [];
    renderAWGProfiles();
  } catch (error) {
    if (generation === awgProfilesGeneration) {
      $('#awg-status').textContent = 'Не удалось загрузить профили';
      toast(error.message, true);
    }
  }
}

async function importAWGProfile(event) {
  event.preventDefault();
  if (awgProfilesBusy || !authenticated || !can('subscriptions.manage')) return;
  const file = $('#awg-conf').files?.[0];
  const name = $('#awg-name').value.trim();
  if (!file || file.size === 0 || file.size > 256 * 1024 || !file.name.toLowerCase().endsWith('.conf')) {
    toast('Выберите корректный .conf размером не более 256 КиБ', true);
    return;
  }
  if (!name || name.length > 128) {
    toast('Укажите название профиля', true);
    return;
  }
  const form = new FormData();
  form.append('name', name);
  form.append('config', file, file.name);
  awgProfilesBusy = true;
  $('#awg-upload').disabled = true;
  try {
    await api('/api/v1/awg/import', { method: 'POST', body: form });
    $('#awg-conf').value = '';
    toast('Профиль AmneziaWG сохранён');
    await loadAWGProfiles();
  } catch (error) {
    toast(error.message, true);
  } finally {
    awgProfilesBusy = false;
    renderAWGProfiles();
  }
}

async function deleteAWGProfile(id) {
  if (awgProfilesBusy || !can('subscriptions.manage') || !window.confirm('Удалить профиль AmneziaWG?')) return;
  awgProfilesBusy = true;
  renderAWGProfiles();
  try {
    await api('/api/v1/awg/delete', { method: 'POST', body: JSON.stringify({ id }) });
    await loadAWGProfiles();
    toast('Профиль удалён');
  } catch (error) {
    toast(error.message, true);
  } finally {
    awgProfilesBusy = false;
    renderAWGProfiles();
  }
}

function initializeAWGProfiles() {
  $('#awg-form').addEventListener('submit', importAWGProfile);
}
