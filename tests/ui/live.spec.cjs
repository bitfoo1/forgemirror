const { test, expect } = require('@playwright/test');

// Release fixture jobs even after an assertion failure, so later tests are isolated.
test.afterEach(async ({ request }) => {
  await request.post('/test/complete');
  await request.post('/test/hold?enabled=false');
});

test('forge badges stay beside the repository and shared changes appear once', async ({ page, request }) => {
  await request.post('/test/seed?repo=acme/app');
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/');
    const repo = page.locator('.repo').filter({ has: page.locator('.repo-name', { hasText: /^acme\/app$/ }) });
    const badges = repo.locator('.repo-head .forge-badge');
    await expect(badges).toHaveText([/^GitHub: /, /^Codeberg: /, /^GitLab: /]);
    await expect(badges.first()).toHaveAttribute('title', /GitHub: out of sync/);
    // Status is announced as text; the badge itself is not a tab stop.
    await expect(badges.first().locator('.visually-hidden')).toHaveText(/: out of sync/);
    await expect(badges.first()).not.toHaveAttribute('tabindex');
    const changes = repo.locator('.grouped-changes li');
    await expect(changes).toHaveCount(1);
    await expect(changes.locator('.forge-badge')).toHaveText([/^GitHub: /, /^Codeberg: /]);
    const positions = await repo.evaluate(node => {
      const name = node.querySelector('.repo-name').getBoundingClientRect();
      const badge = node.querySelector('.repo-head .forge-badge').getBoundingClientRect();
      const row = node.querySelector('.grouped-changes li');
      const code = row.querySelector('code:last-of-type').getBoundingClientRect();
      const target = row.querySelector('.forge-badge').getBoundingClientRect();
      return { headerLine: Math.abs(name.top - badge.top), badgeGap: target.left - code.right };
    });
    expect(positions.headerLine).toBeLessThan(5);
    if (width > 400) expect(positions.badgeGap).toBeLessThan(15);
    await repo.locator('.target-details > summary').click();
    await expect(repo.locator('[data-publication-state="review_required"]').first()).toContainText('Sync uses the latest state.');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
});

