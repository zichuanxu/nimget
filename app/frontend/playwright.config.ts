import { defineConfig } from "@playwright/test";

const port = 19245;
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "./tests",
  use: {
    baseURL,
    channel: process.env.NIMGET_TEST_BROWSER || undefined,
    locale: "en-US",
  },
  webServer: {
    command: `npm run dev -- --port ${port}`,
    url: baseURL,
  },
});
