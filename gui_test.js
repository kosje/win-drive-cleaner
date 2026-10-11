const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function bridge() {
  const html = fs.readFileSync(__dirname + '/gui.html', 'utf8');
  const source = html.slice(html.indexOf('var WAITERS ='), html.indexOf('/* ---------------- 导航'));
  const nodes = {};
  const context = vm.createContext({ window: {}, api: {}, renderCleanProgress() {},
    el(id) { return nodes[id] || (nodes[id] = { textContent:'',disabled:false,addEventListener(){} }); }, toast() {} });
  vm.runInContext(source, context);
  return context;
}

test('scan progress renders paths as text and ignores late updates', () => {
  const ctx = bridge();
  ctx.expect('media');
  ctx.window.__wdcResult('scanProgress', { ok: true, data: { task:'media', visited:12, errors:1, path:'<script>alert(1)</script>' } });
  assert.match(ctx.el('mediaProgress').textContent, /已检查 12 项/);
  assert.match(ctx.el('mediaProgress').textContent, /<script>/);
  delete ctx.WAITERS.media;
  const text = ctx.el('mediaProgress').textContent;
  ctx.renderScanProgress({task:'media',visited:999,path:'late'});
  assert.equal(ctx.el('mediaProgress').textContent,text);
});

test('cancelled scan reports cancellation and frees its waiter', async () => {
  const ctx = bridge();
  const pending = ctx.expect('cache');
  ctx.window.__wdcResult('cache', {ok:false,error:'扫描已取消',cancelled:true});
  await assert.rejects(pending, error => error.cancelled === true && ctx.scanFailure('cache',error) === '扫描已取消');
  assert.equal(ctx.WAITERS.cache,undefined);
  ctx.setScanStatus('cache',false);
  assert.equal(ctx.el('btnCacheStop').disabled,true);
});

test('backend rejection settles the task and allows retry', async () => {
  const ctx = bridge();
  await assert.rejects(ctx.runTask('media', () => Promise.reject(new Error('bridge failed'))), /bridge failed/);
  assert.equal(ctx.WAITERS.media, undefined);
  const result = ctx.runTask('media', () => {
    ctx.window.__wdcResult('media', { ok: true, data: 'retry worked' });
  });
  assert.equal(await result, 'retry worked');
});

test('synchronous backend errors reject the task', async () => {
  const ctx = bridge();
  await assert.rejects(ctx.runTask('readLog', () => { throw new Error('sync failure'); }), /sync failure/);
  assert.equal(ctx.WAITERS.readLog, undefined);
});

test('a duplicate task does not overwrite the original waiter', async () => {
  const ctx = bridge();
  const pending = ctx.runTask('readLog', () => Promise.resolve({ started: true }));
  let invoked = false;
  await assert.rejects(ctx.runTask('readLog', () => { invoked = true; }), /正在执行/);
  assert.equal(invoked, false);
  ctx.window.__wdcResult('readLog', { ok: true, data: 'original result' });
  assert.equal(await pending, 'original result');
});

test('an immediate result arrives after its waiter is registered', async () => {
  const ctx = bridge();
  const pending = ctx.runTask('bigfiles', () => {
    ctx.window.__wdcResult('bigfiles', { ok: true, data: 42 });
  });
  assert.equal(await pending, 42);
});

test('the full embedded GUI script parses', () => {
  const html = fs.readFileSync(__dirname + '/gui.html', 'utf8');
  const source = html.slice(html.indexOf('<script>') + 8, html.indexOf('</script>'));
  new vm.Script(source);
});

test('top bar and disk page share one in-flight disk query', async () => {
  const ctx = bridge();
  let calls = 0;
  ctx.api.apiDiskInfo = () => { calls++; return { started: true }; };
  const top = ctx.readDiskInfo();
  const disk = ctx.readDiskInfo();
  assert.equal(top, disk);
  await Promise.resolve();
  assert.equal(calls, 1);
  ctx.window.__wdcResult('diskInfo', { ok: true, data: { free: 123, systemDrive: 'D:\\' } });
  assert.equal((await top).free, 123);
  assert.equal((await disk).systemDrive, 'D:\\');
  const refresh = ctx.readDiskInfo();
  await Promise.resolve();
  assert.equal(calls, 2);
  ctx.window.__wdcResult('diskInfo', { ok: true, data: { free: 456 } });
  assert.equal((await refresh).free, 456);
});

