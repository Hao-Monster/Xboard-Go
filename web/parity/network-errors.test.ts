import { describe, expect, it } from "vitest";
import { shouldRecordServerError } from "./network-errors";

describe("parity network error policy", () => {
  it("records only server failures from the application origin", () => {
    expect(shouldRecordServerError(500, "https://panel.example/admin", "https://panel.example/api/v1/config")).toBe(true);
    expect(shouldRecordServerError(503, "https://panel.example/admin", "https://cdn.example/avatar.png")).toBe(false);
  });

  it("ignores successful responses and responses before navigation", () => {
    expect(shouldRecordServerError(200, "https://panel.example/admin", "https://panel.example/api/v1/config")).toBe(false);
    expect(shouldRecordServerError(500, "about:blank", "https://panel.example/api/v1/config")).toBe(false);
  });
});
