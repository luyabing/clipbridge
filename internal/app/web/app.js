(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const state = {
    data: null,
    settingsDirty: false,
    loaded: false,
    quitting: false,
    pollTimer: null,
    toastTimer: null,
  };

  const MUT_HEADER = { 'X-ClipBridge': '1' };

  async function api(path, opts = {}) {
    const headers = Object.assign({}, opts.headers || {});
    let body = opts.body;
    if (opts.method && opts.method !== 'GET') {
      headers['X-ClipBridge'] = MUT_HEADER['X-ClipBridge'];
    }
    if (body !== undefined && typeof body !== 'string') {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(body);
    }
    const res = await fetch(path, {
      ...opts,
      body,
      headers,
      cache: 'no-store',
    });
    let data = null;
    try { data = await res.json(); } catch (_) { /* empty */ }
    if (!res.ok) {
      throw new Error((data && data.error) || `HTTP ${res.status}`);
    }
    return data;
  }

  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = String(text);
    return n;
  }

  function setText(id, value) {
    const n = $(id);
    if (n) n.textContent = value === undefined || value === null ? '' : String(value);
  }

  function setPill(text, cls) {
    const n = $('statePill');
    n.textContent = text;
    n.className = 'pill' + (cls ? ' ' + cls : '');
  }

  function toast(message, isError = false) {
    const t = $('toast');
    t.textContent = message;
    t.className = 'toast show' + (isError ? ' err' : '');
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(() => {
      t.className = 'toast' + (isError ? ' err' : '');
    }, 2600);
  }

  function formatTime(ms) {
    if (!ms) return '';
    const d = new Date(ms);
    return d.toLocaleTimeString('zh-CN', { hour12: false });
  }

  function formatDateTime(ms) {
    if (!ms) return '';
    return new Date(ms).toLocaleString('zh-CN', { hour12: false });
  }

  function formatBytes(n) {
    if (!n || n < 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let v = n;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
  }

  function initialOf(name) {
    const chars = Array.from(name || '?');
    return chars.length ? chars[0].toUpperCase() : '?';
  }

  function render(data) {
    state.data = data;

    setText('version', 'v' + data.version);
    setText('footVer', 'v' + data.version);
    setText('deviceName', data.deviceName);
    setText('osName', data.os + (data.headless ? ' · 无托盘模式' : ''));
    setText('ipList', (data.localIPs && data.localIPs.length) ? data.localIPs.join(' · ') : '未获取到局域网 IP');
    setText('tcpPort', data.ports.tcp);

    const connected = data.connected || [];
    setText('connCount', connected.length);
    setText('sentCount', data.stats.sent);
    setText('recvCount', data.stats.received);

    if (data.paused) {
      setPill('已暂停', 'warn');
    } else if (connected.length > 0) {
      setPill(`已连接 ${connected.length} 台`, 'ok');
    } else {
      setPill('等待连接', '');
    }
    $('pauseBtn').textContent = data.paused ? '恢复同步' : '暂停同步';

    const secureTag = $('secureTag');
    secureTag.textContent = data.secure ? '已加密' : '未加密';
    secureTag.className = 'tag ' + (data.secure ? 'secure' : 'warn');

    renderTransfer(data.transfer);
    renderConnected(connected);
    renderNearby(data.nearby || []);
    renderManual(data.manualPeers || []);
    renderEvents(data.events || []);
    renderPaths(data);

    if (!state.settingsDirty) {
      fillSettings(data.config, data.autostart);
    }
    const warn = $('codeWarn');
    warn.style.display = (data.config && data.config.pairCode) ? 'none' : '';
    if (data.autostartError) {
      $('saveMsg').textContent = '开机自启设置失败：' + data.autostartError;
      $('saveMsg').className = 'save-msg err';
    }
  }

  function fillSettings(cfg, autostart) {
    if (!cfg) return;
    $('cfgName').value = cfg.deviceName || '';
    $('cfgCode').value = cfg.pairCode || '';
    $('cfgAuto').checked = !!cfg.autoConnect;
    $('cfgSkip').checked = !!cfg.skipSensitive;
    $('cfgText').checked = !!cfg.syncText;
    $('cfgImage').checked = !!cfg.syncImage;
    $('cfgFiles').checked = !!cfg.syncFiles;
    $('cfgAutostart').checked = !!autostart;
    state.loaded = true;
  }

  function renderTransfer(t) {
    const box = $('transfer');
    if (!t || !t.active) {
      box.classList.add('is-hidden');
      return;
    }
    box.classList.remove('is-hidden');
    const dir = t.direction === 'in' ? '接收' : '发送';
    const who = t.peer ? ` · ${t.peer}` : '';
    $('transferLabel').textContent = `${dir} ${t.files} 个文件${who} · ${formatBytes(t.done)} / ${formatBytes(t.total)}`;
    const pct = t.total > 0 ? Math.min(100, Math.floor((t.done / t.total) * 100)) : 0;
    $('transferPct').textContent = pct + '%';
    $('transferBar').style.width = pct + '%';
  }

  function peerCard(p, opts) {
    const card = el('div', 'peer' + (p.lastError ? ' bad' : ''));
    card.appendChild(el('div', 'avatar', initialOf(p.name)));
    const body = el('div', 'body');
    body.appendChild(el('div', 'name', p.name || '未命名设备'));
    const bits = [p.os || '未知系统'];
    if (p.address) bits.push(p.address);
    if (p.secure) bits.push('已加密');
    body.appendChild(el('div', 'sub', bits.join(' · ')));
    if (p.lastError) body.appendChild(el('div', 'err', p.lastError));
    card.appendChild(body);

    const actions = el('div', 'actions');
    if (opts.nearby) {
      if (p.lastError) {
        actions.appendChild(el('span', 'state off', '暂不可连'));
      } else {
        const btn = el('button', 'mini', '连接');
        btn.type = 'button';
        btn.addEventListener('click', () => connectPeer(p.address));
        actions.appendChild(btn);
      }
    } else {
      actions.appendChild(el('span', 'state', '已连接'));
    }
    card.appendChild(actions);
    return card;
  }

  function renderConnected(list) {
    const box = $('connectedList');
    box.replaceChildren();
    if (!list.length) {
      box.appendChild(el('p', 'empty', '暂无连接，正在自动寻找…'));
      return;
    }
    for (const p of list) box.appendChild(peerCard(p, { nearby: false }));
  }

  function renderNearby(list) {
    const box = $('nearbyList');
    setText('nearbyCount', list.length);
    box.replaceChildren();
    if (!list.length) {
      box.appendChild(el('p', 'empty', '正在寻找局域网设备…'));
      return;
    }
    for (const p of list) box.appendChild(peerCard(p, { nearby: true }));
  }

  function renderManual(list) {
    const box = $('manualList');
    box.replaceChildren();
    for (const addr of list) {
      const chip = el('span', 'chip');
      chip.appendChild(el('span', '', addr));
      const rm = el('button', '', '×');
      rm.type = 'button';
      rm.title = '移除这个地址';
      rm.addEventListener('click', () => removePeer(addr));
      chip.appendChild(rm);
      box.appendChild(chip);
    }
  }

  function renderPaths(data) {
    setText('downloadPath', data.downloadDir || '');
    $('downloadPath').title = data.downloadDir || '';
  }

  const ICONS = {
    text: '≡',
    image: '▧',
    files: '▤',
    conn: '⇄',
    config: '⚙',
    error: '!',
  };

  function renderEvents(events) {
    const box = $('events');
    box.replaceChildren();
    if (!events.length) {
      box.appendChild(el('li', 'empty', '还没有活动。复制一些文字或图片试试。'));
      return;
    }
    for (const ev of events) {
      const li = el('li', 'event ' + (ev.direction || 'sys') + (ev.kind === 'error' ? ' err' : ''));
      li.appendChild(el('span', 'ev-icon', ICONS[ev.kind] || '·'));
      const time = el('span', 'ev-time', formatTime(ev.at));
      time.title = formatDateTime(ev.at);
      li.appendChild(time);
      li.appendChild(el('span', 'ev-text', ev.message));
      box.appendChild(li);
    }
  }

  async function saveSettings() {
    if (!state.data) return;
    const btn = $('saveBtn');
    btn.disabled = true;
    const msg = $('saveMsg');
    msg.className = 'save-msg';
    msg.textContent = '';
    const oldCode = state.data.config ? state.data.config.pairCode : '';
    const payload = {
      deviceName: $('cfgName').value,
      pairCode: $('cfgCode').value,
      autoConnect: $('cfgAuto').checked,
      skipSensitive: $('cfgSkip').checked,
      syncText: $('cfgText').checked,
      syncImage: $('cfgImage').checked,
      syncFiles: $('cfgFiles').checked,
      autostart: $('cfgAutostart').checked,
    };
    try {
      const data = await api('/api/config', { method: 'POST', body: payload });
      state.settingsDirty = false;
      render(data);
      msg.textContent = '设置已保存。';
      toast(oldCode !== (data.config && data.config.pairCode) ? '设置已保存，配对码已更新，正在重新连接' : '设置已保存');
    } catch (err) {
      msg.className = 'save-msg err';
      msg.textContent = err.message;
      toast(err.message, true);
    } finally {
      btn.disabled = false;
    }
  }

  async function togglePause() {
    if (!state.data || state.quitting) return;
    try {
      const data = await api('/api/pause', { method: 'POST', body: { paused: !state.data.paused } });
      render(data);
      toast(data.paused ? '已暂停同步' : '已恢复同步');
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function connectPeer(addr) {
    if (!addr) return;
    try {
      const data = await api('/api/peer', { method: 'POST', body: { addr } });
      render(data);
      toast('正在连接 ' + addr);
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function removePeer(addr) {
    try {
      const data = await api('/api/peer', { method: 'DELETE', body: { addr } });
      render(data);
      toast('已移除 ' + addr);
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function openTarget(target) {
    try {
      await api('/api/open', { method: 'POST', body: { target } });
    } catch (err) {
      toast(err.message, true);
    }
  }

  async function quitApp() {
    if (state.quitting) return;
    if (!confirm('确定要退出剪贴桥吗？退出后将不再同步剪贴板。')) return;
    state.quitting = true;
    try {
      await api('/api/quit', { method: 'POST', body: {} });
      clearTimeout(state.pollTimer);
      setPill('正在退出…', 'warn');
      toast('剪贴桥正在退出');
    } catch (err) {
      state.quitting = false;
      toast(err.message, true);
    }
  }

  async function copyPairCode() {
    const input = $('cfgCode');
    const value = input.value;
    if (!value) {
      toast('配对码为空', true);
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      toast('配对码已复制，请粘贴到另一台电脑');
    } catch (_) {
      input.type = 'text';
      input.select();
      try { document.execCommand('copy'); toast('配对码已复制'); }
      catch (err) { toast('复制失败，请手动选择复制', true); }
    }
  }

  function markDirty() {
    state.settingsDirty = true;
    const warn = $('codeWarn');
    warn.style.display = $('cfgCode').value ? 'none' : '';
  }

  async function poll() {
    if (state.quitting) return;
    try {
      const data = await api('/api/status');
      render(data);
    } catch (err) {
      setPill('控制面板已断开', 'bad');
    } finally {
      clearTimeout(state.pollTimer);
      state.pollTimer = setTimeout(poll, 1500);
    }
  }

  function bind() {
    $('settingsForm').addEventListener('input', markDirty);
    $('settingsForm').addEventListener('change', markDirty);
    $('saveBtn').addEventListener('click', saveSettings);
    $('pauseBtn').addEventListener('click', togglePause);
    $('quitBtn').addEventListener('click', quitApp);

    $('peerForm').addEventListener('submit', (ev) => {
      ev.preventDefault();
      const addr = $('peerAddr').value.trim();
      if (!addr) {
        toast('请填写 IP 地址', true);
        return;
      }
      connectPeer(addr).then(() => { $('peerAddr').value = ''; });
    });

    $('showCode').addEventListener('click', () => {
      const input = $('cfgCode');
      const show = input.type === 'password';
      input.type = show ? 'text' : 'password';
      $('showCode').textContent = show ? '隐藏' : '显示';
    });
    $('copyCode').addEventListener('click', copyPairCode);

    document.querySelectorAll('[data-open]').forEach((b) => {
      b.addEventListener('click', () => openTarget(b.dataset.open));
    });
  }

  bind();
  poll();
})();
