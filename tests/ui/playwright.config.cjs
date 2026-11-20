const { defineConfig } = require('@playwright/test');
const baseURL = 'http://127.0.0.1:' + process.env.FORGEMIRROR_UI_TEST_PORT;
module.exports = defineConfig({
  testDir: '.',
  testMatch: '*.spec.cjs',
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  outputDir: '../../.superpowers/browser-results',
  use: { baseURL, browserName: 'chromium' },
  webServer: {
    command: '"' + process.env.FORGEMIRROR_UI_TEST_SERVER + '"',
    cwd: '../..',
    url: baseURL,
    reuseExistingServer: false,
    timeout: 60000,
  },
});