test('official forge icons work offline in both themes and keep header badges inline', async ({ page }) => {
  await page.route('https://**/*', route => route.abort());
  await page.goto('/');
  const repo = page.locator('[data-repo="acme/app"]');
  for (const width of [1440, 390]) {
    await page.setViewportSize({width, height: 900});
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      const badges = repo.locator('.repo-head .forge-badge');
      await expect(badges).toHaveText([/^GitHub: /, /^Codeberg: /, /^GitLab: /]);
      await expect(repo.locator('.repo-head [data-forge="codeberg"] img:visible')).toHaveAttribute('src',
        theme === 'dark' ? /^static\/icons\/codeberg-white\.svg\?v=/ : /^static\/icons\/codeberg\.svg\?v=/);
      await expect.poll(() => page.locator('.forge-icon:visible').evaluateAll(images =>
        images.length > 0 && images.every(image => image.complete && image.naturalWidth > 0))).toBe(true);
      const inline = await repo.locator('.repo-head').evaluate(head => {
        const name = head.querySelector('.repo-name').getBoundingClientRect();
        return [...head.querySelectorAll('.forge-badge')].every(badge => Math.abs(badge.getBoundingClientRect().top - name.top) < 5);
      });
      expect(inline).toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    }
  }
  await repo.locator('.target-details > summary').click();
  await expect(repo.locator('.target h3').first()).toHaveText('GitHub');
  await expect(repo.locator('.target h3').first().locator('img')).toHaveAttribute('src', /^static\/icons\/github\.svg\?v=/);
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
  // A retained applied plan can predate this action; completion is the fresh
  // comparison, which may arrive on the next five-second polling cycle.
  await expect(page.locator('.repo-head .forge-badge').first()).toHaveAttribute('title', 'GitHub: in sync', {timeout: 12000});
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
  const current = page.locator('details.diff').first();
  if (!await current.evaluate(details => details.open)) await current.locator(':scope > summary').click();
  await current.locator('.target-details > summary').click();
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
  await page.locator('form[data-live-key="sync-all"] button').click();
  await page.locator('#confirm-dialog [data-confirm-accept]').click();
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
    const scope = action.bulk ? page : page.locator('[data-repo="acme/app"]');
    await scope.locator('form[data-live-key="'+action.key+'"] button').click();
    if (action.key === 'sync-all') await page.locator('#confirm-dialog [data-confirm-accept]').click();
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

test('notifications are announced once by urgency and polled activity is not live', async ({page}) => {
  // Record ariaNotify calls where the browser provides it; remove it in the
  // second pass to exercise the pre-rendered live-region fallback.
  for (const native of [true, false]) {
    await page.addInitScript(native => {
      window.announcements = [];
      if (native) { document.ariaNotify = (text, options) => window.announcements.push([text, options.priority]); }
      else { delete Document.prototype.ariaNotify; delete document.ariaNotify; }
    }, native);
    await page.goto('/');
    await expect(page.locator('#notifications')).toHaveAttribute('role', 'region');
    await expect(page.locator('#notifications')).not.toHaveAttribute('aria-live');
    await expect(page.locator('#repos [role=status], #repos [aria-live]')).toHaveCount(0);
    await page.evaluate(() => document.dispatchEvent(new CustomEvent('forgemirror:notice', {detail:{message:'Sync accepted',kind:'success'}})));
    await page.evaluate(() => document.dispatchEvent(new CustomEvent('forgemirror:notice', {detail:{message:'Sync failed',kind:'error'}})));
    if (native) {
      expect(await page.evaluate(() => window.announcements)).toEqual([['Sync accepted', 'normal'], ['Sync failed', 'high']]);
    } else {
      await expect(page.locator('#announce-polite')).toHaveText('Sync accepted');
      await expect(page.locator('#announce-assertive')).toHaveText('Sync failed');
      await expect(page.locator('#announce-assertive')).toHaveAttribute('role', 'alert');
    }
  }
});

test('cards collapse from heading buttons, persist per browser and survive polling', async ({page}) => {
  await page.goto('/');
  const toggle = card => page.locator(`[data-card-toggle="${card}"]`);
  const body = card => page.locator(`[data-card-body="${card}"]`);
  for (const card of ['repos', 'controls', 'logs', 'config']) {
    await expect(page.locator(`h2 > [data-card-toggle="${card}"]`)).toHaveCount(1);
  }
  await expect(toggle('config')).toHaveAttribute('aria-expanded', 'false');
  await expect(body('config')).toBeHidden();
  await expect(body('repos')).toBeVisible();
  await toggle('repos').click();
  await expect(toggle('repos')).toHaveAttribute('aria-expanded', 'false');
  await expect(body('repos')).toBeHidden();
  // A poll re-renders the repository header with aria-expanded="true".
  await page.waitForResponse(response => response.url().endsWith('/fragments/repos'));
  await expect(toggle('repos')).toHaveAttribute('aria-expanded', 'false');
  await expect(body('repos')).toBeHidden();
  await page.reload();
  await expect(body('repos')).toBeHidden();
  await toggle('repos').press('Enter');
  await expect(body('repos')).toBeVisible();
  await toggle('config').click();
  await expect(body('config')).toBeVisible();
});

test.describe('without JavaScript', () => {
  test.use({javaScriptEnabled: false});
  test('every card stays expanded', async ({page}) => {
    await page.goto('/');
    for (const card of ['repos', 'controls', 'logs', 'config']) {
      await expect(page.locator(`[data-card-body="${card}"]`)).toBeVisible();
    }
  });
});

test('confirmations use a native modal dialog that cancels safely', async ({page, request}) => {
  await request.post('/test/seed?repo=acme/app');
  await request.post('/test/hold');
  let nativeDialog = false;
  page.on('dialog', dialog => { nativeDialog = true; dialog.dismiss(); });
  await page.goto('/');
  const dialog = page.locator('#confirm-dialog');
  const syncAll = page.locator('form[data-live-key="sync-all"] button');
  await syncAll.click();
  await expect(dialog).toBeVisible();
  expect(await dialog.evaluate(node => node.matches(':modal'))).toBe(true);
  await expect(dialog.locator('h2')).toHaveText('Sync all repositories');
  await expect(dialog.locator('[data-confirm-message]')).toHaveText('Sync the out-of-sync repository to the targets?');
  await expect(dialog.getByRole('button', {name:'Cancel'})).toBeFocused();
  await expect(dialog.locator('[data-confirm-accept]')).toHaveText('Sync all');
  // Esc cancels: no request, focus returns to the invoking button.
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(syncAll).toBeFocused();
  await expect(page.locator('[data-repository-activity]')).toHaveCount(0);
  await syncAll.click();
  await dialog.locator('[data-confirm-accept]').click();
  await expect(page.locator('[data-global-activity]')).toHaveText('Syncing 1 repository');
  expect(nativeDialog).toBe(false);
  await request.post('/test/complete');
});

test('starting a run lights its trigger at once, not on the next poll', async ({page, request}) => {
  await request.post('/test/seed?repo=acme/app');
  await request.post('/test/hold');
  await page.goto('/');
  const engine = page.locator('#overview .pl-engine');
  await expect(engine.locator('small')).toHaveText(/^idle/);
  await page.locator('[data-repo="acme/app"] form[data-live-key="refresh"] button').click();
  // The overview polls every 5s; the action's activity event must beat that.
  await expect(page.locator('#overview [data-trigger="manual"]')).toHaveClass('pl-fired', {timeout: 2000});
  await expect(engine).toHaveClass(/pl-busy/);
  await expect(engine.locator('small')).toHaveText('refreshing acme/app · by manual');
  await request.post('/test/complete');
});

test('a push lights the push branch and the targets fan out with their state', async ({page, request}) => {
  await request.post('/test/hold');
  await page.goto('/');
  await request.post('/test/trigger?trigger=webhook&repo=acme/app');
  await expect(page.locator('#overview [data-trigger="push"]')).toHaveClass('pl-fired', {timeout: 8000});
  await expect(page.locator('#overview [data-trigger="schedule"]')).not.toHaveClass('pl-fired');
  await expect(page.locator('#overview .pl-engine small')).toHaveText('syncing acme/app · by push');
  await expect(page.locator('#overview [data-target="github"]')).toHaveClass('pl-busy');
  await request.post('/test/complete');
  await expect(page.locator('#overview .pl-fired')).toHaveCount(0, {timeout: 8000});
});
