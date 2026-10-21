import manifest from 'manifest';
import React, {useState} from 'react';
import {Provider} from 'react-redux';
import {combineReducers, legacy_createStore as createStore} from 'redux';
import type {Reducer} from 'redux';

import reducer, {openAdminRequestModal, openRequestModal} from './store';
import type {GlobalState} from './store';

/**
 * Harness for components that read the plugin's Redux state.
 *
 * Builds a real store from the plugin reducer (under `plugins-<id>`, as
 * Mattermost registers it) plus static `entities`, so closing a modal
 * really unmounts it. Two hidden-label buttons reopen the modals, which lets
 * a test check that a reopened form starts empty.
 */

type Props = {
    entities?: Partial<GlobalState['entities']>;
    requestOpen?: boolean;
    adminChannelId?: string | null;
    children?: React.ReactNode;
};

const defaultEntities: GlobalState['entities'] = {
    teams: {
        currentTeamId: 'team1',
        teams: {team1: {id: 'team1', display_name: 'Team One', name: 'team-one'}},
        myMembers: {},
    },
    channels: {
        currentChannelId: 'chan1',
        channels: {chan1: {display_name: 'Town Square', type: 'O'}},
    },
};

export const StoreHarness = ({entities, requestOpen = false, adminChannelId = null, children}: Props) => {
    const [store] = useState(() => {
        const allEntities = {...defaultEntities, ...entities};
        const rootReducer = combineReducers({
            entities: (state: GlobalState['entities'] = allEntities) => state,
            [`plugins-${manifest.id}`]: reducer,
        }) as Reducer;
        return createStore(
            rootReducer,
            {
                entities: allEntities,
                [`plugins-${manifest.id}`]: {
                    ...reducer(undefined, {type: '@@INIT'}),
                    modalOpen: requestOpen,
                    adminModalChannelId: adminChannelId,
                },
            },
        );
    });

    return (
        <Provider store={store}>
            {children}
            <button
                aria-label='harness: open request modal'
                onClick={() => store.dispatch(openRequestModal())}
            />
            <button
                aria-label='harness: open admin modal'
                onClick={() => store.dispatch(openAdminRequestModal('chan1'))}
            />
        </Provider>
    );
};
