import type { Page } from "@playwright/test";

type Handler = (...args: unknown[]) => unknown;
type WailsCall = { object: number; args?: { methodID: number; args?: unknown[] } };

// Wails identifies Desktop methods by the FNV-1a hash of their qualified name.
function desktopMethodID(method: string): number {
  let hash = 0x811c9dc5;
  for (const char of `main.Desktop.${method}`) {
    hash = Math.imul(hash ^ char.charCodeAt(0), 0x01000193);
  }
  return hash >>> 0;
}

/** Stub the native host at the Wails transport, leaving the UI and bindings intact. */
export async function mockDesktop(page: Page, methods: Record<string, Handler> = {}) {
  const handlers: Record<string, Handler> = {
    State: () => ({ version: "test", defaultDirectory: "C:\\Downloads", autostart: false }),
    Settings: () => ({ desktop: { language: "en" } }),
    SetLocale: () => null,
    CanConvert: () => false,
    MissingDownloadFiles: () => ({}),
    List: () => [],
    ...methods,
  };
  handlers.QueueSnapshot ??= async () => ({ items: await handlers.List(), sequence: 0 });
  const bound = new Map(Object.entries(handlers).map(([name, handler]) => [desktopMethodID(name), handler]));
  await page.route("**/wails/runtime", async (route) => {
    const call: WailsCall = route.request().postDataJSON();
    const handler = call.object === 0 && call.args && bound.get(call.args.methodID);
    try {
      if (!handler) throw new Error(`Unexpected Wails call ${call.object}/${call.args?.methodID}`);
      await route.fulfill({ json: (await handler(...(call.args?.args ?? []))) ?? null });
    } catch (error) {
      await route.fulfill({
        status: 500,
        json: { kind: "RuntimeError", message: error instanceof Error ? error.message : String(error) },
      });
    }
  });
}
