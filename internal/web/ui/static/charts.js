'use strict';

let chartRange = 60;
let chartHistory = [];
function chartRows(samples, range = chartRange, end = Date.now()) {
  const begin = end - range * 60000;
  return samples.map((sample) => ({ ...sample, time: Date.parse(sample.updated_at) }))
    .filter((sample) => Number.isFinite(sample.time) && sample.time >= begin && sample.time <= end)
    .sort((a, b) => a.time - b.time);
}
function chartHTML(samples, series, percent = false, range = chartRange, end = Date.now()) {
  const duration = range * 60000;
  const begin = end - duration;
  const rows = chartRows(samples, range, end);
  const values = rows.flatMap((sample) => series.map(([key]) => sample[key])).filter(Number.isFinite);
  if (!values.length) return '<p class="sub">Метрики недоступны</p>';
  const max = percent ? 100 : Math.max(1, ...values);
  const x = (time) => 42 + (time - begin) / duration * 530;
  const y = (value) => 156 - Math.max(0, Math.min(max, value)) / max * 132;
  const lines = series.map(([key, label, color]) => {
    let segments = [], current = [], lastTime = null;
    for (const row of rows) {
      if (!Number.isFinite(row[key]) || (lastTime !== null && row.time - lastTime > 15000)) {
        if (current.length) segments.push(current);
        current = [];
      }
      if (Number.isFinite(row[key])) current.push([x(row.time), y(row[key])]);
      lastTime = row.time;
    }
    if (current.length) segments.push(current);
    return segments.map((segment) => segment.length === 1
      ? `<circle cx="${segment[0][0].toFixed(1)}" cy="${segment[0][1].toFixed(1)}" r="3" fill="${color}"/>`
      : `<polyline points="${segment.map(([px, py]) => `${px.toFixed(1)},${py.toFixed(1)}`).join(' ')}" fill="none" stroke="${color}" stroke-width="2"/>`).join('');
  }).join('');
  return `<svg viewBox="0 0 600 190" role="img" tabindex="0" aria-label="${esc(series.map(([, label]) => label).join(' и '))} за ${range} минут. Стрелки влево и вправо: наблюдения; Home и End: первое и последнее."><text x="4" y="28" class="chart-label">${max.toFixed(percent ? 0 : 1)}</text><text x="22" y="156" class="chart-label">0</text><path d="M42 24V156H572" fill="none" stroke="var(--line)"/>${lines}<line class="chart-cursor" x1="0" x2="0" y1="24" y2="156" stroke="var(--muted)" stroke-dasharray="3 3" visibility="hidden"/><text x="42" y="182" class="chart-label">−${range} мин</text><text x="500" y="182" class="chart-label">Сейчас</text></svg><div class="chart-legend">${series.map(([, label, color]) => `<span><i style="background:${color}"></i>${esc(label)}</span>`).join('')}</div><div class="chart-tooltip hidden" role="status" aria-live="polite"></div>`;
}
function bindChartInspector(element, samples, series, percent, end) {
  const svg = element.querySelector?.('svg');
  const tooltip = element.querySelector?.('.chart-tooltip');
  const cursor = element.querySelector?.('.chart-cursor');
  if (!svg || !tooltip) return;
  const range = chartRange;
  const rows = chartRows(samples, range, end);
  const begin = end - range * 60000;
  let selected = -1;
  tooltip.id = `${element.id}-tooltip`;
  svg.setAttribute('aria-describedby', tooltip.id);
  const hide = () => { tooltip.classList.add('hidden'); cursor?.setAttribute('visibility', 'hidden'); };
  const show = (index) => {
    selected = index;
    tooltip.classList.remove('hidden');
    const row = rows[index];
    if (!row) { if (tooltip.textContent !== 'Нет наблюдения в этой точке') tooltip.textContent = 'Нет наблюдения в этой точке'; cursor?.setAttribute('visibility', 'hidden'); return; }
    const x = 42 + (row.time - begin) / (range * 60000) * 530;
    cursor?.setAttribute('x1', String(x)); cursor?.setAttribute('x2', String(x)); cursor?.setAttribute('visibility', 'visible');
    const text = `${new Date(row.time).toLocaleTimeString()} · ${series.map(([key, label]) => `${label}: ${Number.isFinite(row[key]) ? `${row[key].toFixed(1)} ${percent ? '%' : 'Мбит/с'}` : '—'}`).join(' · ')}`;
    if (tooltip.textContent !== text) tooltip.textContent = text;
  };
  const pointer = (event) => {
    const rect = svg.getBoundingClientRect();
    const x = (event.clientX - rect.left) / rect.width * 600;
    const time = begin + (x - 42) / 530 * range * 60000;
    let index = -1, distance = Infinity;
    rows.forEach((row, candidate) => { const delta = Math.abs(row.time - time); if (delta < distance) { distance = delta; index = candidate; } });
    // A gap stays a gap: never interpolate an absent observation into a tooltip.
    show(x >= 42 && x <= 572 && distance <= 7500 ? index : -1);
  };
  svg.onpointermove = (event) => { if (event.pointerType !== 'touch') pointer(event); };
  svg.onclick = pointer;
  svg.onpointerleave = () => { if (document.activeElement !== svg) hide(); };
  svg.onfocus = () => show(rows.length - 1);
  svg.onblur = () => { hide(); renderCharts(chartHistory); };
  svg.onkeydown = (event) => {
    if (event.key === 'Escape') { hide(); return; }
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const index = event.key === 'Home' ? 0 : event.key === 'End' ? rows.length - 1 : event.key === 'ArrowLeft' ? Math.max(0, selected - 1) : Math.min(rows.length - 1, selected + 1);
    show(index);
  };
}
function renderCharts(samples) {
  chartHistory = samples;
  const end = Date.now();
  const charts = [
    ['usage-chart', [['cpu_percent', 'CPU', 'var(--chart-cpu, #7ea2ff)'], ['ram_percent', 'RAM', 'var(--chart-ram, #4bd39a)']], true],
    ['traffic-chart', [['rx_mbps', 'Входящий', 'var(--chart-cpu, #7ea2ff)'], ['tx_mbps', 'Исходящий', 'var(--chart-tx, #f3bd61)']], false],
  ];
  charts.forEach(([id, series, percent]) => {
    const element = $(`#${id}`);
    if (updateWorkspaceHTML(element, chartHTML(samples, series, percent, chartRange, end))) bindChartInspector(element, samples, series, percent, end);
  });
}
function initializeCharts() {
  const field = $('#chart-range');
  if (!field) return;
  field.value = String(chartRange);
  field.onchange = () => {
    chartRange = field.value === '15' ? 15 : 60;
    renderCharts(chartHistory);
  };
}
