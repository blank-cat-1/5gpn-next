'use strict';

// 5gpn-NEXT 管理面板前端。零依赖、无框架，避免给网关引入额外供应链。

const $ = (id) => document.getElementById(id);
let currentStatus = null;
let accessDenied = false;

function denyAccess(message) {
  accessDenied = true;
  closeSubscriptionPicker();
  $('management-content').hidden = true;
  $('management-dock').hidden = true;
  $('auth-notice').hidden = false;
  $('auth-title').textContent = '无法访问内网面板';
  $('auth-message').textContent = message;
  $('connection-state').textContent = '无法连接';
}

// ---------- 通用 ----------

function toast(msg, isErr) {
  const node = $('toast');
  node.textContent = msg;
  node.className = 'toast' + (isErr ? ' toast-err' : '');
  node.hidden = false;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => { node.hidden = true; }, isErr ? 5000 : 2800);
}

async function api(path, opts) {
  if (accessDenied) throw new Error('请连接允许的内网后重新打开面板');
  const headers = Object.assign({ 'Content-Type': 'application/json' }, opts && opts.headers);
  const res = await fetch(path, Object.assign({}, opts, { headers, cache: 'no-store' }));
  let data = null;
  try { data = await res.json(); } catch (_) { /* 忽略非 JSON 响应 */ }
  if (!res.ok) {
    if (res.status === 401 || res.status === 403) denyAccess((data && data.error) || '无管理权限');
    throw new Error((data && data.error) || ('请求失败 ' + res.status));
  }
  return data;
}

