import type {Store} from 'redux';

import {makeDebug} from './debug';
import {openTeamAdminRequestModal, openTeamCreationModal} from './store';
import type {GlobalState} from './store';

// The team sidebar dropdown contains two plugin-owned items:
//
//   1. "Request a Team" — replaces or intercepts the native "Create a team"
//      item for non-System Admin users. System Admins see the native create
//      flow unchanged.
//
//   2. "Request Team Admin" — injected next to "Manage members". Shown only
//      when the current user is NOT already a Team Admin or System Admin for
//      the active team.
//
// Both items are DOM-injected because Mattermost exposes no plugin hook into
// the team sidebar dropdown. The approach mirrors channelCreationOverride.ts:
// identify the menu by stable sibling text, scan on body mutations, and
// use a capture-phase click listener for reliable interception.

const CREATE_TEAM_LABEL = 'Create a team';
const MANAGE_MEMBERS_LABEL = 'Manage members';
const VIEW_MEMBERS_LABEL = 'View members'; // label shown to non-admin members
const REQUEST_TEAM_LABEL = 'Request a Team';
const REQUEST_TEAM_ADMIN_LABEL = 'Request Team Admin';

const CREATE_TEAM_MARK = 'data-mm-plugin-create-team-intercepted';
const REQUEST_TEAM_ADMIN_MARK = 'data-mm-plugin-request-team-admin';

const ITEM_SELECTOR = 'a, button, li, [role="menuitem"]';

// Stable text strings that identify the team dropdown (as opposed to the
// channel "+" dropdown which has its own sibling labels).
const TEAM_MENU_SIBLING_LABELS = [
    'Invite people',
    'Team settings',
    'Manage members',
    'Leave team',
    'Learn about teams',
];

const debug = makeDebug('team-menu');

function isSystemAdmin(store: Store): boolean {
    const state = store.getState() as GlobalState;
    const users = state?.entities?.users;
    const uid = users?.currentUserId ?? '';
    const roles = (uid ? users?.profiles?.[uid]?.roles : '') ?? '';
    return roles.split(' ').includes('system_admin');
}

function isTeamAdmin(store: Store): boolean {
    const state = store.getState() as GlobalState;
    const teamId = state?.entities?.teams?.currentTeamId ?? '';
    if (!teamId) {
        return false;
    }
    const myMember = state.entities?.teams?.myMembers?.[teamId];
    if (!myMember) {
        return false;
    }
    // scheme_admin and roles are non-optional on TeamMembership — no fallbacks needed.
    return myMember.scheme_admin || myMember.roles.split(' ').includes('team_admin');
}

// findTeamMenuRoot walks through the DOM looking for the team dropdown. It's
// identified by having at least 2 of the known sibling labels as descendants.
function findTeamMenuRoot(): HTMLElement | null {
    const items = Array.from(document.querySelectorAll<HTMLElement>(ITEM_SELECTOR));
    for (const item of items) {
        const label = (item.textContent ?? '').trim();
        if (!TEAM_MENU_SIBLING_LABELS.includes(label)) {
            continue;
        }

        // Walk up until we find an ancestor with ≥2 team-menu sibling labels.
        let cursor: HTMLElement | null = item.parentElement;
        while (cursor && cursor !== document.body) {
            let count = 0;
            cursor.querySelectorAll<HTMLElement>(ITEM_SELECTOR).forEach((el) => {
                if (TEAM_MENU_SIBLING_LABELS.includes((el.textContent ?? '').trim())) {
                    count++;
                }
            });
            if (count >= 2) {
                return cursor;
            }
            cursor = cursor.parentElement;
        }
    }
    return null;
}

function replaceTextNode(root: HTMLElement, from: string, to: string): boolean {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    let node = walker.nextNode();
    while (node) {
        if (node.nodeValue && node.nodeValue.trim() === from) {
            node.nodeValue = node.nodeValue.replace(from, to);
            return true;
        }
        node = walker.nextNode();
    }
    return false;
}

function findItemByText(root: ParentNode, label: string): HTMLElement | null {
    const items = Array.from(root.querySelectorAll<HTMLElement>(ITEM_SELECTOR));
    return items.find((el) => (el.textContent ?? '').trim() === label) ?? null;
}

// interceptCreateTeam relabels the native "Create a team" item to
// "Request a Team" and marks it so the click listener can intercept it.
function interceptCreateTeam(menuRoot: HTMLElement): void {
    // Already intercepted — the label was already swapped on a prior scan.
    if (menuRoot.querySelector(`[${CREATE_TEAM_MARK}]`)) {
        return;
    }

    const native = findItemByText(menuRoot, CREATE_TEAM_LABEL);
    if (!native) {
        debug('interceptCreateTeam: native item not found');
        return;
    }

    if (replaceTextNode(native, CREATE_TEAM_LABEL, REQUEST_TEAM_LABEL)) {
        native.setAttribute(CREATE_TEAM_MARK, 'true');
        debug('relabeled "Create a team" → "Request a Team"');
    }
}

