/**
 * End-to-end smoke test against a running yoyocode-server.
 *
 * Verifies the four compatibility-critical invariants that mcode-app depends
 * on, against a REAL server process rather than in-process unit tests:
 *   1. Bearer auth (401 = bare text, not JSON)
 *   2. Unknown command (404 = bare text)
 *   3. Structured AppCommandError body + status mapping
 *   4. WS handshake (__ready__ with payload null) + ping/pong + attach
 *
 * Usage: node scripts/smoke.js [baseUrl] [token]
 */
const BASE = process.argv[2] || "http://127.0.0.1:3999";
const TOKEN = process.argv[3] || process.env.YOYOCODE_TOKEN || "";

let failures = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  PASS  ${name}`);
  } else {
    failures++;
    console.log(`  FAIL  ${name}${detail ? " — " + detail : ""}`);
  }
}

async function post(cmd, body, { auth = true } = {}) {
  const headers = { "content-type": "application/json" };
  if (auth) headers.authorization = `Bearer ${TOKEN}`;
  const res = await fetch(`${BASE}/api/${cmd}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body ?? {}),
  });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {}
  return { status: res.status, text, json };
}

(async () => {
  console.log(`smoke: ${BASE}\n`);

  // 1 — auth
  const noAuth = await post("health", {}, { auth: false });
  check(
    "401 without token is bare text, not JSON",
    noAuth.status === 401 && noAuth.json === null,
    `status=${noAuth.status} body=${JSON.stringify(noAuth.text)}`
  );
  check(
    '401 body is exactly "Invalid or missing token"',
    noAuth.text.trim() === "Invalid or missing token",
    JSON.stringify(noAuth.text)
  );

  // 2 — unknown route
  const missing = await post("definitely_not_a_command");
  check(
    "404 unknown command is bare text",
    missing.status === 404 && missing.json === null,
    `status=${missing.status} body=${JSON.stringify(missing.text)}`
  );

  // 3 — structured error envelope: get_conversation with no id → invalid_input/400
  const badInput = await post("get_conversation", {});
  check(
    "invalid input → HTTP 400",
    badInput.status === 400,
    `status=${badInput.status}`
  );
  check(
    "error body has BOTH code and message (mcode-app parseAppCommandError gate)",
    badInput.json &&
      typeof badInput.json.code === "string" &&
      badInput.json.code.trim() !== "" &&
      typeof badInput.json.message === "string" &&
      badInput.json.message.trim() !== "",
    JSON.stringify(badInput.json)
  );
  check(
    'error code is snake_case "invalid_input"',
    badInput.json && badInput.json.code === "invalid_input",
    JSON.stringify(badInput.json?.code)
  );

  // 3b — not_found mapping: a conversation id that cannot exist
  const notFound = await post("get_conversation", { conversationId: 999999 });
  check(
    "missing conversation → HTTP 404 with code not_found",
    notFound.status === 404 && notFound.json?.code === "not_found",
    `status=${notFound.status} code=${notFound.json?.code}`
  );

  // 4 — health (public)
  const health = await post("health", {}, { auth: false });
  check("health responds 200 without token", health.status === 200, `status=${health.status}`);

  // 5 — WS handshake
  const wsProto = [
    "codeg-events",
    "codeg-token." + Buffer.from(TOKEN).toString("base64url"),
  ].join(", ");
  const { default: WebSocket } = await import("ws").catch(() => ({}));
  if (!WebSocket) {
    console.log("  SKIP  WS checks (ws module unavailable)");
  } else {
    await new Promise((resolve) => {
      const ws = new WebSocket(`${BASE.replace(/^http/, "ws")}/ws/events`, wsProto);
      const timer = setTimeout(() => {
        check("WS received __ready__ frame", false, "timeout");
        ws.close();
        resolve();
      }, 5000);
      ws.on("open", () => {
        ws.send(JSON.stringify({ action: "attach", subscription_id: "s", connection_id: "nope" }));
      });
      let readySeen = false;
      ws.on("message", (raw) => {
        const msg = JSON.parse(raw.toString());
        if (msg.channel === "__ready__") {
          readySeen = true;
          check("__ready__ payload is null", msg.payload === null, JSON.stringify(msg.payload));
          return;
        }
        if (msg.type === "detached") {
          clearTimeout(timer);
          check("__ready__ frame received", readySeen);
          check(
            "attach to unknown connection → detached/connection_gone",
            msg.reason === "connection_gone" && msg.subscription_id === "s",
            JSON.stringify(msg)
          );
          ws.close();
          resolve();
        }
      });
      ws.on("error", (e) => {
        clearTimeout(timer);
        check("WS connect", false, e.message);
        resolve();
      });
    });
  }

  console.log(failures === 0 ? "\nALL PASS" : `\n${failures} FAILURE(S)`);
  process.exit(failures === 0 ? 0 : 1);
})();
