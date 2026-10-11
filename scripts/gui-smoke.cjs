const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WDC_PLAYWRIGHT_MODULE || 'playwright');

(async () => {
  const executablePath = [
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
    'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
  ].find(p => fs.existsSync(p));
  assert.ok(executablePath, 'Microsoft Edge is required for the GUI smoke test');
  const browser = await chromium.launch({ executablePath, headless: true });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('dialog', dialog => dialog.accept());
    const version = fs.readFileSync(path.join(__dirname, '../main.go'), 'utf8').match(/const version = "([^"]+)"/)[1];
    const cachePath = 'C:\\Test\\AppData\\Local\\Google\\Chrome\\User Data\\Default\\Cache';
    const mediaPath = 'C:\\Test\\视频 文件夹,测试\\我的 录屏,2026.mp4';
    let cleaned = false, mediaScans = 0, opened = '', elevatedOptions = null;
    const startupOriginal = 'C:\\Test\\Startup\\测试启动项.lnk';
    let startupID = startupOriginal, startupChanges=0;
    const deliver = (task, payload) => setTimeout(() => {
      page.evaluate(({ task, payload }) => window.__wdcResult(task, payload), { task, payload })
        .catch(error => errors.push(error.message));
    }, 20);
    const complete = (task, data) => { deliver(task, { ok: true, data }); return { started: true, task }; };
    await page.exposeFunction('apiDiskInfo', () => complete('diskInfo', {
      version, admin: false, free: 1000, total: 2000, compactable: true, systemDrive: 'D:\\',
      logDir:'C:\\Test\\AppData\\Local\\WinDriveCleaner',
    }));
    await page.exposeFunction('apiStartupList', () => complete('startupList',[
      {id:startupID,name:'测试启动项',target:startupID,source:'启动文件夹(当前用户)',enabled:true},
      {id:'readonly',name:'只读项',target:'',source:'RunOnce',enabled:true,readOnly:true},
    ]));
    await page.exposeFunction('apiStartupToggle',(id,on) => {
      assert.equal(id,startupID,'UI reused an obsolete startup path');
      startupChanges++;
      if(startupChanges===3) return complete('startupToggle',{ok:false,error:'模拟同名文件冲突'});
      startupID=on?startupOriginal:startupOriginal+'.wcd_disabled';
      return complete('startupToggle',{ok:true,id:startupID,target:startupID,msg:'已修改'});
    });
    await page.exposeFunction('apiElevate',(deep,dry) => {
      elevatedOptions=[deep,dry];
      return {ok:false,msg:'测试取消提权'};
    });
    await page.exposeFunction('apiReadLog', () => complete('readLog', { content: 'test log', count: 1, file: 'test.txt' }));
    await page.exposeFunction('apiCacheScan', () => complete('cache', {
      items: cleaned ? [] : [{ software: 'Chrome · Cache', path: cachePath, bytes: 20 << 20 }], total: cleaned ? 0 : 20 << 20,
    }));
    await page.exposeFunction('apiCacheDelete', paths => {
      assert.deepEqual(paths, [cachePath]);
      cleaned = true;
      return complete('cacheDelete', { deleted: 1, bytes: 20 << 20, errors: 0 });
    });
    await page.exposeFunction('apiMediaScan', () => {
      mediaScans++;
      if (mediaScans === 1) {
        deliver('scanProgress', { ok: true, data: { task: 'media', visited: 12, errors: 0, path: mediaPath } });
        return { started: true, task: 'media' };
      }
      return complete('media', { items: [{ path: mediaPath, size: 1000 }], total: 1000 });
    });
    await page.exposeFunction('apiCancelScan', task => {
      assert.equal(task, 'media');
      deliver('media', { ok: false, error: '扫描已取消', cancelled: true });
      return { ok: true };
    });
    await page.exposeFunction('apiOpenFolder', target => {
      opened = target;
      return complete('openFolder', null);
    });
    await page.setContent(fs.readFileSync(path.join(__dirname, '../gui.html'), 'utf8'));
    await page.waitForFunction(expected => document.getElementById('versionLabel').textContent === 'v' + expected, version);
    assert.match(await page.locator('#diskBadge').textContent(), /^D:\\/);
    await page.locator('input[name=mode][value=deep]').check();
    await page.locator('#dryRun').check();
    await page.locator('#btnElevate').click();
    await page.waitForFunction(() => !document.getElementById('btnElevate').disabled);
    assert.deepEqual(elevatedOptions,[true,true]);
    assert.equal(await page.locator('#dryRun').isChecked(),true);
    await page.locator('input[name=mode][value=std]').check();
    await page.locator('#dryRun').uncheck();
    await page.locator('#nav button[data-sec=startup]').click();
    await page.locator('#startBody label.sw').first().waitFor();
    const startupToggle=page.locator('#startBody input').first();
    await page.locator('#startBody label.sw').first().click();
    await page.waitForFunction(() => document.querySelector('#startBody input').getAttribute('data-id').endsWith('.wcd_disabled'));
    await page.locator('#startBody label.sw').first().click();
    await page.waitForFunction(() => !document.querySelector('#startBody input').getAttribute('data-id').endsWith('.wcd_disabled'));
    assert.equal(startupID,startupOriginal);
    assert.equal(await startupToggle.isChecked(),true);
    await page.locator('#startBody label.sw').first().click();
    await page.waitForFunction(() => !document.querySelector('#startBody input').disabled&&document.querySelector('#startBody input').checked);
    assert.equal(startupID,startupOriginal);
    assert.equal(await page.locator('#startBody input').nth(1).isDisabled(),true);
    await page.locator('#nav button[data-sec=cache]').click();
    await page.locator('#btnCache').click();
    await page.locator('#cacheBody input').waitFor();
    await page.locator('#cacheBody input').check();
    await page.locator('#btnCacheDel').click();
    await page.waitForFunction(() => document.getElementById('cacheBody').textContent.includes('未发现'));
    assert.equal(cleaned, true);
    await page.locator('#nav button[data-sec=media]').click();
    await page.locator('#btnMedia').click();
    await page.waitForFunction(() => document.getElementById('mediaProgress').textContent.includes('12'));
    await page.locator('#btnMediaStop').click();
    await page.waitForFunction(() => document.getElementById('mediaProgress').textContent === '扫描已取消');
    await page.locator('#btnMedia').click();
    await page.locator('#mediaBody button').waitFor();
    assert.match(await page.locator('#mediaProgress').textContent(), /^扫描完成/);
    await page.locator('#mediaBody button').click();
    await page.waitForFunction(() => !window.WAITERS.openFolder);
    assert.equal(opened, mediaPath);
    assert.deepEqual(errors, []);
    await page.screenshot({ path: path.join(__dirname, '../dist/gui-smoke.png'), fullPage: true });
    console.log('GUI smoke passed: elevation options, startup disable/restore, version, disk, cache deletion/refresh, media cancellation/retry and Unicode opening');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
