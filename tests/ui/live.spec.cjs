const { test, expect } = require('@playwright/test');

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
  await diff.locator('summary').click();
  await page.waitForResponse(response => response.url().endsWith('/fragments/repos'));
  await expect(diff).not.toHaveAttribute('open');
});

test('sync updates current diff and retains the applied plan without reloading', async ({ page }) => {
  await page.goto('/');
  await page.evaluate(() => {window.pageIdentity = 'retained';});
  await page.locator('.repo-actions button', { hasText: /^Sync$/ }).click();
  await expect(page.locator('.applied summary')).toContainText('Last applied plan', {timeout: 12000});
  await expect(page.locator('.repo-head')).toContainText('in sync');
  await expect(page.locator('details.diff').first().locator('.changes code')).toHaveCount(0);
  await expect(page.locator('.repo-actions button', { hasText: /^Sync$/ })).toBeFocused();
  await page.locator('.applied summary').click();
  await expect(page.locator('.applied .changes').first()).toContainText('refs/heads/main');
  expect(await page.evaluate(() => window.pageIdentity)).toBe('retained');
  await page.waitForResponse(response => response.url().endsWith('/fragments/repos'));
  await expect(page.locator('.applied')).toHaveAttribute('open', '');
});

test('deferred updates coalesce, retain action notices and reject older responses', async ({ page }) => {
  await page.goto('/');
  await page.locator('.repo-name').evaluate(node => {
    const range = document.createRange(); range.selectNodeContents(node);
    window.getSelection().removeAllRanges(); window.getSelection().addRange(range);
    const target = document.getElementById('repos');
    const respond = (seq, html) => document.dispatchEvent(new CustomEvent('htmx:beforeSwap', {
      detail: { target, shouldSwap: true, serverResponse: html, xhr: { forgemirrorSequence: seq } }
    }));
    respond(10000, '<p class="notice" role="status">Sync accepted</p><p>older snapshot</p>');
    respond(10001, '<p data-live-key="latest">latest snapshot</p>');
  });
  await expect(page.locator('.repo-name')).toHaveText('acme/app');
  expect(await page.evaluate(() => window.getSelection().toString())).toBe('acme/app');
  await page.evaluate(() => window.getSelection().removeAllRanges());
  await expect(page.locator('#repos')).toContainText('latest snapshot');
  await expect(page.locator('#repos')).toContainText('Sync accepted');
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
