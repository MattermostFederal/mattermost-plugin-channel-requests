import type {Store} from 'redux';

import {fetchEnabledRequestTypes} from './client';
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
// When bot or webhook requests are disabled, native links are left alone.
// Returns a disposer that removes the listener (call from Plugin.uninitialize).

const debug = makeDebug('integrations-page');

type IntegrationKind = 'bot' | 'incoming' | 'outgoing';

function isSystemAdmin(store: Store): boolean {
    const state = store.getState() as GlobalState;
    const users = state?.entities?.users;
    const uid = users?.currentUserId ?? '';
    const roles = (uid ? users?.profiles?.[uid]?.roles : '') ?? '';
    return roles.split(' ').includes('system_admin');
}

function detectIntegrationKind(href: string): IntegrationKind | null {
    if (href.includes('/integrations/incoming_webhooks') || href.includes('/integrations/add_incoming_webhook')) {
        return 'incoming';
    }
    if (href.includes('/integrations/outgoing_webhooks') || href.includes('/integrations/add_outgoing_webhook')) {
        return 'outgoing';
    }
    if (href.includes('/integrations/bot_accounts') || href.includes('/integrations/add_bot_account')) {
        return 'bot';
    }
    return null;
}

export function installIntegrationsPageOverride(store: Store): () => void {
    debug('installing integrations-page override');

    const onClick = (event: MouseEvent) => {
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
        const kind = detectIntegrationKind(href);
        if (!kind) {
            return;
        }

        // Hold the click while we check enable toggles. If the type is
        // disabled, fall through to the native Integrations page.
        event.preventDefault();
        event.stopImmediatePropagation();

        void fetchEnabledRequestTypes().then((enabled) => {
            if (kind === 'bot' && !enabled.bot) {
                debug('bot requests disabled — leaving native bot accounts link');
                window.location.assign(href);
                return;
            }
            if ((kind === 'incoming' || kind === 'outgoing') && !enabled.webhook) {
                debug('webhook requests disabled — leaving native webhook link');
                window.location.assign(href);
                return;
            }

            if (kind === 'incoming') {
                debug('intercepted incoming webhook click');
                store.dispatch(openIncomingWebhookModal());
            } else if (kind === 'outgoing') {
                debug('intercepted outgoing webhook click');
                store.dispatch(openOutgoingWebhookModal());
            } else {
                debug('intercepted bot accounts click');
                store.dispatch(openBotRequestModal());
            }
        });
    };

    document.addEventListener('click', onClick, true);
    return () => {
        document.removeEventListener('click', onClick, true);
        debug('removed integrations-page override');
    };
}
