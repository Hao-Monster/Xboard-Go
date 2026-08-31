import { gzipSync } from "node:zlib";
import { readFileSync } from "node:fs";
import { dirname, isAbsolute, join, normalize, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distRoot = join(webRoot, "dist");
const html = readFileSync(join(distRoot, "index.html"), "utf8");
const scripts = [...html.matchAll(/<script\b[^>]*\bsrc="([^"]+)"[^>]*>/gu)].map((match) => match[1]);

if (scripts.length !== 1) {
  throw new Error(`expected exactly one initial script in dist/index.html, found ${scripts.length}`);
}

const relativeScript = normalize(scripts[0].replace(/^\/+/, ""));
const scriptPath = resolve(distRoot, relativeScript);
const rawLimit = 500_000;
const gzipLimit = 150_000;
const staticScripts = collectStaticScripts(scriptPath);
const sizes = staticScripts.map((path) => {
  const source = readFileSync(path);
  return { path: relative(distRoot, path).replaceAll("\\", "/"), raw: source.byteLength, gzip: gzipSync(source, { level: 9 }).byteLength };
});
const rawBytes = sizes.reduce((sum, size) => sum + size.raw, 0);
const gzipBytes = sizes.reduce((sum, size) => sum + size.gzip, 0);

for (const size of sizes) console.log(`initial script ${size.path}: ${size.raw} bytes raw, ${size.gzip} bytes gzip`);
console.log(`initial static JS total: ${rawBytes} bytes raw, ${gzipBytes} bytes gzip`);
if (rawBytes > rawLimit || gzipBytes > gzipLimit) {
  throw new Error(`initial static JS exceeds budget: raw <= ${rawLimit}, gzip <= ${gzipLimit}`);
}

function collectStaticScripts(entryPath) {
  const seen = new Set();
  const visit = (path) => {
    requireWithinDist(path);
    if (seen.has(path)) return;
    seen.add(path);
    const source = readFileSync(path, "utf8");
    const imports = [
      ...source.matchAll(/\bimport(?!\s*\()[^;]*?\bfrom\s*["']([^"']+)["']/gu),
      ...source.matchAll(/\bimport\s*["']([^"']+)["']/gu)
    ];
    for (const match of imports) {
      if (!match[1].startsWith(".")) continue;
      visit(resolve(dirname(path), match[1]));
    }
  };
  visit(entryPath);
  return [...seen].sort();
}

function requireWithinDist(path) {
  const child = relative(distRoot, path);
  if (child === "" || child === ".." || child.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`) || isAbsolute(child)) {
    throw new Error(`initial script escapes dist: ${path}`);
  }
}