function el(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

function number(value) {
  return Number(value || 0).toLocaleString('zh-CN');
}

function relativeTime(ts) {
  if (!ts) return '—';
  const delta = Math.max(0, Math.floor(Date.now() / 1000) - Number(ts));
  if (delta < 10) return '刚刚';
  if (delta < 60) return delta + ' 秒前';
  if (delta < 3600) return Math.floor(delta / 60) + ' 分钟前';
  if (delta < 86400) return Math.floor(delta / 3600) + ' 小时前';
  return new Date(Number(ts) * 1000).toLocaleString('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  });
}

// ---------- 状态 ----------

const COUNTER_LABELS = {
  dns_query: 'DNS 查询',
  dns_direct: 'DNS 直连',
  dns_proxy: 'DNS 改写',
  dns_block: '规则命中',
  connect: 'DNS 查询',       // 兼容旧运行态键名
  blocked: '规则命中',
  handled: '接管连接',
  dial_fail: '拨号失败',
  no_host: '目标未知',
  hinted: 'DNS 线索回退',
  v6_fastfail: 'IPv6 回落',
};

function renderStatus(st) {
  currentStatus = st;
  $('connection-state').replaceChildren(el('i'), document.createTextNode('网关在线'));
  $('connection-state').classList.remove('offline');
  const grid = $('stat-grid');
  grid.textContent = '';

  const items = [
    ['运行时长', st.uptime || '—'],
    ['监听地址', st.listen || '—'],
    ['策略规则', number(st.rules) + ' 条'],
    ['运行内存', Number(st.memory_mb || 0).toFixed(1) + ' MB'],
  ];
  if (st.cert_until) items.push(['证书有效至', st.cert_until]);

  const counters = st.counters || {};
  const preferredKeys = ['dns_query', 'dns_block', 'dial_fail', 'hinted'];
  const legacyKeys = ['connect', 'blocked', 'v6_fastfail'];
  for (const key of preferredKeys) {
    if (key in counters) items.push([COUNTER_LABELS[key] || key, number(counters[key])]);
  }
  for (const key of legacyKeys) {
    if (items.length >= 8) break;
    if (key in counters && !((key === 'connect' && 'dns_query' in counters) || (key === 'blocked' && 'dns_block' in counters))) {
      items.push([COUNTER_LABELS[key] || key, number(counters[key])]);
    }
  }

  for (const [label, value] of items) {
    const box = el('div', 'stat');
    box.appendChild(el('span', 'stat-label', label));
    box.appendChild(el('span', 'stat-value', value));
    grid.appendChild(box);
  }

  const ad = st.ad_block || {};
  const hits = ad.hits || {};
  $('hero-uptime').textContent = st.uptime || '运行中';
  $('hero-dns').textContent = number(counters.dns_query !== undefined ? counters.dns_query : counters.connect);
  $('hero-blocked').textContent = number(hits.today);
  const activeEgress = (st.egress || []).find((item) => item.current);
  $('hero-egress').textContent = activeEgress ? (activeEgress.display || activeEgress.name) : 'DIRECT';

  renderAdBlock(ad);
  renderEgress(st.egress || [], st.domestic_ready);
  const testing = st.latency_test || {};
  $('egress-test-all').disabled = !!testing.running || !(st.egress || []).some(e => e.type !== 'direct' && e.name !== 'DIRECT');
  $('egress-test-all').textContent = testing.running ? ('测速中 ' + number(testing.done) + '/' + number(testing.total)) : '一键测速';
  $('egress-latency-state').textContent = testing.running ? ('正在检测已导入节点 · ' + number(testing.done) + '/' + number(testing.total)) : ('HTTP 延迟由服务器经节点测得' + (testing.total ? ' · 本轮已完成 ' + number(testing.done) + ' 个' : '，点击一键测速开始。'));
  $('home-memory').textContent = Number(st.memory_mb || 0).toFixed(1) + ' MB';
  $('home-protection-detail').textContent = ad.enabled ? ('今日已拦截 ' + number(hits.today) + ' 次') : '当前未开启';
  $('home-adblock-state').classList.toggle('on', !!ad.enabled);
  $('home-adblock-state').setAttribute('aria-checked', String(!!ad.enabled));
  $('home-adblock-state').disabled = false;
  $('home-node-count').textContent = (st.egress || []).filter(e => e.name !== 'DIRECT').length + ' 个节点 · 管理订阅';
  $('exit-caption').textContent = (activeEgress ? activeEgress.type : '—') + ' · 当前国外默认出口';
  $('home-rule-count').textContent = number(st.rules);
  $('home-run-time').textContent = '已运行 ' + (st.uptime || '—');
  $('home-domestic-state').textContent = st.domestic_ready === false ? '规则待就绪' : '规则已就绪';
  $('home-domestic-state').classList.toggle('warning', st.domestic_ready === false);
}

function renderAdBlock(ad) {
  const hits = ad.hits || {};
  $('ad-today').textContent = number(hits.today);
  $('ad-total').textContent = number(hits.total);
  $('ad-domains').textContent = number(ad.domains);
  $('ad-allow-count').textContent = number(ad.allowlist);

  const toggle = $('adblock-toggle');
  toggle.disabled = false;
  toggle.dataset.enabled = ad.enabled ? 'true' : 'false';
  toggle.textContent = ad.enabled ? '● 已开启' : '○ 已关闭';
  toggle.className = 'btn btn-state ' + (ad.enabled ? 'btn-on' : 'btn-off');

  const recent = $('ad-recent');
  recent.textContent = '';
  const events = hits.recent || [];
  if (!events.length) {
    recent.appendChild(el('p', 'empty', ad.enabled ? '暂无成功拦截，等待首次命中。' : '开启后将在这里显示成功命中。'));
  } else {
    events.slice(0, 12).forEach((event) => {
      const row = el('div', 'hit-row');
      const main = el('div', 'hit-main');
      main.appendChild(el('span', 'hit-mark', '↳'));
      main.appendChild(el('code', 'hit-host', event.host));
      row.appendChild(main);
      row.appendChild(el('time', 'hit-time', relativeTime(event.at)));
      recent.appendChild(row);
    });
  }

  const top = $('ad-top');
  top.textContent = '';
  const domains = hits.top || [];
  if (!domains.length) {
    top.appendChild(el('p', 'empty', '暂无高频域名统计。'));
  } else {
    const max = Math.max(1, Number(domains[0].count || 0));
    domains.slice(0, 8).forEach((item, index) => {
      const row = el('div', 'top-row');
      const meta = el('div', 'top-meta');
      meta.appendChild(el('span', 'top-rank', String(index + 1).padStart(2, '0')));
      meta.appendChild(el('code', 'hit-host', item.host));
      meta.appendChild(el('strong', 'top-count', number(item.count)));
      row.appendChild(meta);
      const track = el('div', 'top-track');
      const fill = el('i', 'top-fill');
      fill.style.width = Math.max(4, Number(item.count || 0) / max * 100) + '%';
      track.appendChild(fill);
      row.appendChild(track);
      top.appendChild(row);
    });
  }

  const allowBox = $('ad-allow-list');
  allowBox.textContent = '';
  const allow = ad.allow_domains || [];
  if (!allow.length) {
    allowBox.appendChild(el('span', 'empty', '暂无白名单'));
  } else {
    allow.forEach((domain, index) => {
      const chip = el('span', 'allow-chip');
      chip.appendChild(el('code', null, domain));
      const rm = el('button', null, '×');
      rm.type = 'button';
      rm.title = '移除 ' + domain;
      rm.setAttribute('aria-label', '移除白名单 ' + domain);
      rm.onclick = () => adBlockAction('remove_allow', { index });
      chip.appendChild(rm);
      allowBox.appendChild(chip);
    });
  }
}

async function adBlockAction(action, extra) {
  try {
    const data = await api('/api/adblock', {
      method: 'POST',
      body: JSON.stringify(Object.assign({ action }, extra || {})),
    });
    if (data.status) renderStatus(data.status);
    toast(data.message || '广告拦截设置已更新');
  } catch (err) {
    toast(err.message, true);
  }
}

// ---------- 出口 ----------

function renderEgress(list, domesticReady) {
  const box = $('egress-list');
  box.textContent = '';

  if (domesticReady === false) {
    box.appendChild(el('p', 'empty warning', '注意：国内规则未就绪，系统已安全回落到本机直连，暂不允许切换代理出口。'));
  }
  if (!list.length) {
    box.appendChild(el('p', 'empty', '暂无出口'));
    return;
  }

  for (const e of list) {
    const row = el('div', 'row');
    const main = el('div', 'row-main');
    const title = el('div', 'row-title');
    title.appendChild(el('span', null, e.display || e.name));
    if (e.current) title.appendChild(el('span', 'tag tag-current', '当前国外出口'));
    title.appendChild(el('span', 'tag', e.type));
    if (e.subscription_id) title.appendChild(el('span', 'tag', '订阅导入'));
    if (e.name !== 'DIRECT') {
      const delay = e.latency || {};
      let label = '未测速';
      let state = 'untested';
      if (delay.state === 'ok') { label = Math.max(1, Number(delay.ms || 0)) + ' ms'; state = delay.ms < 150 ? 'fast' : delay.ms < 400 ? 'medium' : 'slow'; }
      else if (delay.state === 'error') { label = '超时 / 失败'; state = 'failed'; }
      else if (delay.state === 'testing') { label = '测速中…'; state = 'testing'; }
      else if (delay.state === 'queued') { label = '等待测速'; state = 'queued'; }
      const badge = el('span', 'latency-badge latency-' + state, label);
      badge.title = delay.checked_at ? 'HTTP 延迟 · ' + new Date(delay.checked_at * 1000).toLocaleString('zh-CN') : '服务器经节点访问测试地址的 HTTP 延迟';
      title.appendChild(badge);
    }
    main.appendChild(title);
    if (e.server) main.appendChild(el('div', 'row-sub', e.server));
    row.appendChild(main);

    const acts = el('div', 'row-actions');
    if (!e.current) {
      const sw = el('button', 'btn btn-sm', '设为默认');
      sw.onclick = () => egressAction('switch', { name: e.name });
      acts.appendChild(sw);
    }
    const test = el('button', 'btn btn-sm', e.name === 'DIRECT' ? '测试' : '测速');
    test.disabled = e.latency && ['queued', 'testing'].includes(e.latency.state);
    test.onclick = () => egressAction('test', { name: e.name });
    acts.appendChild(test);
    if (e.name !== 'DIRECT' && !e.subscription_id) {
      const rm = el('button', 'btn btn-sm btn-danger', '删除');
      rm.onclick = () => {
        if (confirm('确定删除出口 ' + e.name + '？')) egressAction('remove', { name: e.name });
      };
      acts.appendChild(rm);
    }
    row.appendChild(acts);
    box.appendChild(row);
  }
}

async function loadStatus() {
  try {
    renderStatus(await api('/api/status'));
  } catch (err) {
    if (!accessDenied) { $('connection-state').textContent = '连接中断'; $('connection-state').classList.add('offline'); }
    toast(err.message, true);
  }
}

async function egressAction(action, extra) {
  try {
    const data = await api('/api/egress', {
      method: 'POST',
      body: JSON.stringify(Object.assign({ action }, extra)),
    });
    toast(data.message || '操作完成');
    if (data.status) renderStatus(data.status);
  } catch (err) {
    toast(err.message, true);
  }
}
$('egress-test-all').addEventListener('click', async () => {
  const button = $('egress-test-all'); button.disabled = true;
  try {
    const data = await api('/api/egress-latency', {method:'POST',body:'{}'});
    if (data.status) renderStatus(data.status);
    toast(data.message || '已开始测速');
  } catch (err) { toast(err.message, true); button.disabled = false; }
});
setInterval(() => {
  if (activePage === 'nodes' && !document.hidden && currentStatus && currentStatus.latency_test && currentStatus.latency_test.running && !accessDenied) loadStatus();
}, 1000);

// ---------- 节点订阅 ----------
let subscriptionBusy = false;
let subscriptionsLoading = false;
let subscriptionsRequest = 0;
function renderSubscriptions(list) {
  const box = $('subscriptions-list');
  box.replaceChildren();
  if (!list.length) box.appendChild(el('p', 'empty', '还没有订阅，添加后即可在下方选择节点。'));
  for (const item of list) {
    const row = el('div', 'row');
    const main = el('div', 'row-main');
    main.appendChild(el('div', 'row-title', item.name));
    main.appendChild(el('div', 'row-sub', item.host + ' · 候选 ' + number(item.available) + ' 个 · 已导入 ' + number(item.nodes) + ' 个'));
    main.appendChild(el('div', 'row-sub', '最近更新：' + relativeTime(item.updated_at)));
    const actions = el('div', 'row-actions');
    const choose = el('button', 'btn btn-sm', '选择节点');
    choose.dataset.subscriptionId = item.id;
    choose.disabled = subscriptionBusy;
    choose.onclick = () => openSubscriptionPicker(item.id);
    const update = el('button', 'btn btn-sm', '更新');
    update.disabled = subscriptionBusy;
    update.onclick = () => subscriptionAction('update', {id: item.id});
    const remove = el('button', 'btn btn-sm btn-danger', '删除');
    remove.disabled = subscriptionBusy;
    remove.onclick = () => {
      if (confirm('删除订阅“' + item.name + '”及其导入节点？正在被规则或默认出口使用的节点需要先调整。')) subscriptionAction('remove', {id: item.id});
    };
    actions.append(choose, update, remove);
    row.append(main, actions);
    box.appendChild(row);
  }
}
async function loadSubscriptions() {
  if (subscriptionsLoading || subscriptionBusy || accessDenied) return;
  subscriptionsLoading = true;
  const request = ++subscriptionsRequest;
  try {
    const data = await api('/api/subscriptions');
    if (request === subscriptionsRequest && !subscriptionBusy) renderSubscriptions(data.subscriptions || []);
  }
  catch (err) { if (request === subscriptionsRequest) $('subscriptions-list').replaceChildren(el('p', 'empty', err.message)); }
  finally { if (request === subscriptionsRequest) subscriptionsLoading = false; }
}
async function subscriptionAction(action, extra) {
  if (subscriptionBusy) return;
  subscriptionBusy = true;
  subscriptionsRequest++;
  subscriptionsLoading = false;
  $('subscription-add').disabled = true;
  $('subscription-add').textContent = action === 'add' ? '正在添加…' : '处理中…';
  $('subscription-import').disabled = true;
  $('subscription-picker-error').hidden = true;
  let openID = '';
  $('subscriptions-list').querySelectorAll('button').forEach(button => button.disabled = true);
  if (candidateData) renderCandidates();
  try {
    const result = await api('/api/subscriptions', {method: 'POST', body: JSON.stringify({action, ...extra})});
    if (result.status) renderStatus(result.status);
    if (action === 'add') $('subscription-form').reset();
    if (action === 'add' && result.subscriptions && result.subscriptions.length) openID = result.subscriptions[result.subscriptions.length-1].id;
    if (action === 'update' && candidateData && candidateData.id === extra.id) openID = extra.id;
    if (action === 'import') closeSubscriptionPicker();
    if (action === 'remove' && candidateData && candidateData.id === extra.id) closeSubscriptionPicker();
    toast(result.message || '订阅已更新');
  } catch (err) {
    if (action === 'import' && $('subscription-picker').open) {
      $('subscription-picker-error').textContent = err.message;
      $('subscription-picker-error').hidden = false;
    }
    toast(err.message, true);
  }
  finally {
    subscriptionBusy = false;
    $('subscription-add').disabled = false;
    $('subscription-add').textContent = '添加订阅';
    await loadSubscriptions();
    if (openID) await openSubscriptionPicker(openID);
    else if (candidateData) renderCandidates();
    updateCandidateSelection();
  }
}
$('subscription-form').addEventListener('submit', event => {
  event.preventDefault();
  subscriptionAction('add', {name: $('subscription-name').value.trim(), url: $('subscription-url').value.trim()});
});
$('subscriptions-refresh').addEventListener('click', loadSubscriptions);

let candidateData = null;
let candidateSelection = new Set();
let candidateRequest = 0;
let candidateOpener = null;
let pickerScrollY = 0;
function closeSubscriptionPicker() {
  candidateRequest++;
  candidateData = null;
  candidateSelection.clear();
  const dialog = $('subscription-picker');
  const wasOpen = dialog.open;
  if (wasOpen) dialog.close();
  dialog.hidden = true;
  $('subscription-picker-error').hidden = true;
  if (document.body.classList.contains('picker-open')) {
    document.body.classList.remove('picker-open');
    document.body.style.top = '';
    window.scrollTo({top:pickerScrollY,behavior:'instant'});
  }
  if (wasOpen && !accessDenied && candidateOpener && candidateOpener.isConnected) candidateOpener.focus({preventScroll:true});
  candidateOpener = null;
}
async function openSubscriptionPicker(id) {
  const request = ++candidateRequest;
  try {
    const data = await api('/api/subscriptions?id=' + encodeURIComponent(id));
    if (request !== candidateRequest || accessDenied) return;
    candidateData = data;
    candidateSelection = new Set();
    $('subscription-search').value = '';
    $('subscription-picker-name').textContent = data.name + ' · ' + number((data.nodes || []).length) + ' 个候选节点';
    const dialog = $('subscription-picker');
    $('subscription-picker-error').hidden = true;
    dialog.hidden = false;
    renderCandidates();
    if (!dialog.open) {
      candidateOpener = document.activeElement;
      pickerScrollY = window.scrollY;
      document.body.style.top = -pickerScrollY + 'px';
      document.body.classList.add('picker-open');
      dialog.showModal();
    }
  } catch (err) { toast(err.message, true); }
}
function matchingCandidates() {
  const query = $('subscription-search').value.trim().toLocaleLowerCase();
  return ((candidateData && candidateData.nodes) || []).filter(n => (n.name + ' ' + n.type + ' ' + n.server).toLocaleLowerCase().includes(query));
}
function updateCandidateSelection() {
  $('subscription-selected-count').textContent = '已选 ' + candidateSelection.size + ' 个';
  $('subscription-import').textContent = subscriptionBusy ? '正在导入…' : candidateSelection.size ? '导入选中节点（' + candidateSelection.size + '）' : '导入选中节点';
  $('subscription-import').disabled = subscriptionBusy || !candidateSelection.size;
}
function renderCandidates() {
  const box = $('subscription-candidates'); box.replaceChildren();
  const list = matchingCandidates();
  if (!list.length) box.appendChild(el('p','empty',candidateData && !candidateData.nodes.length ? '暂无候选节点，请先更新此订阅。' : '没有匹配的节点'));
  for (const item of list) {
    const row = el('label','candidate-row' + (item.imported ? ' imported' : ''));
    const check = el('input'); check.type = 'checkbox'; check.value = item.key;
    check.checked = item.imported || candidateSelection.has(item.key);
    check.disabled = item.imported || subscriptionBusy;
    check.setAttribute('aria-label', (item.imported ? '已导入：' : '选择节点：') + item.name);
    check.addEventListener('change', () => { if (check.checked) candidateSelection.add(item.key); else candidateSelection.delete(item.key); row.classList.toggle('selected',check.checked); updateCandidateSelection(); });
    row.classList.toggle('selected',check.checked && !item.imported);
    const indicator = el('span','candidate-indicator');
    indicator.setAttribute('aria-hidden','true');
    indicator.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><path d="m6 12 4 4 8-8"/></svg>';
    const main = el('span','candidate-main');
    main.append(el('strong',null,item.name), el('small',null,item.type + ' · ' + item.server));
    row.append(check,main,indicator);
    if (item.imported) row.appendChild(el('span','tag','已导入'));
    box.appendChild(row);
  }
  updateCandidateSelection();
}
$('subscription-picker-close').addEventListener('click', closeSubscriptionPicker);
$('subscription-picker').addEventListener('cancel', event => { event.preventDefault(); closeSubscriptionPicker(); });
$('subscription-picker').addEventListener('close', () => { if (!$('subscription-picker').open && !$('subscription-picker').hidden) closeSubscriptionPicker(); });
$('subscription-picker').addEventListener('click', event => {
  const dialog = $('subscription-picker');
  if (event.target !== dialog) return;
  const rect = dialog.getBoundingClientRect();
  if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) closeSubscriptionPicker();
});
$('subscription-search').addEventListener('input', renderCandidates);
$('subscription-select-all').addEventListener('click', () => { if (subscriptionBusy) return; for (const n of matchingCandidates()) if (!n.imported) candidateSelection.add(n.key); renderCandidates(); });
$('subscription-select-none').addEventListener('click', () => { if (subscriptionBusy) return; candidateSelection.clear(); renderCandidates(); });
$('subscription-import').addEventListener('click', () => {
  if (candidateData && candidateSelection.size) subscriptionAction('import',{id:candidateData.id, revision:candidateData.revision, keys:[...candidateSelection]});
});

// ---------- 服务日志 ----------
let logsLoading = false;
async function loadLogs() {
  if (logsLoading || accessDenied) return;
  logsLoading = true;
  $('logs-refresh').disabled = true;
  $('logs-state').textContent = '正在读取服务日志…';
  try {
    const data = await api('/api/logs?lines=' + encodeURIComponent($('logs-lines').value));
    const box = $('service-logs');
    const nearBottom = box.scrollTop + box.clientHeight >= box.scrollHeight - 40;
    const previousScroll = box.scrollTop;
    box.replaceChildren();
    const entries = data.entries || [];
    if (!entries.length) box.appendChild(el('p', 'empty', '暂无服务日志'));
    for (const entry of entries) {
      const row = el('div', 'log-entry');
      const header = el('div', 'log-meta');
      const level = ['error', 'warning', 'info', 'debug'].includes(entry.level) ? entry.level : 'info';
      header.append(el('time', null, entry.time ? new Date(entry.time).toLocaleString('zh-CN', {hour12: false}) : '—'), el('span', 'log-level log-' + level, level.toUpperCase()));
      row.append(header, el('pre', null, entry.message));
      box.appendChild(row);
    }
    box.scrollTop = nearBottom ? box.scrollHeight : previousScroll;
    $('logs-state').textContent = entries.length + ' 条记录 · ' + new Date(data.read_at).toLocaleTimeString('zh-CN', {hour12: false}) + ' 更新';
  } catch (err) {
    // Retain previously read records if a later refresh fails.
    $('logs-state').textContent = err.message;
  } finally { logsLoading = false; $('logs-refresh').disabled = false; }
}
$('logs-refresh').addEventListener('click', loadLogs);
$('logs-lines').addEventListener('change', loadLogs);
setInterval(() => {
  if (activePage === 'logs' && !document.hidden && $('logs-auto').checked && !accessDenied) loadLogs();
}, 5000);

// ---------- 规则 ----------

function renderRules(data) {
  const box = $('custom-rules-list');
  box.textContent = '';
  const builtinBox = $('builtin-rules-list');
  builtinBox.textContent = '';
  const rules = data.rules || [];
  const pre = data.builtin_pre || [];
  const post = data.builtin_post || [];

  $('custom-rules-count').textContent = number(rules.length) + ' 条';
  $('builtin-rules-count').textContent = number(pre.length + post.length) + ' 条';
  if (!rules.length) {
    box.appendChild(el('p', 'empty', '暂无自定义规则，可在下方添加。'));
  }
  rules.forEach((rule, index) => {
    const row = el('div', 'row');
    row.appendChild(el('span', 'rule-index', String(index + 1)));
    const main = el('div', 'row-main');
    main.appendChild(el('div', 'rule-text', rule));
    row.appendChild(main);
    const acts = el('div', 'row-actions');
    const rm = el('button', 'btn btn-sm btn-danger', '删除');
    rm.onclick = async () => {
      if (!confirm('确认删除这条规则？\n\n' + rule)) return;
      try {
        await api('/api/rules?index=' + index, { method: 'DELETE' });
        await loadRules();
        toast('规则已删除');
      } catch (err) {
        toast(err.message, true);
      }
    };
    acts.appendChild(rm);
    row.appendChild(acts);
    box.appendChild(row);
  });
  pre.forEach((rule) => builtinBox.appendChild(builtinRow(rule, '内置 · 优先')));
  post.forEach((rule) => builtinBox.appendChild(builtinRow(rule, '内置 · 兜底')));
  if (!pre.length && !post.length) builtinBox.appendChild(el('p', 'empty', '暂无内置规则。'));
}

function builtinRow(rule, label) {
  const row = el('div', 'row row-builtin');
  row.appendChild(el('span', 'rule-index', '内'));
  const main = el('div', 'row-main');
  main.appendChild(el('div', 'rule-text', rule));
  row.appendChild(main);
  const acts = el('div', 'row-actions');
  acts.appendChild(el('span', 'builtin-tag', label));
  row.appendChild(acts);
  return row;
}

async function loadRules() {
  try {
    renderRules(await api('/api/rules'));
  } catch (err) {
    toast(err.message, true);
  }
}

// ---------- 诊断 ----------

const STAGE_NAMES = {
  ingress: '入口', policy: '策略', egress: '出口',
  connect: '连接', app: '应用',
};
const STATUS_MARK = {
  ok: ['✓', 'probe-ok'], fail: ['✗', 'probe-err'],
  warn: ['!', 'probe-warn'], skipped: ['–', ''],
};

async function runProbe() {
  const target = $('probe-target').value.trim();
  if (!target) { toast('请输入要诊断的域名', true); return; }

  const btn = $('probe-run');
  const out = $('probe-out');
  btn.disabled = true;
  btn.textContent = '诊断中…';
  out.hidden = false;
  out.textContent = '正在诊断 ' + target + ' …';

  try {
    const data = await api('/api/probe?target=' + encodeURIComponent(target));
    out.textContent = '';
    (data.steps || []).forEach((step, index) => {
      const mark = STATUS_MARK[step.status] || ['?', ''];
      const line = el('span', 'probe-line ' + mark[1]);
      line.textContent = '[' + (index + 1) + '] ' + (STAGE_NAMES[step.stage] || step.stage) +
        '  ' + step.detail + '  ' + mark[0] + ' ' + Number(step.dur_ms || 0).toFixed(1) + 'ms';
      out.appendChild(line);
      if (step.err) out.appendChild(el('span', 'probe-line probe-err', '        └─ ' + step.err));
    });
    const verdict = el('span', 'probe-line ' + (data.ok ? 'probe-ok' : 'probe-err'));
    verdict.textContent = '结论：' + (data.ok ? '正常' : '失败') +
      '（总计 ' + Number(data.total || 0).toFixed(1) + 'ms）';
    out.appendChild(verdict);
  } catch (err) {
    out.textContent = '诊断失败：' + err.message;
  } finally {
    btn.disabled = false;
    btn.textContent = '开始诊断';
  }
}

// ---------- 事件绑定 ----------

$('home-adblock-state').addEventListener('click', () => $('adblock-toggle').click());
$('adblock-toggle').onclick = () => {
  const enabled = currentStatus && currentStatus.ad_block && currentStatus.ad_block.enabled;
  if (enabled && !confirm('关闭后，广告域名将恢复正常解析。确认关闭？')) return;
  adBlockAction('toggle', { enabled: !enabled });
};

$('ad-allow-add').onclick = async () => {
  const input = $('ad-allow-input');
  const domain = input.value.trim();
  if (!domain) { toast('请输入要放行的域名', true); return; }
  await adBlockAction('allow', { domain });
  input.value = '';
};
$('ad-allow-input').addEventListener('keydown', (event) => {
  if (event.key === 'Enter') $('ad-allow-add').click();
});

$('egress-add').onclick = async () => {
  const link = $('egress-link').value.trim();
  if (!link) { toast('请粘贴节点链接', true); return; }
  const btn = $('egress-add');
  btn.disabled = true;
  btn.textContent = '添加中…';
  try {
    await egressAction('add', { link });
    $('egress-link').value = '';
  } finally {
    btn.disabled = false;
    btn.textContent = '添加出口';
  }
};

$('rule-add').onclick = async () => {
  const rule = $('rule-input').value.trim();
  if (!rule) { toast('请输入规则', true); return; }
  try {
    await api('/api/rules', { method: 'POST', body: JSON.stringify({ rule }) });
    await loadRules();
    $('custom-rules-group').open = true;
    $('rule-input').value = '';
    toast('规则已添加');
  } catch (err) {
    toast(err.message, true);
  }
};

$('probe-run').onclick = runProbe;
$('probe-target').addEventListener('keydown', (event) => {
  if (event.key === 'Enter') runProbe();
});
$('rule-input').addEventListener('keydown', (event) => {
  if (event.key === 'Enter') $('rule-add').click();
});


// ---------- 健康监控 ----------

function fmtBytes(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB';
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB';
  if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n || 0) + ' B';
}

