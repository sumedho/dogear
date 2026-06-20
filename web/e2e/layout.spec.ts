import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.route("**/api/documents", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/settings", (route) => route.fulfill({ json: { provider: { base_url: "", model: "", timeout: "30s", api_key_set: false }, embedding: { base_url: "", model: "", timeout: "30s", api_key_set: false, dimensions: 768, batch_size: 16, query_instruction: "" }, environment_overrides: [] } }));
  await page.route("**/api/index/embeddings/status", (route) => route.fulfill({ json: { configured: false, complete: false, stale: false, model: "", dimensions: 0, indexed: 0, total: 0 } }));
});

test("keeps the sidebar and composer inside the desktop viewport", async ({ page }) => {
  await page.goto("/");
  const sidebar = page.locator(".sidebar");
  const composer = page.locator(".composer-wrap");
  await expect(sidebar).toBeVisible();
  const [sidebarBox, composerBox] = await Promise.all([sidebar.boundingBox(), composer.boundingBox()]);
  expect(sidebarBox?.y).toBeGreaterThanOrEqual(0);
  expect(composerBox?.y).toBeGreaterThanOrEqual(0);
  expect((composerBox?.y || 0) + (composerBox?.height || 0)).toBeLessThanOrEqual(await page.evaluate(() => innerHeight));
});
