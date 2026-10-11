const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function bridge() {
  const html = fs.readFileSync(__dirname + '/gui.html', 'utf8');
  const source = html.slice(html.indexOf('var WAITERS ='), html.indexOf('/* ---------------- 导航'));
  const context = vm.createContext({ window: {}, renderCleanProgress() {} });
  vm.runInContext(source, context);
  return context;
}

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