function healthDot(fail, count) {
  if (!count) return '⚪';
  const r = fail / count;
  if (r >= 0.2) return '🔴';
  if (r > 0) return '🟡';
  return '🟢';
}

function renderHealth(h) {
  const list = $('health-list');
  const sys = $('health-sys');
  if (!list || !sys) return;
  list.textContent = '';
  sys.textContent = '';
  if (!h || !h.enabled) {
    list.appendChild(el('div', 'intro', '监控未启用。'));
    return;
  }
  for (const e of (h.egress || [])) {
    const row = el('div', 'row');
    const left = el('div', 'row-main');
    left.appendChild(el('strong', '', healthDot(e.probe_1h.fail, e.probe_1h.count) + ' ' + e.name));
    const bits = [];
    if (e.probe_1h.count) {
      bits.push((e.probe_kind || '探测') + ' 均 ' + e.probe_1h.avg + ' · p95 ' + e.probe_1h.p95 + ' · 失败 ' + e.probe_1h.fail + '/' + e.probe_1h.count);
    }
    if (e.fw_1h.count) {
      bits.push('转发 ' + e.fw_1h.count + ' 次 · 均 ' + e.fw_1h.avg + ' · 失败 ' + e.fw_1h.fail);
    }
    if (e.up_bytes || e.down_bytes) {
      bits.push('↑ ' + fmtBytes(e.up_bytes) + ' · ↓ ' + fmtBytes(e.down_bytes));
    }
    left.appendChild(el('span', 'muted', bits.join('　') || '暂无数据'));
    row.appendChild(left);
    list.appendChild(row);
  }
  const stats = [
    ['DNS 1h', h.dns_1h.count ? ('均 ' + h.dns_1h.avg + ' / 失败 ' + h.dns_1h.fail) : '无查询'],
    ['TCP 会话', h.tcp.active + ' / ' + h.tcp.max],
    ['QUIC 会话', h.quic.max ? (h.quic.active + ' / ' + h.quic.max) : '未启用'],
    ['内存', (h.sys.memory_mb || 0).toFixed(1) + ' MB'],
    ['goroutine', String(h.sys.goroutines)],
    ['证书剩余', h.sys.cert_days >= 0 ? (h.sys.cert_days + ' 天') : '—'],
  ];
  for (const [k, v2] of stats) {
    const s = el('div', 'stat');
    s.appendChild(el('span', 'stat-label', k));
    s.appendChild(el('span', 'stat-value', v2));
    sys.appendChild(s);
  }
}

