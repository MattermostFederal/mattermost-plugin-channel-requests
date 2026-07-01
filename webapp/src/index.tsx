import manifest from 'manifest';
import React from 'react';
import type {Store} from 'redux';

import type {PluginRegistry} from 'types/mattermost-webapp';

import {installChannelCreationOverride} from './channelCreationOverride';
import {HeaderIcon} from './HeaderIcon';
import {RequestChannelModal} from './RequestChannelModal';
import reducer, {openRequestModal} from './store';

export default class Plugin {
    public async initialize(registry: PluginRegistry, store: Store) {
        registry.registerReducer(reducer);

        registry.registerRootComponent(RequestChannelModal);

        // For non-admins, reroute the native sidebar "Create new channel" action to the request
        // workflow and rename it to "Request new channel".
        installChannelCreationOverride(store);

        registry.registerChannelHeaderButtonAction(
            <HeaderIcon/>,
            () => {
                store.dispatch(openRequestModal());
            },
            'Request Channel',
            'Request the creation of a new channel',
        );
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
