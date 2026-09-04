export function shouldRecordServerError(status: number, pageURL: string, responseURL: string): boolean {
  if (status < 500 || !pageURL.startsWith("http")) return false;
  return new URL(responseURL).origin === new URL(pageURL).origin;
}