// injectRequestTeamAdmin clones the "Manage members" item and inserts a
// "Request Team Admin" entry directly below it. No-ops when already injected.
function injectRequestTeamAdmin(menuRoot: HTMLElement, store: Store): void {
    if (menuRoot.querySelector(`[${REQUEST_TEAM_ADMIN_MARK}]`)) {
        debug('injectRequestTeamAdmin: already injected');
        return;
    }

    // Admins see "Manage members"; regular members see "View members" — try both.
    const manageItem =
        findItemByText(menuRoot, MANAGE_MEMBERS_LABEL) ??
        findItemByText(menuRoot, VIEW_MEMBERS_LABEL);
    if (!manageItem) {
        debug('injectRequestTeamAdmin: neither "Manage members" nor "View members" found');
        return;
    }

    const container = manageItem.parentElement;
    if (!container) {
        return;
    }

    const injected = manageItem.cloneNode(true) as HTMLElement;
    injected.removeAttribute('id');
    injected.setAttribute(REQUEST_TEAM_ADMIN_MARK, 'true');
    injected.setAttribute('data-testid', 'teamMenuRequestTeamAdmin');

    const replaced =
        replaceTextNode(injected, MANAGE_MEMBERS_LABEL, REQUEST_TEAM_ADMIN_LABEL) ||
        replaceTextNode(injected, VIEW_MEMBERS_LABEL, REQUEST_TEAM_ADMIN_LABEL);
    if (!replaced) {
        // Fallback: overwrite text content while keeping the leading icon.
        const iconClone = injected.firstElementChild?.cloneNode(true) ?? null;
        injected.textContent = '';
        if (iconClone) {
            injected.appendChild(iconClone);
        }
        injected.appendChild(document.createTextNode(' ' + REQUEST_TEAM_ADMIN_LABEL));
    }

    if (injected instanceof HTMLAnchorElement) {
        injected.href = '#';
    }

    injected.addEventListener(
        'click',
        (event) => {
            event.preventDefault();
            event.stopImmediatePropagation();
            document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
            store.dispatch(openTeamAdminRequestModal());
        },
        true,
    );

    // Insert immediately after "Manage members".
    if (manageItem.nextSibling) {
        container.insertBefore(injected, manageItem.nextSibling);
    } else {
        container.appendChild(injected);
    }

    debug('injected "Request Team Admin" after "Manage members"');
}

// processTeamMenu scans the document for the team dropdown and applies both
// DOM patches: the "Create a team" label swap and the "Request Team Admin"
// injection.
// Set to true the first time processTeamMenu successfully finds the team dropdown.
// Used by the install-time timeout to detect silent failures.
let menuFoundOnce = false;

function processTeamMenu(store: Store): void {
    const menuRoot = findTeamMenuRoot();
    if (!menuRoot) {
        return;
    }
    menuFoundOnce = true;

    // Non-admins get the "Request a Team" relabel.
    if (!isSystemAdmin(store)) {
        interceptCreateTeam(menuRoot);
    }

    // "Request Team Admin" is injected for members who are not already
    // Team Admin or System Admin.
    if (!isSystemAdmin(store) && !isTeamAdmin(store)) {
        injectRequestTeamAdmin(menuRoot, store);
    }
}

export function installTeamMenuOverride(store: Store): void {
    debug('installing team-menu override');

    processTeamMenu(store);

    let scheduled = false;
    const observer = new MutationObserver((mutations) => {
        if (scheduled) {
            return;
        }
        const addedElements = mutations.some((m) =>
            Array.from(m.addedNodes).some((n) => n.nodeType === Node.ELEMENT_NODE),
        );
        if (!addedElements) {
            return;
        }
        scheduled = true;
        requestAnimationFrame(() => {
            scheduled = false;
            processTeamMenu(store);
        });
    });
    observer.observe(document.body, {childList: true, subtree: true});

    // Warn once if the team dropdown was never found — likely means MM changed
    // its DOM structure and "Request a Team" / "Request Team Admin" are broken.
    setTimeout(() => {
        if (!menuFoundOnce) {
            console.warn(
                '[mattermost-permissions] Team sidebar dropdown not found after 10s. ' +
                '"Request a Team" and "Request Team Admin" labels may not be working. ' +
                'Check whether Mattermost changed its team menu DOM structure.',
            );
        }
    }, 10000);

    // Re-run when Redux team membership state changes so "Request Team Admin"
    // disappears immediately after a server-side promotion rather than persisting
    // until the user reloads the page.
    let prevMyMembers: unknown = null;
    store.subscribe(() => {
        const state = store.getState() as GlobalState;
        const myMembers = state?.entities?.teams?.myMembers;
        if (myMembers !== prevMyMembers) {
            prevMyMembers = myMembers;
            processTeamMenu(store);
        }
    });

    // Capture-phase listener: intercepts clicks on the relabeled "Create a team"
    // item and routes them to the team creation request modal.
    document.addEventListener(
        'click',
        (event) => {
            if (isSystemAdmin(store)) {
                return;
            }
            const target = event.target as HTMLElement | null;
            if (!target) {
                return;
            }

            const intercepted = target.closest<HTMLElement>(`[${CREATE_TEAM_MARK}]`);
            if (!intercepted) {
                return;
            }

            event.preventDefault();
            event.stopImmediatePropagation();
            document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
            store.dispatch(openTeamCreationModal());
            debug('routed "Create a team" click to Request Team modal');
        },
        true,
    );
}
