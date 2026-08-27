export function secureRandomUUID(): string {
  const cryptoAPI = globalThis.crypto;
  if (typeof cryptoAPI?.randomUUID === "function") return cryptoAPI.randomUUID();
  if (typeof cryptoAPI?.getRandomValues !== "function") {
    throw new Error("当前浏览器无法安全生成请求标识，请使用支持 Web Crypto 的浏览器");
  }

  const bytes = cryptoAPI.getRandomValues(new Uint8Array(16));
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
