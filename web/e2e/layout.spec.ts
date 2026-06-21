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

test("opens the sidebar from a mobile viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await expect(page.locator(".sidebar")).not.toBeInViewport();
  await page.getByRole("button", { name: "Open sidebar" }).click();
  await expect(page.locator(".sidebar")).toBeInViewport();
});

test("renders the dark color scheme", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  await expect(page.locator(".chat-main")).toHaveCSS("background-color", "rgb(33, 33, 33)");
});

test("opens provider settings", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /Settings/ }).click();
  await expect(page.getByRole("dialog", { name: "Provider settings" })).toBeVisible();
});

test("browses manuals and renders a streamed answer", async ({ page }) => {
  const manual = { id: "dx7", title: "DX7 Manual", source_path: "dx7.md", tags: [], chunk_count: 12, page_count: 4 };
  let requestedMode = "";
  await page.unroute("**/api/documents");
  await page.route("**/api/documents", (route) => route.fulfill({ json: [manual] }));
  await page.route("**/api/ask/stream", async (route) => {
    requestedMode = (await route.request().postDataJSON()).mode;
    await route.fulfill({ status: 200, contentType: "text/event-stream", body: 'event: status\ndata: {"message":"Planning guide…"}\n\nevent: delta\ndata: {"content":"Use local control."}\n\nevent: result\ndata: {"answer":"Use local control.","mode":"guide","model":"test","provider_url":"local","sources":[],"retrieval":{"query":"local control","blocks":[]}}\n\n' });
  });
  await page.goto("/");
  await page.getByRole("button", { name: /Browse manuals/ }).click();
  await expect(page.getByRole("dialog", { name: "Manual library" }).getByText("DX7 Manual")).toBeVisible();
  await page.getByRole("button", { name: "Close manual library" }).click();
  await page.getByRole("button", { name: "Guide" }).click();
  await page.getByPlaceholder("Message DogEar").fill("How do I configure it?");
  await page.getByRole("button", { name: "Send message" }).click();
  await expect(page.getByText("Use local control.")).toBeVisible();
  await expect(page.locator(".mode-badge")).toHaveText("Guide");
  expect(requestedMode).toBe("guide");
});
