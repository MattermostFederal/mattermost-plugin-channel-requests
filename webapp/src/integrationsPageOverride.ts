import type {Store} from 'redux';

import {makeDebug} from './debug';
import {openBotRequestModal, openIncomingWebhookModal, openOutgoingWebhookModal} from './store';
import type {GlobalState} from './store';

// installIntegrationsPageOverride intercepts clicks on the Mattermost
// Integrations page for non-System Admin users and routes them to the
// appropriate request modal instead of the native creation flow.
//
// Identification is href-based rather than text-based: the Integrations page
// sidebar links and landing-page cards both use <a> tags whose href contains
// the integration's URL segment (/integrations/incoming_webhooks, etc.). This
// is more stable than class or text matching, and covers both the sidebar nav
// and the main card grid in a single listener.
//
// System Admins are never intercepted — they get the native flow.

const debug = makeDebug('integrations-page');

function isSystemAdmin(store: Store): boolean {
    const state = store.getState() as GlobalState;
    const users = state?.entities?.users;
    const uid = users?.currentUserId ?? '';
    const roles = (uid ? users?.profiles?.[uid]?.roles : '') ?? '';
    return roles.split(' ').includes('system_admin');
}

export function installIntegrationsPageOverride(store: Store): void {
    debug('installing integrations-page override');

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

            // Walk up from the click target to find the nearest anchor.
            // Cards on the Integrations landing page are typically <a> wrappers,
            // and sidebar items are also <a> links — so this covers both.
            const link = target.closest<HTMLAnchorElement>('a');
            if (!link) {
                return;
            }

            const href = link.getAttribute('href') ?? '';

            let action = null;
            if (href.includes('/integrations/incoming_webhooks') || href.includes('/integrations/add_incoming_webhook')) {
                action = openIncomingWebhookModal();
                debug('intercepted incoming webhook click');
            } else if (href.includes('/integrations/outgoing_webhooks') || href.includes('/integrations/add_outgoing_webhook')) {
                action = openOutgoingWebhookModal();
                debug('intercepted outgoing webhook click');
            } else if (href.includes('/integrations/bot_accounts') || href.includes('/integrations/add_bot_account')) {
                action = openBotRequestModal();
                debug('intercepted bot accounts click');
            }

            if (!action) {
                return;
            }

            event.preventDefault();
            event.stopImmediatePropagation();
            store.dispatch(action);
        },
        true,
    );
}
