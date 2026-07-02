import manifest from 'manifest';
import React from 'react';
import type {Store} from 'redux';

import type {PluginRegistry} from 'types/mattermost-webapp';

import {ChannelPicker, TeamPicker} from './ApprovalChannelPicker';
import {installChannelCreationOverride} from './channelCreationOverride';
import {HeaderIcon} from './HeaderIcon';
import {PrefixEditor} from './PrefixEditor';
import {RequestChannelModal} from './RequestChannelModal';
import reducer, {openRequestModal} from './store';

export default class Plugin {
    public async initialize(registry: PluginRegistry, store: Store) {
        registry.registerReducer(reducer);

        registry.registerRootComponent(RequestChannelModal);

        // For non-admins, reroute the native sidebar "Create new channel" action to the request
        // workflow and rename it to "Request new channel". Also injects a "Request new channel"
        // item when the native one has been stripped by permissions.
        installChannelCreationOverride(store);

        registry.registerChannelHeaderButtonAction(
            <HeaderIcon/>,
            () => {
                store.dispatch(openRequestModal());
            },
            'Request Channel',
            'Request the creation of a new channel',
        );

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
        }
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
