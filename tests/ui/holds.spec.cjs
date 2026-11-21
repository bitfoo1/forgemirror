const {test,expect}=require('@playwright/test');

test.beforeEach(async({request})=>{
 await request.post('/test/complete');await request.post('/test/hold?enabled=false');await request.post('/test/reset-controls');await request.post('/test/seed?repo=acme/app');
});
test.afterEach(async({request})=>{
 await request.post('/test/complete');await request.post('/test/hold?enabled=false');await request.post('/test/reset-controls');await request.post('/test/seed?repo=acme/app');
});
async function review(page){for(let i=0;i<3;i++)await page.locator('[data-hold-next]').click();}

test('H+C creates real independent holds and resumes only the selected ID',async({page})=>{
 page.on('dialog',dialog=>dialog.accept());await page.goto('/');
 expect(await page.locator('#controls').evaluate(node=>!node.closest('#repos'))).toBe(true);
 await expect(page.locator('#hold-current-tab')).toHaveText('Current holds (0)');
 await expect(page.locator('[data-duration-label]')).toHaveText('Until resumed');
 await review(page);await page.locator('[data-hold-submit]').click();
 await expect(page.locator('#hold-current-tab')).toHaveText('Current holds (1)');await expect(page.locator('#hold-current-tab')).toHaveAttribute('aria-selected','true');await expect(page.locator('.current-hold')).toHaveCount(1);
 await page.locator('#hold-new-tab').click();await page.getByRole('button',{name:'Remove All targets',exact:true}).click();await page.locator('#hold-target-search').fill('GitHub');await page.locator('#hold-target-search').press('Enter');await page.locator('[data-picker=targets] [data-picker-done]').click();await page.locator('[data-hold-next]').click();
 await page.locator('#hold-repository-search').fill('acme/*');await page.locator('#hold-repository-search').press('Enter');await page.locator('[data-picker=repositories] [data-picker-done]').click();await page.locator('[data-hold-next]').click();await page.locator('#hold-reason').fill('repo maintenance');await page.locator('[data-hold-next]').click();await page.locator('[data-hold-submit]').click();
 await expect(page.locator('#hold-current-tab')).toHaveText('Current holds (2)');
 const global=page.locator('.current-hold').filter({hasText:'All targets'});await global.getByRole('button',{name:'Resume & sync'}).click();await expect(page.locator('.current-hold')).toHaveCount(1);await expect(page.locator('.current-hold')).toContainText('repo maintenance');
 await page.reload();await page.locator('#hold-current-tab').click();await expect(page.locator('.current-hold')).toHaveCount(1);await expect(page.locator('.current-hold')).toContainText('acme/*');
 await page.locator('.current-hold button[type=submit]').click();await expect(page.locator('#hold-current-tab')).toHaveText('Current holds (0)');await expect(page.locator('[data-no-holds]')).toBeVisible();
});

test('live controls preserve wizard draft, badges, custom duration and tab while current holds update',async({page,request})=>{
 await page.goto('/');await page.getByRole('button',{name:'Remove All targets',exact:true}).click();await page.locator('#hold-target-search').fill('GitHub');await page.locator('#hold-target-search').press('Enter');await page.locator('[data-picker=targets] [data-picker-done]').click();await page.locator('[data-hold-next]').click();
 await page.locator('#hold-repository-search').fill('acme/*');await page.locator('#hold-repository-search').press('Enter');await page.locator('[data-picker=repositories] [data-picker-done]').click();await page.locator('[data-hold-next]').click();await page.locator('#hold-duration-button').click();await page.locator('[data-duration=custom]').click();await page.locator('#hold-custom-duration').fill('45m');await page.locator('#hold-reason').fill('<img src=x onerror=alert(1)>');
 await request.post('/actions/hold',{form:{all_targets:'true',all_repositories:'true'}});
 await page.locator('#hold-current-tab').click();await page.waitForResponse(response=>response.url().endsWith('/fragments/controls'));
 await expect(page.locator('#hold-current-tab')).toHaveAttribute('aria-selected','true');await expect(page.locator('#hold-current-tab')).toHaveText('Current holds (1)');await page.locator('#hold-new-tab').click();
 await expect(page.locator('[data-hold-step="2"]')).toHaveAttribute('aria-current','step');await expect(page.locator('[data-picker=targets] .hold-token-label')).toHaveText(['GitHub']);await expect(page.locator('[data-picker=repositories] .hold-token-label')).toHaveText(['acme/*']);await expect(page.locator('#hold-custom-duration')).toHaveValue('45m');await expect(page.locator('#hold-reason')).toHaveValue('<img src=x onerror=alert(1)>');await expect(page.locator('img[src=x]')).toHaveCount(0);
 await page.waitForResponse(response=>response.url().endsWith('/fragments/controls'));await expect(page.locator('#hold-custom-duration')).toHaveValue('45m');
});

test('badge pickers, centered icons and duration work on mobile with empty-scope protection',async({page})=>{
 await page.setViewportSize({width:390,height:900});await page.goto('/');await page.getByRole('button',{name:'Remove All targets',exact:true}).click();await page.locator('[data-picker=targets] [data-picker-done]').click();await page.locator('[data-hold-next]').click();await expect(page.locator('[data-hold-validation]')).toContainText('Choose at least one');
 await page.locator('#hold-target-search').fill('GitHub');await page.locator('#hold-target-search').press('Enter');await expect(page.locator('[data-picker=targets] [role=option]').filter({hasText:'GitHub'})).toHaveCount(0);await page.locator('[data-picker=targets] [data-picker-done]').click();await page.locator('[data-hold-next]').click();await page.locator('#hold-repository-search').fill('acme/*');await page.locator('#hold-repository-search').press(',');await expect(page.locator('[data-picker=repositories] .hold-token-label')).toHaveText(['acme/*']);await page.getByRole('button',{name:'Remove acme/*',exact:true}).click();await expect(page.locator('[data-picker=repositories] [role=option]').filter({hasText:'acme/app'})).toBeVisible();await page.locator('[data-picker=repositories] [role=option]').filter({hasText:'acme/app'}).click();await page.locator('[data-picker=repositories] [data-picker-done]').click();await page.locator('[data-hold-next]').click();await page.locator('#hold-duration-button').press('ArrowDown');await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');await expect(page.locator('[data-duration-label]')).toHaveText('30 minutes');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
});

test.describe('native forms',()=>{
 test.use({javaScriptEnabled:false});
 test('holds can be created and removed without JavaScript',async({page})=>{
  await page.goto('/');await expect(page.locator('#hold-all-targets')).toBeChecked();await expect(page.locator('#hold-all-repositories')).toBeChecked();await expect(page.locator('#hold-duration-source')).toHaveValue('until-resumed');await page.locator('[data-hold-submit]').click();await expect(page.locator('.current-hold')).toHaveCount(1);await expect(page.locator('.current-hold')).toContainText('Until resumed');await page.locator('.current-hold button[type=submit]').click();await expect(page.locator('.current-hold')).toHaveCount(0);
 });
});
