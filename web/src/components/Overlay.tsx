import { useEffect, useRef, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { focusableElements, useOverlayLayer } from "./overlayStack";

interface DrawerProps {
  title: string;
  className?: string;
  suspended: boolean;
  onClose: () => void;
  children: ReactNode;
}

interface ModalProps {
  title: string;
  suspended?: boolean;
  className?: string;
  role?: "dialog" | "alertdialog";
  restoreFocusRef?: RefObject<HTMLElement | null>;
  onClose: () => void;
  children: ReactNode;
}

export function Drawer({ title, className = "", suspended, onClose, children }: DrawerProps) {
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
      <section ref={contentRef} className={`drawer-panel ${className}`.trim()} role="dialog" aria-modal={!suspended} aria-label={title}>
        {children}
      </section>
    </div>,
    overlayRoot()
  );
}

export function Modal({ title, className = "", suspended = false, role = "dialog", restoreFocusRef, onClose, children }: ModalProps) {
  const contentRef = useRef<HTMLElement>(null);
  useDialogFocus(contentRef, !suspended, onClose, restoreFocusRef);
  useEffect(() => { if (contentRef.current) contentRef.current.inert = suspended; }, [suspended]);
  useDocumentScrollLock();
  return createPortal(
    <div className="overlay-layer modal-layer" data-testid="modal-layer">
      <button className="overlay-backdrop modal-backdrop" aria-label={`点击遮罩关闭${title}`} tabIndex={-1} onClick={onClose} />
      <section ref={contentRef} className={`modal-panel ${className}`.trim()} role={role} aria-modal={!suspended} aria-label={title}>
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

function useDialogFocus(
  ref: RefObject<HTMLElement | null>,
  active: boolean,
  onClose: () => void,
  restoreFocusRef?: RefObject<HTMLElement | null>
) {
  useOverlayLayer({ active, onEscape: onClose, trapRef: ref });

  useEffect(() => {
    const opener = restoreFocusRef?.current
      ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    const container = ref.current;
    const frame = window.requestAnimationFrame(() => {
      const currentContainer = ref.current;
      // A fast keyboard or pointer user may reach the dialog before this frame.
      // Do not steal that focus and redirect their following keystrokes.
      if (currentContainer !== null && !currentContainer.contains(document.activeElement)) {
        focusableElements(currentContainer)[0]?.focus();
      }
    });
    return () => {
      window.cancelAnimationFrame(frame);
      // The parent drawer may still be inert during this cleanup. Restore on the
      // next frame, after React has committed the overlay stack change, unless
      // the user has already moved focus to another connected element.
      window.requestAnimationFrame(() => {
        const active = document.activeElement;
        const focusIsUnset =
          active === null ||
          active === document.body ||
          active === document.documentElement ||
          !active.isConnected ||
          (container !== null && container.contains(active));
        if (focusIsUnset && opener?.isConnected === true) {
          opener.focus();
        }
      });
    };
  }, [ref, restoreFocusRef]);
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
