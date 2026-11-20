const { test, expect } = require('@playwright/test');

// Release fixture jobs even after an assertion failure, so later tests are isolated.
test.afterEach(async ({ request }) => {
  await request.post('/test/complete');
  await request.post('/test/hold?enabled=false');
});

// Exercise the real CSP, HTMX and browser selection/focus APIs, not a DOM mock.
test('polling preserves selected text and expanded diffs', async ({ page }) => {
  await page.goto('/');
  const diff = page.locator('details.diff').first();
  await expect(diff).toHaveAttribute('open', '');
  await diff.locator('.changes code').first().evaluate(node => {
    window.originalRef = node;
    const range = document.createRange(); range.selectNodeContents(node);
    window.getSelection().removeAllRanges(); window.getSelection().addRange(range);
  });
  await page.waitForResponse(response => response.url().endsWith('/fragments/repos'));
  await expect.poll(() => page.evaluate(() => window.getSelection().toString())).toBe('refs/heads/main');
  expect(await page.evaluate(() => window.originalRef.isConnected)).toBe(true);
  await page.evaluate(() => window.getSelection().removeAllRanges());
  await diff.locator(':scope > summary').click();
  await page.waitForResponse(response => response.url().endsWith('/fragments/repos'));
  await expect(diff).not.toHaveAttribute('open');
});

test('sync updates current diff and retains the applied plan without reloading', async ({ page }) => {
  await page.goto('/');
  await page.evaluate(() => {window.pageIdentity = 'retained';});
  await page.locator('.repo-actions button', { hasText: /^Sync$/ }).click();
  await expect(page.locator('.applied > summary')).toContainText('Last applied plan', {timeout: 12000});
  await expect(page.locator('.repo-head')).toContainText('in sync');
  await expect(page.locator('details.diff').first().locator('.changes code')).toHaveCount(0);
  await expect(page.locator('.repo-actions button', { hasText: /^Sync$/ })).toBeFocused();
  await page.locator('.applied > summary').click();
  await expect(page.locator('.applied .changes').first()).toContainText('refs/heads/main');
  expect(await page.evaluate(() => window.pageIdentity)).toBe('retained');
  await page.waitForResponse(response => response.url().endsWith('/fragments/repos'));
  await expect(page.locator('.applied')).toHaveAttribute('open', '');
});

test('deferred updates coalesce, show notifications independently and reject older responses', async ({ page }) => {
  await page.goto('/');
  await page.locator('.repo-name').evaluate(node => {
    const range = document.createRange(); range.selectNodeContents(node);
    window.getSelection().removeAllRanges(); window.getSelection().addRange(range);
    const target = document.getElementById('repos');
    const respond = (seq, html) => document.dispatchEvent(new CustomEvent('htmx:beforeSwap', {
      detail: { target, shouldSwap: true, serverResponse: html, xhr: { forgemirrorSequence: seq } }
    }));
    document.dispatchEvent(new CustomEvent('forgemirror:notice', {detail:{message:'Sync accepted',kind:'success'}}));
    respond(10000, '<p>older snapshot</p>');
    respond(10001, '<p data-live-key="latest">latest snapshot</p>');
  });
  await expect(page.locator('.repo-name')).toHaveText('acme/app');
  expect(await page.evaluate(() => window.getSelection().toString())).toBe('acme/app');
  await expect(page.locator('#notifications')).toContainText('Sync accepted');
  await page.evaluate(() => window.getSelection().removeAllRanges());
  await expect(page.locator('#repos')).toContainText('latest snapshot');
  await expect(page.locator('#repos')).not.toContainText('Sync accepted');
  await page.evaluate(() => document.dispatchEvent(new CustomEvent('htmx:beforeSwap', {
    detail: { target: document.getElementById('repos'), shouldSwap: true,
      serverResponse: '<p>obsolete response</p>', xhr: { forgemirrorSequence: 9999 } }
  })));
  await expect(page.locator('#repos')).not.toContainText('obsolete response');
});

