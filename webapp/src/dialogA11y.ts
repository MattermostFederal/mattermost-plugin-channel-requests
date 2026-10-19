import type React from 'react';
import {useEffect, useRef} from 'react';

// Selector for the elements that can hold focus inside a dialog. Used by the
// focus trap to find the first/last stop. Mirrors the common WAI-ARIA
// focusable set; [tabindex="-1"] is excluded because it is programmatic-only.
const FOCUSABLE_SELECTOR =
    'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

// useDialogFocusTrap wires the keyboard affordances every request modal needs:
// Escape closes the dialog, and Tab / Shift+Tab are trapped so focus cycles
// within the modal instead of escaping to the page behind the overlay. On open
// it moves focus into the dialog (first focusable, else the dialog root) and
// restores the previously focused element on close.
//
// isOpen must be passed because every modal mounts while closed (root
// components). An empty dependency array would run once with dialogRef null
// and never re-run when the dialog actually opens.
//
// The handler yields to children that already consumed the Tab (the
// MemberPicker uses Tab to complete the highlighted candidate and calls
// preventDefault), so autocomplete completion still works inside the trap.
export function useDialogFocusTrap(onClose: () => void, isOpen: boolean) {
    const dialogRef = useRef<HTMLDivElement>(null);
    const previouslyFocused = useRef<HTMLElement | null>(null);

    useEffect(() => {
        if (!isOpen) {
            return undefined;
        }
        const root = dialogRef.current;
        if (!root) {
            return undefined;
        }
        previouslyFocused.current = document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null;

        const focusable = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
        const target = focusable[0] ?? root;
        // Defer so the dialog is in the DOM before focus moves (hub → child modal).
        const timer = window.setTimeout(() => {
            target.focus();
        }, 0);

        return () => {
            window.clearTimeout(timer);
            const prev = previouslyFocused.current;
            if (prev && typeof prev.focus === 'function' && document.contains(prev)) {
                prev.focus();
            }
        };
    }, [isOpen]);

    const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.key === 'Escape') {
            e.stopPropagation();
            onClose();
            return;
        }
        if (e.key !== 'Tab' || e.defaultPrevented) {
            return;
        }
        const root = dialogRef.current;
        if (!root) {
            return;
        }
        const focusable = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
        if (focusable.length === 0) {
            return;
        }
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        const active = document.activeElement as HTMLElement | null;
        if (e.shiftKey) {
            if (active === first || !root.contains(active)) {
                e.preventDefault();
                last.focus();
            }
        } else if (active === last || !root.contains(active)) {
            e.preventDefault();
            first.focus();
        }
    };

    return {dialogRef, onKeyDown};
}
