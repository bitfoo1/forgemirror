const { defineConfig } = require('@playwright/test');
const port = process.env.FORGEMIRROR_DOCS_TEST_PORT;
const baseURL = 'http://127.0.0.1:' + port;
module.exports = defineConfig({
  testDir: '.',
  testMatch: '*.spec.cjs',
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  outputDir: '../../.superpowers/browser-results/docs',
  use: { baseURL, browserName: 'chromium' },
  webServer: {
    // The production environment renders what is published (Hextra changes
    // its robots and analytics output outside production); without live
    // reload no development script is injected into the pages under test.
    command: 'hugo server --source docs --environment production --bind 127.0.0.1 --port ' + port +
      ' --baseURL ' + baseURL + '/ --appendPort=false --disableLiveReload --renderToMemory --quiet',
    cwd: '../..',
    url: baseURL + '/sitemap.xml',
    reuseExistingServer: false,
    timeout: 120000,
  },
});
