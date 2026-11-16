import { expect, test, type Page } from "@playwright/test";
import { mockDesktop } from "./desktop";

async function openSettings(page: Page) {
  await page.getByRole("button", { name: "Settings" }).press("Enter");
  const dialog = page.getByRole("dialog", { name: "Settings" });
  await dialog.getByRole("button", { name: "Network" }).press("Enter");
  return dialog;
}

test("edits existing site settings and preserves untouched settings across save and reopen", async ({ page }) => {
  const existing = {
    host: " Files.Example.test ",
    connections: 7,
    hostConnections: 12,
    headers: { "X-Test": " two spaces  ", "X-Empty": "", "x-test": "saved case variant" },
    username: "saved-user",
    password: "********",
  };
  const sibling = { host: "example.test", username: "other-user" };
  const initial = { connections: 8, hostConnections: 16, schedule: {}, sites: [existing, sibling], desktop: { language: "en" } };
  let stored: unknown = initial;
  await mockDesktop(page, {
    Settings: () => stored,
    WhenDone: () => "",
    SaveSettings: (next) => (stored = next),
  });
  await page.goto("/");
  const dialog = await openSettings(page);
  const site = dialog.getByRole("group", { name: "Files.Example.test", exact: true });
  await expect(site.getByLabel("Host name", { exact: true })).toHaveValue(existing.host);
  await expect(site.getByLabel("User name", { exact: true })).toHaveValue("saved-user");
  await expect(site.getByLabel("Password", { exact: true })).toHaveAttribute("type", "password");
  await expect(site.getByLabel("Connections per server", { exact: true })).toHaveValue("12");
  await site.getByLabel("Connections per download", { exact: true }).fill("5");
  await dialog.getByRole("button", { name: "Save", exact: true }).press("Enter");
  await expect(dialog).toHaveCount(0);
  expect(stored).toMatchObject({ sites: [{ ...existing, connections: 5 }, sibling] });

  await openSettings(page);
  await expect(site.getByLabel("Connections per download", { exact: true })).toHaveValue("5");
  await expect(site.getByText(/saved password is masked/)).toBeVisible();
  await expect(site.getByLabel("Header value", { exact: true }).first()).toHaveValue(" two spaces  ");
});

test("validates site fields and keeps entered settings after the service rejects a save", async ({ page }) => {
  const saves: unknown[] = [];
  await mockDesktop(page, {
    WhenDone: () => "",
    SaveSettings: (next) => {
      saves.push(next);
      if (saves.length === 1) throw new Error("Could not write settings to disk");
      return next;
    },
  });
  await page.goto("/");
  const dialog = await openSettings(page);
  await dialog.getByRole("button", { name: "Add site" }).press("Enter");
  const host = dialog.getByLabel("Host name", { exact: true });
  await expect(host).toBeFocused();
  await expect(host).toHaveAttribute("aria-invalid", "false");
  await host.fill("https://example.test/file");
  const connections = dialog.getByLabel("Connections per download", { exact: true });
  await connections.fill("33");
  await dialog.getByRole("button", { name: "Add header" }).press("Enter");
  const name = dialog.getByLabel("Header name", { exact: true });
  await expect(name).toBeFocused();
  await name.fill("Invalid Header");
  await dialog.getByLabel("Header value", { exact: true }).fill("example");
  const save = dialog.getByRole("button", { name: "Save", exact: true });
  await dialog.getByRole("button", { name: "General" }).press("Enter");
  await save.press("Enter");
  await expect(host).toBeFocused();
  await expect(host).toHaveAccessibleDescription(/Enter a host name without a scheme/);
  await expect(connections).toHaveAccessibleDescription(/1 to 32/);
  await expect(name).toHaveAccessibleDescription(/Enter a valid HTTP header name/);
  expect(saves).toHaveLength(0);

  await host.fill("example.test");
  await name.fill("X-Test");
  await connections.fill("");
  await expect(host).toHaveAttribute("aria-invalid", "false");
  await dialog.getByRole("button", { name: "Add header" }).press("Enter");
  const duplicate = dialog.getByLabel("Header name", { exact: true }).last();
  await duplicate.fill("x-test");
  await save.press("Enter");
  await expect(dialog.getByLabel("Header name", { exact: true }).first()).toBeFocused();
  await expect(duplicate).toHaveAccessibleDescription(/Use each header name once per site/);
  expect(saves).toHaveLength(0);
  await dialog.getByRole("button", { name: "Remove header" }).last().press("Enter");
  await save.press("Enter");
  await expect(dialog.getByRole("alert")).toContainText("Could not write settings to disk");
  await expect(host).toHaveValue("example.test");
  await expect(dialog.getByLabel("Header value", { exact: true })).toHaveValue("example");
  await save.press("Enter");
  await expect(dialog).toHaveCount(0);
  expect(saves).toHaveLength(2);
  expect(saves[0]).toMatchObject({ sites: [{ host: "example.test", headers: { "X-Test": "example" } }] });
  expect(saves[1]).toEqual(saves[0]);
});

test("edits rules in Chinese with the dark theme and checks a renamed site's saved password", async ({ page }) => {
  const saves: unknown[] = [];
  await mockDesktop(page, {
    Settings: () => ({ desktop: { language: "zh-CN" }, sites: [
      { host: "old.test", password: "********", headers: { "X-Retired": "one" } },
      { host: "remove.test" },
    ] }),
    WhenDone: () => "",
    SaveSettings: (next) => {
      saves.push(next);
      return next;
    },
  });
  await page.goto("/");
  await page.getByRole("button", { name: "设置" }).press("Enter");
  const dialog = page.getByRole("dialog", { name: "设置" });
  await dialog.getByRole("radio", { name: "深色" }).press("Space");
  await dialog.getByRole("button", { name: "网络" }).press("Enter");
  const site = dialog.getByRole("group", { name: "old.test" });
  await site.getByRole("button", { name: "移除请求头" }).press("Enter");
  await expect(site.getByRole("button", { name: "添加请求头" })).toBeFocused();
  await site.getByLabel("主机名", { exact: true }).fill("new.test");
  const renamed = dialog.getByRole("group", { name: "new.test" });
  const save = dialog.getByRole("button", { name: "保存", exact: true });
  await save.press("Enter");
  const password = renamed.getByLabel("密码", { exact: true });
  await expect(password).toBeFocused();
  await expect(password).toHaveAccessibleDescription(/请输入此主机的密码/);
  expect(saves).toHaveLength(0);
  await password.fill("");
  await dialog.getByRole("group", { name: "remove.test" }).getByRole("button", { name: "移除站点" }).press("Enter");
  await expect(dialog.getByRole("button", { name: "添加站点" })).toBeFocused();
  await save.press("Enter");
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(saves).toHaveLength(1);
  expect(saves[0]).toMatchObject({ desktop: { language: "zh-CN" }, sites: [{ host: "new.test" }] });
  expect(saves[0]).not.toHaveProperty("sites.0.password");
  expect(saves[0]).not.toHaveProperty("sites.0.headers");
});
