const { test, expect } = require('@playwright/test');
const { AxeBuilder } = require('@axe-core/playwright');

// WCAG 2.2 AA is the bar AGENTS.md sets for the UI; best-practice rules are
// left out because they are advisory and would fail the gate on taste.
const wcag = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];

test.afterEach(async ({ request }) => {
  await request.post('/test/complete');
  await request.post('/test/hold?enabled=false');
});

async function audit(page) {
  const { violations } = await new AxeBuilder({ page }).withTags(wcag).analyze();
  // Selector, rule and colours make a failure actionable without a rerun.
  return violations.flatMap(v => v.nodes.map(n => {
    const data = n.any.concat(n.all)[0]?.data ?? {};
    const colours = data.fgColor ? ` ${data.fgColor} on ${data.bgColor} = ${data.contrastRatio}` : '';
    return `${v.id}: ${n.target.join(' ')}${colours}`;
  }));
}

// Each state brings markup the others lack: diffs and badges, the running
// pipeline with spinners and a held target, and the modal confirmation.
const states = {
  'idle with an out-of-sync repository': async ({ page, request }) => {
    await request.post('/test/seed?repo=acme/app');
    await page.goto('/');
    await page.locator('.target-details > summary').first().click();
  },
  'syncing with a held target': async ({ page, request }) => {
    await request.post('/test/hold');
    await request.post('/test/trigger?trigger=webhook&repo=acme/app');
    await page.goto('/');
    await expect(page.locator('.pl-engine.pl-busy')).toBeVisible();
  },
  'confirmation dialog open': async ({ page, request }) => {
    await request.post('/test/seed?repo=acme/app');
    await page.goto('/');
    await page.locator('[data-confirm-title="Sync all repositories"]').click();
    await expect(page.locator('#confirm-dialog')).toBeVisible();
  },
};

for (const scheme of ['light', 'dark']) {
  for (const [name, setup] of Object.entries(states)) {
    test(`axe finds no WCAG 2.2 AA violations: ${scheme}, ${name}`, async ({ page, request }) => {
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
      await setup({ page, request });
      expect(await audit(page)).toEqual([]);
    });
  }
}

// The pipeline picks its orientation from its card's width (container query),
// so a narrow desktop window gets the vertical layout instead of a squeezed
// strip, and no width scrolls sideways.
test('the pipeline is horizontal only when it fits and never overflows', async ({ page, request }) => {
  await request.post('/test/hold');
  await request.post('/test/trigger?trigger=webhook&repo=acme/app');
  for (const width of [1728, 1280, 1100, 1000, 900, 640, 390, 320]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/');
    const layout = await page.evaluate(() => {
      const box = sel => document.querySelector(sel).getBoundingClientRect();
      const flow = document.querySelector('.pl-flow');
      const triggers = box('.pl-triggers'), engine = box('.pl-engine'), targets = box('.pl-targets');
      return {
        horizontal: engine.left >= triggers.right && targets.left >= engine.right,
        vertical: engine.top >= triggers.bottom && targets.top >= engine.bottom,
        overflow: flow.scrollWidth > flow.clientWidth + 1 || document.documentElement.scrollWidth > window.innerWidth,
        centred: Math.abs((triggers.left - box('.pl-flow').left) - (box('.pl-flow').right - targets.right)),
      };
    });
    expect(layout.overflow, `overflow at ${width}px`).toBe(false);
    expect(layout.horizontal !== layout.vertical, `one orientation at ${width}px`).toBe(true);
    if (width >= 1100) {
      expect(layout.horizontal, `horizontal at ${width}px`).toBe(true);
      // The strip is centred in the card (within the targets' ragged notes).
      expect(layout.centred, `centred at ${width}px`).toBeLessThan(200);
    }
    if (width <= 900) expect(layout.vertical, `vertical at ${width}px`).toBe(true);
  }
});