test('incremental log updates retain scroll position', async ({ page }) => {
  await page.goto('/');
  const top = await page.evaluate(() => {
    const target = document.getElementById('logs');
    const respond = (seq, text) => document.dispatchEvent(new CustomEvent('htmx:beforeSwap', {
      detail: { target, shouldSwap: true, serverResponse: '<pre>' + text + '</pre>', xhr: { forgemirrorSequence: seq } }
    }));
    const lines = 'log line\n'.repeat(100);
    respond(10000, lines);
    target.scrollTop = 150;
    const before = target.scrollTop;
    respond(10001, lines + 'new line');
    return { before, after: target.scrollTop };
  });
  expect(top.before).toBeGreaterThan(0);
  expect(top.after).toBe(top.before);
});

test('wide layouts use available width and targets occupy full rows', async ({ page }) => {
  await page.setViewportSize({width:1920,height:1080});
  await page.goto('/');
  const dimensions = await page.evaluate(() => {
    const main = document.querySelector('main').getBoundingClientRect();
    const targets = [...document.querySelectorAll('.target')].map(e => e.getBoundingClientRect());
    return {main:main.width, target:targets[0].width, top0:targets[0].top,top1:targets[1].top};
  });
  expect(dimensions.main).toBeGreaterThan(1800);
  expect(dimensions.target).toBeGreaterThan(1700);
  expect(dimensions.top1).toBeGreaterThan(dimensions.top0);
  await page.setViewportSize({width:375,height:812});
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375);
});

test('refresh feedback survives reconciliation and continues after acknowledgement', async ({ page, request }) => {
  await request.post('/test/hold');
  await request.post('/test/seed?repo=acme/other');
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/actions/refresh', async route => { await gate; await route.continue(); });
  await page.goto('/');
  await page.evaluate(() => document.addEventListener('htmx:beforeRequest', event => {
    if (event.detail.elt.matches('form.run-action')) { window.actionSequence = event.detail.xhr.forgemirrorSequence; }
  }));
  const repository = page.locator('[data-repo="acme/app"]');
  const form = repository.locator('form[data-live-key="refresh"]');
  const acknowledgement = page.waitForResponse(response => response.url().endsWith('/actions/refresh'));
  try {
    await form.getByRole('button', {name:'Refresh', exact:true}).click();
    await expect(form.locator('.request-spinner')).toBeVisible();
    await expect(form).toHaveAttribute('aria-busy', 'true');
    const html = await (await request.get('/fragments/repos')).text();
    await page.evaluate(html => document.dispatchEvent(new CustomEvent('htmx:beforeSwap', {
      detail: {target:document.getElementById('repos'),shouldSwap:true,serverResponse:html,
        xhr:{forgemirrorSequence:window.actionSequence}}
    })), html);
    await expect(form.locator('.request-spinner')).toBeVisible();
    await expect(form).toHaveAttribute('aria-busy', 'true');
  } finally { release(); await acknowledgement; }
  await expect(repository.locator('[data-repository-activity]')).toHaveText('Refreshing');
  await expect(page.locator('[data-repo="acme/other"] [data-repository-activity]')).toHaveCount(0);
  await expect(page.locator('[data-global-activity]')).toHaveText('Refreshing 1 repository');
  await expect(form.locator('.request-spinner')).toBeHidden();
  await expect(form).toHaveAttribute('aria-busy', 'false');
  await request.post('/test/complete?repo=acme/app');
  await expect(repository.locator('[data-repository-activity]')).toHaveCount(0, {timeout:12000});
  await expect(page.locator('[data-global-activity]')).toHaveCount(0);
});

