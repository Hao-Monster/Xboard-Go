import { gzipSync } from "node:zlib";
import { readFileSync } from "node:fs";
import { dirname, isAbsolute, join, normalize, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distRoot = join(webRoot, "dist");
const html = readFileSync(join(distRoot, "index.html"), "utf8");
const entrySource = extractEntrySource(html);

const relativeScript = normalizeLocalSpecifier(entrySource);
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
      ...source.matchAll(/\b(?:import|export)(?!\s*\()[^;]*?\bfrom\s*["']([^"']+)["']/gu),
      ...source.matchAll(/\bimport\s*["']([^"']+)["']/gu)
    ];
    for (const match of imports) {
      const specifier = normalizeLocalSpecifier(match[1]);
      visit(match[1].startsWith("/") ? resolve(distRoot, specifier) : resolve(dirname(path), specifier));
    }
  };
  visit(entryPath);
  return [...seen].sort();
}

function normalizeLocalSpecifier(specifier) {
  if (/^(?:[a-z][a-z\d+.-]*:|\/\/)/iu.test(specifier)) {
    throw new Error(`initial script dependency must be local: ${specifier}`);
  }
  if (!specifier.startsWith(".") && !specifier.startsWith("/")) {
    throw new Error(`initial script dependency must use a relative or root path: ${specifier}`);
  }
  return normalize(specifier.replace(/^\/+/, "").replace(/[?#].*$/u, ""));
}

function extractEntrySource(document) {
  const lowerDocument = document.toLowerCase();
  const startMarker = "<script";
  const closeMarker = "</script>";
  const start = lowerDocument.indexOf(startMarker);
  if (start === -1 || lowerDocument.indexOf(startMarker, start + startMarker.length) !== -1) {
    throw new Error("dist/index.html must contain exactly one script element");
  }
  const tagEnd = lowerDocument.indexOf(">", start + startMarker.length);
  const close = tagEnd === -1 ? -1 : lowerDocument.indexOf(closeMarker, tagEnd + 1);
  if (tagEnd === -1 || close === -1 || lowerDocument.indexOf(closeMarker, close + closeMarker.length) !== -1) {
    throw new Error("dist/index.html must contain one well-formed script element");
  }
  if (document.slice(tagEnd + 1, close).trim() !== "") throw new Error("initial script must not contain an inline body");

  const openTag = document.slice(start, tagEnd + 1);
  const sourceMarker = ' src="';
  const sourceStart = openTag.indexOf(sourceMarker);
  const valueStart = sourceStart + sourceMarker.length;
  const valueEnd = sourceStart === -1 ? -1 : openTag.indexOf('"', valueStart);
  if (sourceStart === -1 || valueEnd === -1 || openTag.indexOf(sourceMarker, valueEnd + 1) !== -1) {
    throw new Error("initial script must contain exactly one double-quoted src");
  }
  return openTag.slice(valueStart, valueEnd);
}

function requireWithinDist(path) {
  const child = relative(distRoot, path);
  if (child === "" || child === ".." || child.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`) || isAbsolute(child)) {
    throw new Error(`initial script escapes dist: ${path}`);
  }
}
