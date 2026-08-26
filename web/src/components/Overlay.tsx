import { useEffect, useRef, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";

interface DrawerProps {
  title: string;
  suspended: boolean;
  onClose: () => void;
  children: ReactNode;
}

interface ModalProps {
  title: string;
  className?: string;
  onClose: () => void;
  children: ReactNode;
}

export function Drawer({ title, suspended, onClose, children }: DrawerProps) {
  const contentRef = useRef<HTMLElement>(null);
  useDialogFocus(contentRef, !suspended, onClose);
  useDocumentScrollLock();
  useEffect(() => {
    if (contentRef.current !== null) {
      contentRef.current.inert = suspended;
    }
  }, [suspended]);

  return createPortal(
    <div className="overlay-layer drawer-layer" data-testid="drawer-layer">
      <button className="overlay-backdrop" aria-label="点击遮罩关闭服务器详情" tabIndex={-1} onClick={onClose} />
      <section ref={contentRef} className="drawer-panel" role="dialog" aria-modal={!suspended} aria-label={title}>
        {children}
      </section>
    </div>,
    overlayRoot()
  );
}

export function Modal({ title, className = "", onClose, children }: ModalProps) {
  const contentRef = useRef<HTMLElement>(null);
  useDialogFocus(contentRef, true, onClose);
  useDocumentScrollLock();
  return createPortal(
    <div className="overlay-layer modal-layer" data-testid="modal-layer">
      <button className="overlay-backdrop modal-backdrop" aria-label={`点击遮罩关闭${title}`} tabIndex={-1} onClick={onClose} />
      <section ref={contentRef} className={`modal-panel ${className}`.trim()} role="dialog" aria-modal="true" aria-label={title}>
        {children}
      </section>
    </div>,
    overlayRoot()
  );
}

let scrollLockDepth = 0;
let previousBodyOverflow = "";

function useDocumentScrollLock() {
  useEffect(() => {
    if (scrollLockDepth === 0) {
      previousBodyOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
    }
    scrollLockDepth += 1;
    return () => {
      scrollLockDepth = Math.max(0, scrollLockDepth - 1);
      if (scrollLockDepth === 0) document.body.style.overflow = previousBodyOverflow;
    };
  }, []);
}

function useDialogFocus(ref: RefObject<HTMLElement | null>, active: boolean, onClose: () => void) {
  const closeRef = useRef(onClose);
  useEffect(() => {
    closeRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const frame = window.requestAnimationFrame(() => {
      const container = ref.current;
      // A fast keyboard or pointer user may reach the dialog before this frame.
      // Do not steal that focus and redirect their following keystrokes.
      if (container !== null && !container.contains(document.activeElement)) {
        focusableElements(container)[0]?.focus();
      }
    });
    return () => {
      window.cancelAnimationFrame(frame);
      // The parent drawer may still be inert during this cleanup. Restore on the
      // next frame, after React has committed the overlay stack change.
      window.requestAnimationFrame(() => {
        if (opener?.isConnected === true) {
          opener.focus();
        }
      });
    };
  }, [ref]);

  useEffect(() => {
    if (!active) {
      return undefined;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopImmediatePropagation();
        closeRef.current();
        return;
      }
      if (event.key !== "Tab") {
        return;
      }
      const elements = focusableElements(ref.current);
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
    };
    document.addEventListener("keydown", onKeyDown, true);
    return () => document.removeEventListener("keydown", onKeyDown, true);
  }, [active, ref]);
}

function focusableElements(container: HTMLElement | null): HTMLElement[] {
  if (container === null) {
    return [];
  }
  return Array.from(
    container.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
    )
  ).filter((element) => !element.hidden && element.getAttribute("aria-hidden") !== "true");
}

function overlayRoot(): HTMLElement {
  const existing = document.getElementById("overlay-root");
  if (existing !== null) {
    return existing;
  }
  const root = document.createElement("div");
  root.id = "overlay-root";
  document.body.append(root);
  return root;
}
