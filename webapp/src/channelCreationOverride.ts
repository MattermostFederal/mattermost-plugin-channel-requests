import type {Store} from 'redux';

import {openRequestModal} from './store';

// The native Mattermost "+" sidebar dropdown item we reroute for non-admins. Mattermost exposes no
// plugin API to modify this menu, so we adjust it in the DOM. The menu item has a stable element id
// (rendered by the sidebar "browse or create channel" menu); we match on that, and fall back to the
// visible label for other layouts.
const ITEM_ID = 'createNewChannelMenuItem';
const NATIVE_LABEL = 'Create new channel';
const REQUEST_LABEL = 'Request new channel';

// Menu items render as a <button>/<a>/<li>/[role=menuitem] depending on the Mattermost version.
const ITEM_SELECTOR = 'a, button, li, [role="menuitem"]';

type UsersState = {
    currentUserId: string;
    profiles: Record<string, {roles?: string}>;
};

type MinimalState = {
    entities?: {
        users?: UsersState;
    };
};

function isCurrentUserAdmin(store: Store): boolean {
    const state = store.getState() as MinimalState;
    const users = state?.entities?.users;
    const currentUserId = users?.currentUserId ?? '';
    const roles = users?.profiles?.[currentUserId]?.roles ?? '';
    return roles.split(' ').includes('system_admin');
}

// matchCreateChannelItem returns the create-channel menu item that contains the given element, if
// any — matching first by stable id, then by visible label.
function matchCreateChannelItem(el: HTMLElement | null): HTMLElement | null {
    if (!el) {
        return null;
    }
    const byId = el.closest<HTMLElement>(`#${ITEM_ID}`);
    if (byId) {
        return byId;
    }
    const item = el.closest<HTMLElement>(ITEM_SELECTOR);
    if (item) {
        const label = (item.textContent ?? '').trim();
        if (label === NATIVE_LABEL || label === REQUEST_LABEL) {
            return item;
        }
    }
    return null;
}

// relabelItem replaces the "Create new channel" label text node with "Request new channel", leaving
// the item's icon and structure untouched.
function relabelItem(item: HTMLElement): void {
    const walker = document.createTreeWalker(item, NodeFilter.SHOW_TEXT);
    let node = walker.nextNode();
    while (node) {
        if (node.nodeValue && node.nodeValue.trim() === NATIVE_LABEL) {
            node.nodeValue = node.nodeValue.replace(NATIVE_LABEL, REQUEST_LABEL);
            return;
        }
        node = walker.nextNode();
    }
}

// relabelWithin renames the create-channel menu item found in a newly rendered subtree.
function relabelWithin(root: HTMLElement): void {
    const byId = root.matches(`#${ITEM_ID}`) ? root : root.querySelector<HTMLElement>(`#${ITEM_ID}`);
    if (byId) {
        relabelItem(byId);
        return;
    }
    root.querySelectorAll<HTMLElement>(ITEM_SELECTOR).forEach((item) => {
        if ((item.textContent ?? '').trim() === NATIVE_LABEL) {
            relabelItem(item);
        }
    });
}

// installChannelCreationOverride reroutes the native "Create new channel" sidebar action to the
// channel request modal for non-admins, and renames the menu item to match.
export function installChannelCreationOverride(store: Store): void {
    // Rename the item whenever a menu is (re)rendered into the DOM.
    const observer = new MutationObserver((mutations) => {
        if (isCurrentUserAdmin(store)) {
            return;
        }
        for (const mutation of mutations) {
            mutation.addedNodes.forEach((added) => {
                if (added instanceof HTMLElement) {
                    relabelWithin(added);
                }
            });
        }
    });
    observer.observe(document.body, {childList: true, subtree: true});

    // Intercept the click in the capture phase so it never reaches Mattermost's own handler.
    document.addEventListener(
        'click',
        (event) => {
            if (isCurrentUserAdmin(store)) {
                return;
            }
            const item = matchCreateChannelItem(event.target as HTMLElement | null);
            if (!item) {
                return;
            }

            event.preventDefault();
            event.stopImmediatePropagation();

            // Close the still-open native dropdown, then open our request modal.
            document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
            store.dispatch(openRequestModal());
        },
        true,
    );
}
