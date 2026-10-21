import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { expect, test } from "../../core/fixtures/base.fixture";

// Uses an actual upstream and persists the route through the UI. No provider or
// paid API is involved. Each test owns only its uniquely named route.
test("API route CRUD and OCR proxy", async ({ page, request }) => {
  const name = `OCR ${randomUUID()}`;
  const publicPath = `/api/v2/ocr-${randomUUID()}`;
  const key = randomUUID() + randomUUID();
  const upstream = createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ path: req.url, method: req.method, body, authorization: req.headers.authorization, gateway_key: req.headers["x-bifrost-api-key"] ?? null }));
  });
  await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", resolve));
  const address = upstream.address();
  if (!address || typeof address === "string") throw new Error("upstream did not bind");
  try {
    await page.goto("/workspace/apis");
    await page.getByTestId("add-api-route").click();
    await page.getByLabel("Name", { exact: true }).fill(name);
    await page.getByLabel("Upstream URL").fill(`http://127.0.0.1:${address.port}`);
    await page.getByLabel("Public path", { exact: true }).fill(publicPath);
    await page.getByLabel("Consumer name", { exact: true }).fill("application");
    await page.getByLabel("Access key (at least 32 random characters)").fill(key);
    await page.getByRole("button", { name: "Add consumer", exact: true }).click();
    await page.getByTestId("save-api-route").click();
    const row = page.getByTestId("api-route-row").filter({ hasText: name });
    await expect(row).toBeVisible();
    const denied = await request.post(`${publicPath}/jobs`);
    expect(denied.status()).toBe(401);
    const proxied = await request.post(`${publicPath}/jobs?format=json`, {
      headers: { "X-Bifrost-API-Key": key, Authorization: "Bearer test-upstream" }, data: "document",
    });
    expect(proxied.status(), await proxied.text()).toBe(200);
    expect(await proxied.json()).toEqual({ path: "/jobs?format=json", method: "POST", body: "document", authorization: "Bearer test-upstream", gateway_key: null });
    await row.getByRole("button", { name: "Edit", exact: true }).click();
    await page.getByLabel("Enabled", { exact: true }).uncheck();
    await page.getByTestId("save-api-route").click();
    await expect(row).toContainText("Disabled");
    expect((await request.get(publicPath, { headers: { "X-Bifrost-API-Key": key } })).status()).toBe(404);
    // Verify configuration survives a fresh UI fetch.
    await page.reload();
    await expect(row).toContainText("Disabled");
    page.once("dialog", (dialog) => dialog.accept());
    await row.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(row).toHaveCount(0);
  } finally {
    const current = await request.get("/api/api-gateway/routes");
    if (current.ok()) {
      const data = await current.json();
      if (data.routes.some((r: { name: string }) => r.name === name)) {
        const cleanup = await request.put("/api/api-gateway/routes", { data: { revision: data.revision, routes: data.routes.filter((r: { name: string }) => r.name !== name) } });
        expect(cleanup.status()).toBe(200);
      }
    }
    await new Promise<void>((resolve, reject) => upstream.close((err) => err ? reject(err) : resolve()));
  }
});
