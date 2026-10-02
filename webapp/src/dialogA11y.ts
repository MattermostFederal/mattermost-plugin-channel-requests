import type React from 'react';
import {useRef} from 'react';

// Selector for the elements that can hold focus inside a dialog. Used by the
// focus trap to find the first/last stop. Mirrors the common WAI-ARIA
// focusable set; [tabindex="-1"] is excluded because it is programmatic-only.
const FOCUSABLE_SELECTOR =
    'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

// useDialogFocusTrap wires the keyboard affordances every request modal needs:
// Escape closes the dialog, and Tab / Shift+Tab are trapped so focus cycles
// within the modal instead of escaping to the page behind the overlay. It
// returns a ref to attach to the dialog element and the keydown handler to
// put on it.
//
// The handler yields to children that already consumed the Tab (the
// MemberPicker uses Tab to complete the highlighted candidate and calls
// preventDefault), so autocomplete completion still works inside the trap.
export function useDialogFocusTrap(onClose: () => void) {
    const dialogRef = useRef<HTMLDivElement>(null);

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