test('a failed shared disk query releases the pending query for retry', async () => {
  const ctx = bridge();
  ctx.api.apiDiskInfo = () => Promise.reject(new Error('disk query failed'));
  await assert.rejects(ctx.readDiskInfo(), /disk query failed/);
  assert.equal(ctx.diskInfoPending, null);
  ctx.api.apiDiskInfo = () => ctx.window.__wdcResult('diskInfo', { ok: true, data: 42 });
  assert.equal(await ctx.readDiskInfo(), 42);
});

function filePanels() {
  const html = fs.readFileSync(__dirname + '/gui.html', 'utf8');
  const source = html.slice(html.indexOf('/* ---------------- 大文件'), html.indexOf('/* ---------------- 通讯缓存'));
  const nodes = {};
  const selections = {
    big: [{ checked: true, disabled: false, getAttribute() { return 'C:\\Data\\video.mp4'; } }],
    cache: [{ checked: true, disabled: false, getAttribute() { return 'C:\\Data\\Cache'; } }],
  };
  const pending = [];
  const indicators = {};
  function el(id) {
    if (!nodes[id]) {
      const node = nodes[id] = { disabled: false, handlers: {}, textContent: '' };
      node.addEventListener = (name, callback) => { node.handlers[name] = callback; };
      node.click = () => { if (!node.disabled) node.handlers.click(); };
      Object.defineProperty(node, 'innerHTML', {
        set(value) { node.html = value; if (/^(big|cache)Body$/.test(id)) selections[id.replace('Body', '')] = []; },
        get() { return node.html; },
      });
    }
    return nodes[id];
  }
  const context = vm.createContext({
    el, api: {}, fmt: String, toast() {}, confirm() { return true; },
    busy(panel, on) { indicators[panel] = on; },
    emptyRow(body) { body.innerHTML = ''; },
    setScanStatus() {},
    scanFailure(panel,error) { return error.message; },
    document: {
      querySelectorAll(selector) {
        const panel = selector.match(/#(big|cache)Body/)[1];
        return selections[panel].filter(cb => !selector.includes(':checked') || cb.checked);
      },
    },
    runTask(task) { return new Promise((resolve, reject) => { pending.push({ task, resolve, reject }); }); },
  });
  vm.runInContext(source, context);
  return { context, nodes, pending, indicators, selections };
}

const tick = () => new Promise(resolve => setImmediate(resolve));

for (const [panel, prefix, deletionTask, scanTask] of [
  ['big', 'Big', 'bigfilesDelete', 'bigfiles'],
  ['cache', 'Cache', 'cacheDelete', 'cache'],
]) {
  test(`${panel}: deletion blocks rescanning and keeps refresh marked busy`, async () => {
    const ui = filePanels();
    ui.nodes['btn' + prefix + 'Del'].click();
    assert.equal(ui.pending[0].task, deletionTask);
    assert.equal(ui.nodes['btn' + prefix].disabled, true);
    assert.equal(ui.selections[panel][0].disabled, true);
    ui.nodes['btn' + prefix].click();
    assert.equal(ui.pending.length, 1, 'rescan must not start while deleting');
    ui.pending[0].resolve({ deleted: 1, bytes: 100, errors: 0 });
    await tick();
    assert.equal(ui.pending[1].task, scanTask);
    assert.equal(ui.indicators[panel + 'Busy'], true, 'refresh must keep the spinner visible');
    assert.equal(ui.nodes['btn' + prefix].disabled, true);
    ui.pending[1].resolve({ items: [], total: 0 });
    await tick();
    assert.equal(ui.nodes['btn' + prefix].disabled, false);
    assert.equal(ui.nodes['btn' + prefix + 'Del'].disabled, true);
    assert.equal(ui.indicators[panel + 'Busy'], false);
  });

  test(`${panel}: failed deletion restores selection and allows retry`, async () => {
    const ui = filePanels();
    ui.nodes['btn' + prefix + 'Del'].click();
    ui.pending[0].reject(new Error('delete failed'));
    await tick();
    assert.equal(ui.pending.length, 1, 'failed deletion must not trigger a refresh');
    assert.equal(ui.nodes['btn' + prefix].disabled, false);
    assert.equal(ui.nodes['btn' + prefix + 'Del'].disabled, false);
    assert.equal(ui.selections[panel][0].disabled, false);
    assert.equal(ui.selections[panel][0].checked, true);
    assert.equal(ui.indicators[panel + 'Busy'], false);
  });
}
