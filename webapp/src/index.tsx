import manifest from 'manifest';
import React from 'react';
import type {Store} from 'redux';

import type {PluginRegistry} from 'types/mattermost-webapp';

import {ChannelPicker, TeamPicker} from './ApprovalChannelPicker';
import {AutoApprovePicker} from './AutoApprovePicker';
import {installChannelCreationOverride} from './channelCreationOverride';
import {fetchEnabledRequestTypes} from './client';
import {HeaderIcon} from './HeaderIcon';
import {installMembersPanelButton} from './membersPanelButton';
import {PrefixEditor} from './PrefixEditor';
import {RequestChannelAdminModal} from './RequestChannelAdminModal';
import {RequestChannelModal} from './RequestChannelModal';
import {RequestTeamModal} from './RequestTeamModal';
import reducer, {getChannelType, getCurrentChannelId, openAdminRequestModal, openRequestModal, openTeamRequestModal} from './store';
import type {GlobalState} from './store';

export default class Plugin {
    public async initialize(registry: PluginRegistry, store: Store) {
        registry.registerReducer(reducer);

        registry.registerRootComponent(RequestChannelModal);
        registry.registerRootComponent(RequestChannelAdminModal);
        registry.registerRootComponent(RequestTeamModal);

        // Each feature is installed independently and defensively: a throw in
        // one (e.g. a DOM hack that trips over unexpected markup) must not abort
        // initialize() and take the others down with it. safe() logs and
        // continues. The Members-panel injector is installed FIRST so it's
        // never blocked by an earlier registration failing.
        const safe = (label: string, fn: () => void) => {
            try {
                fn();
            } catch (err) {
                // eslint-disable-next-line no-console
                console.error(`[channel-requests] ${label} failed to initialize`, err);
            }
        };

        // DOM injection of a "Request Admin" button into the native Members
        // right-hand sidebar (next to "Add"). Mattermost has no plugin slot
        // there, so this is a fragile hack that no-ops if the expected markup
        // isn't found — the channel-name menu item below stays as the supported
        // fallback.
        safe('members-panel button', () => installMembersPanelButton(store));

        // Fetch which request types the admin has enabled so we can hide the
        // channel-request entry points when the feature is off. Fails open
        // (channel enabled) on error — the server still enforces the gate on
        // submit, so a transient failure never hides a working feature.
        const enabled = await fetchEnabledRequestTypes();

        // Channel-request entry points are gated on the channel toggle. The
        // "Request Channel Admin" affordance below is a separate feature with
        // its own toggle (enabled.channelAdmin).
        if (enabled.channel) {
            // For non-admins, reroute the native sidebar "Create new channel" action to the request
            // workflow and rename it to "Request new channel". Also injects a "Request new channel"
            // item when the native one has been stripped by permissions.
            safe('channel-creation override', () => installChannelCreationOverride(store));

            safe('channel-header button', () => registry.registerChannelHeaderButtonAction(
                <HeaderIcon/>,
                () => {
                    store.dispatch(openRequestModal());
                },
                'Request Channel',
                'Request the creation of a new channel',
            ));
        }

        // Team-request entry point, gated on the team toggle. Registered
        // independently of the channel button so either can be enabled alone.
        if (enabled.team) {
            safe('team-header button', () => registry.registerChannelHeaderButtonAction(
                <HeaderIcon/>,
                () => {
                    store.dispatch(openTeamRequestModal());
                },
                'Request Team',
                'Request the creation of a new team',
            ));
        }

        // Channel name dropdown menu item, gated on the channel-admin toggle.
        // Members who can't manage the channel themselves can request that
        // someone be made a Channel Admin; the request goes to an admin for
        // approval. Shown only in public/private channels — DMs and group
        // messages have no Channel Admin role, so the item is hidden there via
        // shouldRender.
        if (enabled.channelAdmin) {
            safe('channel-header menu', () => registry.registerChannelHeaderMenuAction(
                'Request Channel Admin',
                (channelId: string) => {
                    store.dispatch(openAdminRequestModal(channelId));
                },
                (state: GlobalState) => {
                    const type = getChannelType(state, getCurrentChannelId(state));
                    return type === 'O' || type === 'P';
                },
            ));
        }

        // Custom admin console settings — replace plain-text fields
        // with structured pickers. Each maps to a settings_schema key
        // in plugin.json (whose type is "custom").
        if (registry.registerAdminConsoleCustomSetting) {
            registry.registerAdminConsoleCustomSetting(
                'ApprovalTeam',
                TeamPicker,
                {showTitle: true},
            );
            registry.registerAdminConsoleCustomSetting(
                'ApprovalChannel',
                ChannelPicker,
                {showTitle: true},
            );
            registry.registerAdminConsoleCustomSetting(
                'ChannelNamePrefixes',
                PrefixEditor,
                {showTitle: true},
            );
            registry.registerAdminConsoleCustomSetting(
                'AutoApproveUserIDs',
                AutoApprovePicker,
                {showTitle: true},
            );
        }
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
