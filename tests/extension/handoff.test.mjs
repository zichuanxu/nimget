import assert from "node:assert/strict";
import test from "node:test";
import { lookupHandoffReceipt, submitHandoff } from "../../extension/handoff.js";

const handoffId = "06278ae9-f42b-44e4-8384-e9d449af4d5e";
const request = { handoffId, url: "https://example.test/archive.zip" };

test("a handoff requires a durable receipt with a task ID", async (t) => {
  const transport = t.mock.method(globalThis, "fetch", async () => Response.json({ taskId: "saved" }, { status: 201 }));
  assert.equal(await submitHandoff(request, "fixture-token"), "saved");

  transport.mock.mockImplementation(async () => Response.json({ taskId: "saved" }, { status: 200 }));
  assert.equal(await submitHandoff(request, "fixture-token"), "saved");
  assert.equal((await lookupHandoffReceipt(handoffId, "fixture-token")).taskId, "saved");

  transport.mock.mockImplementation(async () => Response.json({ taskId: "pending" }, { status: 202 }));
  await assert.rejects(submitHandoff(request, "fixture-token"), /UNEXPECTED_RESPONSE_STATUS/);
  await assert.rejects(lookupHandoffReceipt(handoffId, "fixture-token"), /UNEXPECTED_RESPONSE_STATUS/);

  transport.mock.mockImplementation(async () => Response.json({ taskId: " " }, { status: 201 }));
  await assert.rejects(submitHandoff(request, "fixture-token"), /HANDOFF_UNCONFIRMED/);
});
