import { useEffect, useRef, type RefObject } from "react";

interface OverlayLayerEntry {
  token: symbol;
  getActive: () => boolean;
  getOnEscape: () => () => void;
  getTrapRef: () => RefObject<HTMLElement | null> | undefined;
}

const stack: OverlayLayerEntry[] = [];
let listenerInstalled = false;

export function focusableElements(container: HTMLElement | null): HTMLElement[] {
  if (container === null) {
    return [];
  }
  return Array.from(
    container.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
    )
  ).filter((element) => !element.hidden && element.getAttribute("aria-hidden") !== "true");
}

function handleDocumentKeyDown(event: KeyboardEvent) {
  const top = stack[stack.length - 1];
  if (top === undefined || !top.getActive()) {
    return;
  }
  if (event.key === "Escape") {
    if (event.isComposing || event.keyCode === 229) {
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    top.getOnEscape()();
    return;
  }
  if (event.key !== "Tab") {
    return;
  }
  const trapRef = top.getTrapRef();
  if (trapRef === undefined) {
    return;
  }
  const elements = focusableElements(trapRef.current);
  if (elements.length === 0) {
    event.preventDefault();
    return;
  }
  const first = elements[0];
  const last = elements[elements.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last?.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first?.focus();
  }
}

function syncListener() {
  if (stack.length > 0 && !listenerInstalled) {
    document.addEventListener("keydown", handleDocumentKeyDown, true);
    listenerInstalled = true;
  } else if (stack.length === 0 && listenerInstalled) {
    document.removeEventListener("keydown", handleDocumentKeyDown, true);
    listenerInstalled = false;
  }
}

export function useOverlayLayer(options: {
  active: boolean;
  onEscape: () => void;
  trapRef?: RefObject<HTMLElement | null>;
  open?: boolean;
}): void {
  const { active, onEscape, trapRef, open = true } = options;
  const optionsRef = useRef({ active, onEscape, trapRef });
  useEffect(() => {
    optionsRef.current = { active, onEscape, trapRef };
  }, [active, onEscape, trapRef]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const token = Symbol("overlay-layer");
    const entry: OverlayLayerEntry = {
      token,
      getActive: () => optionsRef.current.active,
      getOnEscape: () => optionsRef.current.onEscape,
      getTrapRef: () => optionsRef.current.trapRef,
    };
    stack.push(entry);
    syncListener();
    return () => {
      const index = stack.findIndex((item) => item.token === token);
      if (index !== -1) {
        stack.splice(index, 1);
      }
      syncListener();
    };
  }, [open]);
}
