const { test, expect } = require('@playwright/test');
const { AxeBuilder } = require('@axe-core/playwright');

// The documentation is held to the same bar as the UI: WCAG 2.2 AA. axe
// files heading-order under best practice, but skipped levels break heading
// navigation in screen readers, and Hextra's home layout produces them.
const wcag = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];
const extraRules = ['heading-order'];

// Every published page, from the sitemap, so new pages are covered without
// touching this file.
async function pages(request) {
  const xml = await (await request.get('/sitemap.xml')).text();
  const paths = [...xml.matchAll(/<loc>([^<]+)<\/loc>/g)].map(m => new URL(m[1]).pathname);
  expect(paths.length, 'sitemap lists pages').toBeGreaterThan(5);
  return [...new Set(paths)];
}

for (const scheme of ['light', 'dark']) {
  test(`axe finds no WCAG 2.2 AA violations on any page: ${scheme}`, async ({ page, request }) => {
    test.setTimeout(180000);
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
    // The table of contents only shows from the xl breakpoint on; at a
    // laptop height its sticky footer overlaps the list (target size).
    await page.setViewportSize({ width: 1440, height: 768 });
    for (const path of await pages(request)) {
      await page.goto(path);
      const results = [
        await new AxeBuilder({ page }).withTags(wcag).analyze(),
        await new AxeBuilder({ page }).withRules(extraRules).analyze(),
      ];
      const found = results.flatMap(r => r.violations).flatMap(v => v.nodes.map(n => `${v.id}: ${n.target.join(' ')}`));
      expect.soft(found, path).toEqual([]);
    }
  });
}

// The home page screenshot is the LCP element: it must load eagerly at high
// priority, and only the image of the active theme may be downloaded.
const visibleShot = page => page.locator('.fm-ribbon img').filter({ visible: true });

for (const scheme of ['light', 'dark']) {
  test(`the home screenshot loads once, with priority: ${scheme} system theme`, async ({ page }) => {
    const fetched = [];
    page.on('request', r => { if (r.resourceType() === 'image' && r.url().endsWith('.webp')) fetched.push(r.url()); });
    await page.emulateMedia({ colorScheme: scheme });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/', { waitUntil: 'networkidle' });
    const shot = visibleShot(page);
    await expect(shot).toHaveCount(1);
    await expect(shot).toHaveAttribute('fetchpriority', 'high');
    await expect(shot).not.toHaveAttribute('loading', 'lazy');
    const src = await shot.evaluate(img => img.currentSrc);
    expect(src).toContain(`-${scheme}`);
    expect(fetched).toEqual([src]);
  });
}

// Hextra's toggle can pick the other theme than the system one; the matching
// screenshot then replaces the system one.
for (const [system, chosen] of [['light', 'dark'], ['dark', 'light']]) {
  test(`the home screenshot follows the theme toggle: ${chosen} on a ${system} system`, async ({ page }) => {
    await page.addInitScript(theme => localStorage.setItem('color-theme', theme), chosen);
    await page.emulateMedia({ colorScheme: system });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/', { waitUntil: 'networkidle' });
    const shot = visibleShot(page);
    await expect(shot).toHaveCount(1);
    await expect(shot).toHaveClass(`fm-shot-override-${chosen}`);
    expect(await shot.evaluate(img => img.complete && img.naturalWidth > 0)).toBe(true);
  });
}

// A plain click opens the full-size screenshot of the active theme in a modal
// dialog; Esc, the Close button and the backdrop close it again.
for (const scheme of ['light', 'dark']) {
  test(`the screenshot opens in a lightbox and closes again: ${scheme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/docs/web-ui/');
    const dialog = page.locator('dialog.fm-lightbox');
    const open = () => page.locator('.fm-shot-open').click();

    await open();
    await expect(dialog).toHaveJSProperty('open', true);
    const shown = dialog.locator('img').filter({ visible: true });
    await expect(shown).toHaveCount(1);
    await expect.poll(() => shown.evaluate(img => img.complete && img.naturalWidth)).toBe(2880);
    expect(await shown.evaluate(img => img.currentSrc)).toContain(`-${scheme}`);
    await expect(dialog.getByRole('button', { name: 'Close' })).toBeFocused();
    const { violations } = await new AxeBuilder({ page }).withTags(wcag).analyze();
    expect(violations.map(v => v.id)).toEqual([]);

    await page.keyboard.press('Escape');
    await expect(dialog).toHaveJSProperty('open', false);

    await open();
    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toHaveJSProperty('open', false);

    await open();
    await page.mouse.click(5, 450); // beside the image: the backdrop
    await expect(dialog).toHaveJSProperty('open', false);
  });
}

// Without the lightbox the link is the fallback: it must open the full-size
// image, and modified clicks (new tab) must not be swallowed by the dialog.
test('the screenshot link opens the full-size image', async ({ page, request, context }) => {
  await page.goto('/docs/web-ui/');
  const link = page.locator('.fm-shot-open');
  const href = await link.getAttribute('href');
  const image = await request.get(href);
  expect(image.status()).toBe(200);
  expect(image.headers()['content-type']).toContain('image/webp');

  const popup = context.waitForEvent('page');
  await link.click({ modifiers: [process.platform === 'darwin' ? 'Meta' : 'Control'] });
  await (await popup).close();
  await expect(page.locator('dialog.fm-lightbox')).toHaveJSProperty('open', false);
});