async function loadHealth() {
  try {
    renderHealth(await api('/api/health'));
  } catch (_) { /* 健康数据失败不打扰 */ }
}


// Telegram window adaptation; browser and Mini App share the original CIDR access policy.
let tg;
function adaptTelegram() {
  tg = window.Telegram && window.Telegram.WebApp;
  if (!tg || !tg.initData) return;
  tg.ready(); tg.expand();
  tg.setHeaderColor('#932d45'); tg.setBackgroundColor('#932d45');
  const syncSafeArea = () => document.body.style.setProperty('--mini-safe-top', ((tg.contentSafeAreaInset && tg.contentSafeAreaInset.top) || 0) + 'px');
  syncSafeArea(); tg.onEvent('contentSafeAreaChanged', syncSafeArea);
  if (tg.BackButton) tg.BackButton.onClick(() => { if ($('subscription-picker').open) closeSubscriptionPicker(); else showPage('home'); });
}
adaptTelegram();
// Ordinary internal browser access has no external SDK dependency.
if (!tg && location.hash.includes('tgWebApp')) {
  const sdk = document.createElement('script');
  sdk.src = 'https://telegram.org/js/telegram-web-app.js';
  sdk.async = true;
  sdk.onload = adaptTelegram;
  document.head.appendChild(sdk);
}
const pages = {
  home: ['控制中心', '你的网络，由你掌控。'],
  rules: ['规则管理', '查看内置规则，管理自定义规则。'],
  nodes: ['节点与订阅', '管理节点，更新你的订阅。'],
  logs: ['服务日志', '查看网关的运行记录。'],
  tools: ['网络诊断', '连接状态，一眼知晓。'],
  protection: ['广告拦截', '少一点打扰，多一份清净。'],
  settings: ['偏好设置', '照你的习惯，安排一切。'],
};
let activePage = 'home';
function showPage(name, section) {
  if (!(name in pages) || accessDenied || $('management-content').hidden) return;
  if ($('subscription-picker').open || name !== activePage) closeSubscriptionPicker();
  activePage = name;
  document.querySelectorAll('[data-page]').forEach(page => { page.hidden = page.dataset.page !== name; });
  document.querySelectorAll('.dock [data-view]').forEach(button => {
    const selected = button.dataset.view === name;
    button.classList.toggle('active', selected);
    if (selected) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current');
  });
  $('page-title').textContent = pages[name][0];
  $('page-kicker').textContent = pages[name][1];
  if (tg && tg.initData && tg.BackButton) {
    if (name === 'home') tg.BackButton.hide(); else tg.BackButton.show();
  }
  window.scrollTo({top: 0, behavior: 'instant'});
  if (section && $(section)) requestAnimationFrame(() => $(section).scrollIntoView({block: 'start'}));
  if (name === 'nodes') loadSubscriptions();
  if (name === 'logs') loadLogs();
}
document.querySelectorAll('[data-view]').forEach(button => button.addEventListener('click', () => showPage(button.dataset.view, button.dataset.section)));
document.querySelector('.brand').addEventListener('click', event => { event.preventDefault(); showPage('home'); });
function setTheme(theme) {
  document.body.classList.toggle('dark', theme === 'dark');
  document.querySelectorAll('[data-theme]').forEach(button => {
    button.classList.toggle('selected', button.dataset.theme === theme);
    button.setAttribute('aria-pressed', String(button.dataset.theme === theme));
  });
  try { localStorage.setItem('5gpn-theme', theme); } catch (_) {}
}
let savedTheme = 'light';
try { if (localStorage.getItem('5gpn-theme') === 'dark') savedTheme = 'dark'; } catch (_) {}
setTheme(savedTheme);
document.querySelectorAll('[data-theme]').forEach(button => button.addEventListener('click', () => setTheme(button.dataset.theme)));
$('refresh-all').addEventListener('click', async () => {
  if (accessDenied || $('management-content').hidden) return;
  const button = $('refresh-all'); button.disabled = true;
  try { await Promise.all([loadStatus(), loadRules(), loadHealth(), ...(activePage === 'nodes' ? [loadSubscriptions()] : []), ...(activePage === 'logs' ? [loadLogs()] : [])]); }
  finally { button.disabled = false; }
});
document.querySelectorAll('[data-pick-wallpaper]').forEach(button => button.addEventListener('click', () => $('wallpaper-file').click()));
$('wallpaper-reset').addEventListener('click', () => {
  if (wallpaperURL) URL.revokeObjectURL(wallpaperURL);
  wallpaperURL = undefined;
  document.body.style.removeProperty('--wallpaper');
  document.body.classList.remove('custom-wallpaper');
  $('wallpaper-file').value = '';
  toast('已恢复默认背景');
});
$('wallpaper-pick').addEventListener('click', () => $('wallpaper-file').click());
let wallpaperURL;
$('wallpaper-file').addEventListener('change', () => {
  const file = $('wallpaper-file').files[0];
  if (!file) return;
  if (!file.type.startsWith('image/') || file.size > 10 * 1024 * 1024) { toast('请选择 10 MB 以内的图片', true); return; }
  if (wallpaperURL) URL.revokeObjectURL(wallpaperURL);
  wallpaperURL = URL.createObjectURL(file);
  document.body.style.setProperty('--wallpaper', 'url("' + wallpaperURL + '")');
  document.body.classList.add('custom-wallpaper');
});

async function startManagement() {
  try {
    const status = await api('/api/status');
    if (accessDenied) return;
    $('auth-notice').hidden = true;
    $('management-content').hidden = false;
    $('management-dock').hidden = false;
    $('access-detail').textContent = '仅允许配置的内网来源访问。网页和 Telegram 使用同一套管理功能。';
    showPage('home');
    renderStatus(status); loadRules(); loadHealth();
    const statusTimer = setInterval(() => {
      if (accessDenied) { clearInterval(statusTimer); return; }
      loadStatus();
    }, 15000);
    const healthTimer = setInterval(() => {
      if (accessDenied) { clearInterval(healthTimer); return; }
      loadHealth();
    }, 30000);
  } catch (err) {
    denyAccess(err.message);
  }
}
startManagement();
