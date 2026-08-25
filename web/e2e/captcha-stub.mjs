import http from "node:http";

/* global URLSearchParams, process */

const address = process.env.XBOARD_CAPTCHA_STUB_ADDRESS ?? "127.0.0.1";
const port = 4199;
const maximumBodyBytes = 16 * 1024;

const server = http.createServer((request, response) => {
  if (request.method === "GET" && request.url === "/healthz") {
    response.writeHead(200, { "Content-Type": "text/plain" });
    response.end("ok");
    return;
  }
  if (request.method !== "POST" || !["/recaptcha", "/recaptcha-v3", "/turnstile"].includes(request.url ?? "")) {
    response.writeHead(404).end();
    return;
  }
  let body = "";
  request.setEncoding("utf8");
  request.on("data", (chunk) => {
    body += chunk;
    if (body.length > maximumBodyBytes) request.destroy();
  });
  request.on("end", () => {
    const token = new URLSearchParams(body).get("response") ?? "";
    let payload = { success: false, "error-codes": ["invalid-input-response"] };
    if (request.url === "/recaptcha" && token === "e2e-v2-token") payload = { success: true, hostname: "127.0.0.1", "error-codes": [] };
    if (request.url === "/recaptcha-v3" && token.startsWith("e2e-v3:")) {
      payload = { success: true, score: 0.9, action: token.slice("e2e-v3:".length), hostname: "127.0.0.1", "error-codes": [] };
    }
    if (request.url === "/turnstile" && token.startsWith("e2e-turnstile:")) {
      payload = { success: true, action: token.slice("e2e-turnstile:".length), hostname: "127.0.0.1", "error-codes": [] };
    }
    response.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" });
    response.end(JSON.stringify(payload));
  });
});

server.listen(port, address);

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
