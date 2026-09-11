import { useEffect, useState } from "react";
import { swrCache } from "../lib/swr";

export function TopProgressBar() {
  const [active, setActive] = useState(false);
  const [finished, setFinished] = useState(false);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    return swrCache.subscribeActivity((count) => {
      if (count > 0) {
        if (timer) clearTimeout(timer);
        setFinished(false);
        setActive(true);
      } else {
        setFinished(true);
        timer = setTimeout(() => {
          setActive(false);
          setFinished(false);
        }, 300);
      }
    });
  }, []);

  if (!active && !finished) return null;

  return (
    <div
      className={`top-progress-bar ${active ? "active" : ""} ${finished ? "finished" : ""}`}
      role="progressbar"
      aria-label="数据同步中"
      aria-hidden="true"
    />
  );
}