test('background sync remains visible without rotation for reduced motion', async ({ page, request }) => {
  await request.post('/test/hold');
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.goto('/');
  const repository = page.locator('[data-repo="acme/app"]');
  await repository.locator('form[data-live-key="sync"] button').click();
  const activity = repository.locator('[data-repository-activity]');
  await expect(activity).toHaveText('Syncing');
  await expect(page.locator('[data-global-activity]')).toHaveText('Syncing 1 repository');
  await expect(activity.locator('.spinner')).toBeVisible();
  expect(await activity.locator('.spinner').evaluate(node => getComputedStyle(node).animationName)).toBe('none');
  await request.post('/test/complete?repo=acme/app');
  await expect(activity).toHaveCount(0, {timeout:12000});
});

test('background polling does not activate action indicators', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/fragments/repos', async route => { await gate; await route.continue(); });
  await page.goto('/');
  try {
    await expect(page.locator('#repos')).toHaveClass(/htmx-request/, {timeout:10000});
    await expect(page.locator('form.run-action .request-spinner:visible')).toHaveCount(0);
  } finally { release(); }
});

test('failed action requests clear the immediate indicator', async ({ page }) => {
  await page.route('**/actions/refresh', route => route.fulfill({status:500,body:'request failed'}));
  await page.goto('/');
  const form = page.locator('[data-repo="acme/app"] form[data-live-key="refresh"]');
  const response = page.waitForResponse(response => response.url().endsWith('/actions/refresh'));
  await form.getByRole('button', {name:'Refresh', exact:true}).click();
  await response;
  await expect(form.locator('.request-spinner')).toBeHidden();
  await expect(form).toHaveAttribute('aria-busy', 'false');
  await expect(page.locator('[data-repository-activity]')).toHaveCount(0);
  await expect(page.locator('#notifications .toast[data-kind="error"]')).toBeVisible();
});

test('bulk refresh and sync keep aggregate activity until the last job finishes', async ({ page, request }) => {
  test.setTimeout(60000);
  await request.post('/test/hold');
  await request.post('/test/seed?repo=acme/other');
  await page.goto('/');
  await page.locator('form[data-live-key="refresh-all"] button').click();
  await expect(page.locator('[data-repository-activity]')).toHaveCount(2);
  await expect(page.locator('[data-global-activity]')).toHaveText('Refreshing 2 repositories');
  await request.post('/test/complete?repo=acme/app');
  await expect(page.locator('[data-global-activity]')).toHaveText('Refreshing 1 repository', {timeout:12000});
  await expect(page.locator('[data-repo="acme/app"] [data-repository-activity]')).toHaveCount(0);
  await request.post('/test/complete?repo=acme/other');
  await expect(page.locator('[data-global-activity]')).toHaveCount(0, {timeout:12000});
  page.once('dialog', dialog => dialog.accept());
  await page.locator('form[data-live-key="sync-all"] button').click();
  await expect(page.locator('[data-repository-activity]')).toHaveCount(2);
  await expect(page.locator('[data-global-activity]')).toHaveText('Syncing 2 repositories');
  await request.post('/test/complete?repo=acme/app');
  await expect(page.locator('[data-global-activity]')).toHaveText('Syncing 1 repository', {timeout:12000});
  await request.post('/test/complete?repo=acme/other');
  await expect(page.locator('[data-global-activity]')).toHaveCount(0, {timeout:12000});
});

test('single and bulk refresh/sync acknowledge in a top toast, never inline', async ({page,request}) => {
  for (const action of [
    {key:'refresh',bulk:false,message:'Refresh started for acme/app.'},
    {key:'sync',bulk:false,message:'Sync started for acme/app.'},
    {key:'refresh-all',bulk:true,message:'Refresh started for all repositories (2 queued).'},
    {key:'sync-all',bulk:true,message:'Sync started for all pending repositories (2 queued).'}
  ]) {
    await request.post('/test/seed?repo=acme/app');
    await request.post('/test/seed?repo=acme/other');
    await page.goto('/');
    if (action.key === 'sync-all') {page.once('dialog', dialog => dialog.accept());}
    const scope = action.bulk ? page : page.locator('[data-repo="acme/app"]');
    await scope.locator('form[data-live-key="'+action.key+'"] button').click();
    await expect(page.locator('#notifications .toast')).toBeVisible();
    await expect(page.locator('#notifications .toast-message')).toHaveText(action.message);
    await expect(page.locator('#repos')).not.toContainText(action.message);
    await expect(page.locator('[data-live-key="action-notice"]')).toHaveCount(0);
    await request.post('/test/complete');
  }
});

