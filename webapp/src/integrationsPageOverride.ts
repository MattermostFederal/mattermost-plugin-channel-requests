import type {Store} from 'redux';

import {fetchEnabledRequestTypes} from './client';
import type {EnabledRequestTypes} from './client';
import {makeDebug} from './debug';
import {
    getChannelType,
    getCurrentChannelId,
    openBotRequestModal,
    openIncomingWebhookModal,
    openOutgoingWebhookModal,
} from './store';
import type {GlobalState} from './store';

// installIntegrationsPageOverride intercepts clicks on the Mattermost
// Integrations page for non-System Admin users and routes them to the
// appropriate request modal instead of the native creation flow.
//
// Identification is pathname-based on same-origin anchors only, so chat
// messages linking to external /integrations/... URLs are never hijacked.
//
// System Admins are never intercepted — they get the native flow.
// When bot or webhook requests are disabled, native links are left alone.
// Webhook interception only runs in public/private channels (DM/GM have no
// team-scoped target for the request forms).
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

function inTeamChannel(store: Store): boolean {
    const state = store.getState() as GlobalState;
    const channelId = getCurrentChannelId(state);
    const type = getChannelType(state, channelId);
    return type === 'O' || type === 'P';
}

function resolveSameOriginPath(href: string): string | null {
    if (!href || href.startsWith('#') || href.startsWith('javascript:')) {
        return null;
    }
    try {
        const url = new URL(href, window.location.origin);
        if (url.origin !== window.location.origin) {
            return null;
        }
        return url.pathname;
    } catch {
        return null;
    }
}

function detectIntegrationKind(pathname: string): IntegrationKind | null {
    if (pathname.includes('/integrations/incoming_webhooks') || pathname.includes('/integrations/add_incoming_webhook')) {
        return 'incoming';
    }
    if (pathname.includes('/integrations/outgoing_webhooks') || pathname.includes('/integrations/add_outgoing_webhook')) {
        return 'outgoing';
    }
    if (pathname.includes('/integrations/bot_accounts') || pathname.includes('/integrations/add_bot_account')) {
        return 'bot';
    }
    return null;
}

function shouldIntercept(kind: IntegrationKind, enabled: EnabledRequestTypes, store: Store): boolean {
    if (kind === 'bot') {
        return enabled.bot;
    }
    if (!enabled.webhook) {
        return false;
    }
    // Webhook forms bind to the current channel with no picker — only team channels.
    return inTeamChannel(store);
}

export function installIntegrationsPageOverride(store: Store): () => void {
    debug('installing integrations-page override');

    let cachedEnabled: EnabledRequestTypes | null = null;
    let disposed = false;

    const refreshEnabled = () => {
        void fetchEnabledRequestTypes().then((enabled) => {
            if (!disposed) {
                cachedEnabled = enabled;
            }
        });
    };
    refreshEnabled();

    const onClick = (event: MouseEvent) => {
        if (isSystemAdmin(store)) {
            return;
        }
        // Preserve open-in-new-tab / open-in-new-window gestures.
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) {
            return;
        }

        const target = event.target as HTMLElement | null;
        if (!target) {
            return;
        }

        const link = target.closest<HTMLAnchorElement>('a');
        if (!link) {
            return;
        }

        const pathname = resolveSameOriginPath(link.getAttribute('href') ?? '');
        if (!pathname) {
            return;
        }
        const kind = detectIntegrationKind(pathname);
        if (!kind) {
            return;
        }

        // Decide synchronously from cache. If config has not loaded yet, leave
        // the native link alone (fail open to Mattermost's own navigation).
        if (!cachedEnabled || !shouldIntercept(kind, cachedEnabled, store)) {
            refreshEnabled();
            return;
        }

        event.preventDefault();
        event.stopImmediatePropagation();

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
        refreshEnabled();
    };

    document.addEventListener('click', onClick, true);
    return () => {
        disposed = true;
        document.removeEventListener('click', onClick, true);
        debug('removed integrations-page override');
    };
}
