import { useState } from "react";

export function BrandMark({ appName, logo }: { appName: string; logo: string | null }) {
  const [failedLogo, setFailedLogo] = useState<string | null>(null);
  const normalizedLogo = logo?.trim() ?? "";

  if (normalizedLogo !== "" && failedLogo !== normalizedLogo) {
    return <img
      className="brand-logo"
      src={normalizedLogo}
      alt={`${appName} LOGO`}
      referrerPolicy="no-referrer"
      onError={() => setFailedLogo(normalizedLogo)}
    />;
  }

  return <span className="brand-mark" aria-hidden="true">{Array.from(appName.trim())[0] ?? "X"}</span>;
}