test('successful notifications expire without being replayed by polling', async ({page}) => {
  await page.clock.install();
  await page.goto('/');
  await page.locator('[data-repo="acme/app"] form[data-live-key="refresh"] button').click();
  await expect(page.locator('#notifications .toast')).toBeVisible();
  await page.clock.runFor(5200);
  await expect(page.locator('#notifications .toast')).toHaveCount(0);
  await page.clock.runFor(5200);
  await expect(page.locator('#notifications .toast')).toHaveCount(0);
});

test('notification lifetime pauses for hover/focus and manual dismissal restores focus', async ({page}) => {
  await page.clock.install();
  await page.goto('/');
  const refresh = page.locator('[data-repo="acme/app"] form[data-live-key="refresh"] button');
  await refresh.click();
  const toast = page.locator('#notifications .toast');
  await expect(toast).toBeVisible();
  await toast.hover();
  await page.clock.runFor(6000);
  await expect(toast).toBeVisible();
  await toast.getByRole('button',{name:'Dismiss notification'}).focus();
  await page.mouse.move(0,600);
  await page.clock.runFor(6000);
  await expect(toast).toBeVisible();
  await toast.getByRole('button',{name:'Dismiss notification'}).click();
  await expect(toast).toHaveCount(0);
  await expect(refresh).toBeFocused();
});

test('latest notification replaces the previous one and resets its expiry', async ({page}) => {
  await page.clock.install();
  await page.goto('/');
  const repo = page.locator('[data-repo="acme/app"]');
  await repo.locator('form[data-live-key="refresh"] button').click();
  await expect(page.locator('#notifications .toast-message')).toHaveText('Refresh started for acme/app.');
  await page.clock.runFor(3000);
  await repo.locator('form[data-live-key="sync"] button').click();
  await expect(page.locator('#notifications .toast-message')).toHaveText('Sync started for acme/app.');
  await page.clock.runFor(2500);
  await expect(page.locator('#notifications .toast')).toHaveCount(1);
  await page.clock.runFor(2700);
  await expect(page.locator('#notifications .toast')).toHaveCount(0);
});

test('notification content is text, errors persist, and reduced motion avoids entrance animation', async ({page,request}) => {
  const message = '<img src=x onerror="window.notificationInjection=true">';
  const html = await (await request.get('/fragments/repos')).text();
  await page.route('**/actions/refresh', route => route.fulfill({status:200,headers:{
    'HX-Trigger':JSON.stringify({'forgemirror:notice':{message,kind:'error'}})
  },body:html}));
  await page.clock.install();
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.setViewportSize({width:375,height:812});
  await page.goto('/');
  await page.locator('[data-repo="acme/app"] form[data-live-key="refresh"] button').click();
  const toast = page.locator('#notifications .toast');
  await expect(toast).toBeVisible();
  await expect(toast.locator('.toast-message')).toHaveText(message);
  await expect(toast.locator('img')).toHaveCount(0);
  expect(await page.evaluate(() => window.notificationInjection)).toBeUndefined();
  expect(await toast.evaluate(node => getComputedStyle(node).animationName)).toBe('none');
  const box = await toast.boundingBox();
  expect(box.y).toBeLessThan(40);
  expect(box.x).toBeGreaterThanOrEqual(10);
  expect(box.x+box.width).toBeLessThanOrEqual(365);
  await page.clock.runFor(10000);
  await expect(toast).toBeVisible();
  await toast.getByRole('button',{name:'Dismiss notification'}).click();
  await expect(toast).toHaveCount(0);
});
