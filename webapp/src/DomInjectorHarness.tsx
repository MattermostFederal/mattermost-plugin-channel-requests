import React, {useEffect} from 'react';
import type {Store} from 'redux';

import {installChannelCreationOverride} from './channelCreationOverride';
import {installMembersPanelButton} from './membersPanelButton';

/**
 * Harness for the two DOM injectors.
 *
 * Mattermost's own markup is replaced by `markup`, written into the page
 * before the injector is installed with a fake store. Every dispatched
 * action, and every Escape keydown the injectors send to close a menu, is
 * recorded on `window.__harness` so the test can read it with
 * page.evaluate. `window.__harness.addMarkup(html)` appends more markup
 * later, to exercise the injectors' MutationObserver.
 */

export type HarnessRecord = {
    actions: Array<{type: string; channelId?: string}>;
    escapes: number;
};

type Props = {
    injector: 'channelCreation' | 'membersPanel';
    markup: string;
    state: unknown;
};

declare global {
    interface Window {
        __harness?: HarnessRecord & {addMarkup: (html: string) => void};
    }
}

export const DomInjectorHarness = ({injector, markup, state}: Props) => {
    useEffect(() => {
        const host = document.createElement('div');
        host.innerHTML = markup;
        document.body.appendChild(host);

        const record: HarnessRecord = {actions: [], escapes: 0};
        // eslint-disable-next-line no-underscore-dangle
        window.__harness = {
            ...record,
            addMarkup: (html: string) => {
                const extra = document.createElement('div');
                extra.innerHTML = html;
                document.body.appendChild(extra);
            },
        };
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                // eslint-disable-next-line no-underscore-dangle
                window.__harness!.escapes++;
            }
        });

        const store = {
            getState: () => state,
            dispatch: (action: {type: string}) => {
                // eslint-disable-next-line no-underscore-dangle
                window.__harness!.actions.push(action);
                return action;
            },
        } as unknown as Store;

        if (injector === 'channelCreation') {
            installChannelCreationOverride(store);
        } else {
            installMembersPanelButton(store);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    return <div data-testid='harness-ready'/>;
};
